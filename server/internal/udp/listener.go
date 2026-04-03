package udp

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"

	"smurov-proxy/pkg/auth"
	pkgudp "smurov-proxy/pkg/udp"
	"smurov-proxy/server/internal/db"
	"smurov-proxy/server/internal/stats"
)

// Listener handles incoming QUIC-disguised UDP packets on a PacketConn.
type Listener struct {
	conn     net.PacketConn
	db       *db.DB
	tracker  *stats.Tracker
	sessions *SessionManager
}

// NewListener creates a new UDP Listener.
func NewListener(conn net.PacketConn, database *db.DB, tracker *stats.Tracker) *Listener {
	return &Listener{
		conn:     conn,
		db:       database,
		tracker:  tracker,
		sessions: NewSessionManager(),
	}
}

// Serve is the main read loop. Packets are processed synchronously to preserve
// write ordering for stream data. Only MsgStreamOpen (which dials) runs in a goroutine.
func (l *Listener) Serve() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			l.sessions.Cleanup(2 * time.Minute)
		}
	}()

	buf := make([]byte, 2048)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			log.Printf("udp listener: read error: %v", err)
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		l.handlePacket(data, addr)
	}
}

// handlePacket dispatches an incoming UDP datagram.
// It extracts the Connection ID from bytes 1-4 of the outer header.
// ConnID==0 means a handshake; otherwise looks up the session and dispatches.
func (l *Listener) handlePacket(data []byte, addr net.Addr) {
	if len(data) < 5 {
		return
	}

	connID := binary.BigEndian.Uint32(data[1:5])

	if connID == 0 {
		l.handleHandshake(data, addr)
		return
	}

	sess, ok := l.sessions.Get(connID)
	if !ok {
		return
	}

	// Update client address (roaming support)
	sess.mu.Lock()
	sess.ClientAddr = addr
	sess.LastSeen = time.Now()
	sess.mu.Unlock()

	pkt, err := pkgudp.DecodePacket(data, sess.SessionKey)
	if err != nil {
		log.Printf("udp: decode packet connID=%d: %v", connID, err)
		return
	}

	switch pkt.Type {
	case pkgudp.MsgStreamOpen:
		go l.handleStreamOpen(sess, pkt, addr) // async: dial blocks
	case pkgudp.MsgStreamData:
		l.handleStreamData(sess, pkt)
	case pkgudp.MsgStreamClose:
		l.handleStreamClose(sess, pkt)
	case pkgudp.MsgKeepalive:
		// no-op
	default:
		log.Printf("udp: unknown msg type 0x%02x from connID=%d", pkt.Type, connID)
	}
}

// handleHandshake processes a handshake packet: tries all active device keys
// to decrypt, validates auth, checks machine ID, derives session key, creates session, sends response.
func (l *Listener) handleHandshake(data []byte, addr net.Addr) {
	if len(data) < 5 {
		return
	}

	// Get all active device keys to try decryption.
	keys, err := l.db.GetActiveKeys()
	if err != nil {
		log.Printf("udp: get active keys: %v", err)
		return
	}

	// The handshake packet is encrypted with the device key.
	// Try each key until decryption succeeds.
	var pkt *pkgudp.Packet
	var matchedKeyHex string
	var matchedKeyBytes []byte
	for _, keyHex := range keys {
		kb, err := hex.DecodeString(keyHex)
		if err != nil {
			continue
		}
		p, err := pkgudp.DecodePacket(data, kb)
		if err == nil && p.Type == pkgudp.MsgHandshake {
			pkt = p
			matchedKeyHex = keyHex
			matchedKeyBytes = kb
			break
		}
	}

	if pkt == nil {
		log.Printf("udp: handshake auth failed from %s: no matching key", addr)
		return
	}

	req, err := pkgudp.DecodeHandshakeRequest(pkt.Data)
	if err != nil {
		log.Printf("udp: decode handshake from %s: %v", addr, err)
		return
	}

	// Validate auth message timestamp to prevent replay attacks.
	rawAuth := pkgudp.RawAuth(pkt.Data)
	if err := auth.ValidateAuthMessage(matchedKeyHex, rawAuth); err != nil {
		log.Printf("udp: handshake auth failed from %s: %v", addr, err)
		return
	}

	device, err := l.db.GetDeviceByKey(matchedKeyHex)
	if err != nil {
		log.Printf("udp: device lookup: %v", err)
		return
	}

	// Check machine ID
	mid := fmt.Sprintf("%x", req.MachineID)
	if err := l.checkMachineID(device.ID, device.Name, mid); err != nil {
		log.Printf("udp: device %s machine check failed: %v", device.Name, err)
		return
	}

	// Generate server ephemeral key
	serverPriv, serverPubBytes, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		log.Printf("udp: generate ephemeral key: %v", err)
		return
	}

	// Derive session key from server priv + client pub
	sessionKey, err := pkgudp.DeriveSessionKey(serverPriv, req.EphemeralPub)
	if err != nil {
		log.Printf("udp: derive session key: %v", err)
		return
	}

	// Create session
	token := l.sessions.Create(sessionKey, device.ID)
	sess, _ := l.sessions.Get(token)
	sess.mu.Lock()
	sess.ClientAddr = addr
	sess.mu.Unlock()

	// Build and send HandshakeResponse encrypted with device key.
	resp := &pkgudp.HandshakeResponse{
		EphemeralPub: serverPubBytes,
		SessionToken: token,
	}
	respPkt := &pkgudp.Packet{
		ConnID: 0,
		Type:   pkgudp.MsgHandshake,
		Data:   resp.Encode(),
	}
	encoded, err := pkgudp.EncodePacket(respPkt, matchedKeyBytes)
	if err != nil {
		log.Printf("udp: encode handshake response: %v", err)
		l.sessions.Remove(token)
		return
	}

	if _, err := l.conn.WriteTo(encoded, addr); err != nil {
		log.Printf("udp: send handshake response to %s: %v", addr, err)
		l.sessions.Remove(token)
		return
	}

	log.Printf("udp: session created token=%d device=%s from %s", token, device.Name, addr)
}

// checkMachineID validates or binds a machine fingerprint to a device.
func (l *Listener) checkMachineID(deviceID int, deviceName, machineID string) error {
	stored, err := l.db.GetDeviceMachineID(deviceID)
	if err != nil {
		return err
	}
	if stored == "" {
		log.Printf("udp: device %s bound to machine %s", deviceName, machineID[:8])
		return l.db.SetDeviceMachineID(deviceID, machineID)
	}
	if stored != machineID {
		return fmt.Errorf("device bound to different machine")
	}
	return nil
}

// handleStreamOpen decodes a StreamOpenMsg, creates a stream in the session,
// dials the destination, and starts a relay goroutine from destination back to client.
func (l *Listener) handleStreamOpen(sess *Session, pkt *pkgudp.Packet, addr net.Addr) {
	msg, err := pkgudp.DecodeStreamOpen(pkt.Data)
	if err != nil {
		log.Printf("udp: decode stream open: %v", err)
		return
	}

	streamID := pkt.StreamID

	// Ensure stream slot exists
	sess.mu.Lock()
	if _, exists := sess.streams[streamID]; !exists {
		sess.streams[streamID] = &StreamState{Created: time.Now()}
	}
	st := sess.streams[streamID]
	sess.mu.Unlock()

	st.Type = msg.StreamType
	st.Addr = msg.Addr
	st.Port = msg.Port

	target := net.JoinHostPort(msg.Addr, fmt.Sprintf("%d", msg.Port))

	switch msg.StreamType {
	case pkgudp.StreamTypeTCP:
		conn, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial TCP %s: %v", target, err)
			l.sendResult(sess, streamID, false, addr)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		l.sendResult(sess, streamID, true, addr)
		go l.relayFromDest(sess, streamID, conn, addr)

	case pkgudp.StreamTypeUDP:
		conn, err := net.DialTimeout("udp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial UDP %s: %v", target, err)
			l.sendResult(sess, streamID, false, addr)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		l.sendResult(sess, streamID, true, addr)
		go l.relayFromDest(sess, streamID, conn, addr)

	default:
		log.Printf("udp: unknown stream type 0x%02x", msg.StreamType)
		l.sendResult(sess, streamID, false, addr)
		sess.RemoveStream(streamID)
	}
}

// handleStreamData writes incoming data to the stream's outbound connection.
func (l *Listener) handleStreamData(sess *Session, pkt *pkgudp.Packet) {
	st, ok := sess.GetStream(pkt.StreamID)
	if !ok || st.Conn == nil {
		return
	}

	n, err := st.Conn.Write(pkt.Data)
	if err != nil {
		log.Printf("udp: write to dest stream=%d: %v", pkt.StreamID, err)
		sess.RemoveStream(pkt.StreamID)
		l.sendClose(sess, pkt.StreamID)
		return
	}
	st.BytesIn += int64(n)
}

// handleStreamClose closes the outbound connection for a stream.
func (l *Listener) handleStreamClose(sess *Session, pkt *pkgudp.Packet) {
	sess.RemoveStream(pkt.StreamID)
}

// relayFromDest reads from the destination connection and sends StreamData packets to the client.
func (l *Listener) relayFromDest(sess *Session, streamID uint32, conn net.Conn, clientAddr net.Addr) {
	defer func() {
		sess.RemoveStream(streamID)
		l.sendClose(sess, streamID)
	}()

	buf := make([]byte, 1344)
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buf)
		if n > 0 {
			st, ok := sess.GetStream(streamID)
			if !ok {
				return
			}
			st.BytesOut += int64(n)

			// Get latest client address (roaming)
			sess.mu.Lock()
			addr := sess.ClientAddr
			sess.mu.Unlock()

			pkt := &pkgudp.Packet{
				ConnID:   sess.Token,
				Type:     pkgudp.MsgStreamData,
				StreamID: streamID,
				Data:     buf[:n],
			}
			encoded, encErr := pkgudp.EncodePacket(pkt, sess.SessionKey)
			if encErr != nil {
				log.Printf("udp: encode relay pkt stream=%d: %v", streamID, encErr)
				return
			}
			if _, writeErr := l.conn.WriteTo(encoded, addr); writeErr != nil {
				log.Printf("udp: write relay to client stream=%d: %v", streamID, writeErr)
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// sendResult sends a single-byte result (0x01=ok, 0x00=fail) as StreamData to the client.
func (l *Listener) sendResult(sess *Session, streamID uint32, ok bool, addr net.Addr) {
	b := byte(0x00)
	if ok {
		b = 0x01
	}
	pkt := &pkgudp.Packet{
		ConnID:   sess.Token,
		Type:     pkgudp.MsgStreamData,
		StreamID: streamID,
		Data:     []byte{b},
	}
	encoded, err := pkgudp.EncodePacket(pkt, sess.SessionKey)
	if err != nil {
		log.Printf("udp: encode result stream=%d: %v", streamID, err)
		return
	}
	if _, err := l.conn.WriteTo(encoded, addr); err != nil {
		log.Printf("udp: send result stream=%d: %v", streamID, err)
	}
}

// sendClose sends a StreamClose packet to the client.
func (l *Listener) sendClose(sess *Session, streamID uint32) {
	sess.mu.Lock()
	addr := sess.ClientAddr
	sess.mu.Unlock()

	if addr == nil {
		return
	}

	pkt := &pkgudp.Packet{
		ConnID:   sess.Token,
		Type:     pkgudp.MsgStreamClose,
		StreamID: streamID,
	}
	encoded, err := pkgudp.EncodePacket(pkt, sess.SessionKey)
	if err != nil {
		log.Printf("udp: encode close stream=%d: %v", streamID, err)
		return
	}
	if _, err := l.conn.WriteTo(encoded, addr); err != nil {
		log.Printf("udp: send close stream=%d: %v", streamID, err)
	}
}

