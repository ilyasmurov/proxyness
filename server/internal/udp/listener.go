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
	"smurov-proxy/pkg/udp/arq"
	"smurov-proxy/server/internal/db"
	"smurov-proxy/server/internal/stats"
)

const (
	arqRetransmitInterval = 10 * time.Millisecond
	arqAckInterval        = 25 * time.Millisecond
)

// Listener handles incoming QUIC-disguised UDP packets on a PacketConn.
type Listener struct {
	conn     net.PacketConn
	db       *db.DB
	tracker  *stats.Tracker
	sessions *SessionManager
}

type inPacket struct {
	data []byte
	addr net.Addr
}

// NewListener creates a new UDP Listener.
func NewListener(conn net.PacketConn, database *db.DB, tracker *stats.Tracker) *Listener {
	if uc, ok := conn.(*net.UDPConn); ok {
		uc.SetReadBuffer(4 * 1024 * 1024)
		uc.SetWriteBuffer(4 * 1024 * 1024)
	}
	return &Listener{
		conn:     conn,
		db:       database,
		tracker:  tracker,
		sessions: NewSessionManager(),
	}
}

// Serve reads UDP packets as fast as possible and feeds them into a buffered
// channel. A single processing goroutine drains the channel, preserving packet
// order for stream data writes while keeping the socket read loop non-blocking.
func (l *Listener) Serve() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			l.sessions.Cleanup(2 * time.Minute)
		}
	}()

	pktCh := make(chan inPacket, 4096)
	go l.processLoop(pktCh)

	buf := make([]byte, 2048)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			log.Printf("udp listener: read error: %v", err)
			close(pktCh)
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		pktCh <- inPacket{data: data, addr: addr}
	}
}

// processLoop handles packets sequentially, preserving write ordering.
func (l *Listener) processLoop(ch chan inPacket) {
	for pkt := range ch {
		l.handlePacket(pkt.data, pkt.addr)
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
		log.Printf("udp: decode packet connID=%d from %s: %v", connID, addr, err)
		return
	}

	switch pkt.Type {
	case pkgudp.MsgStreamOpen:
		// Record PktNum for ACK — stream open is sent through ARQ.
		// Without this, daemon's cwnd fills and blocks all sends.
		if sess.ARQ != nil && pkt.PktNum > 0 {
			sess.ARQ.RecordPktNum(pkt.PktNum)
		}
		log.Printf("udp: stream open connID=%d stream=%d from %s", connID, pkt.StreamID, addr)
		go l.handleStreamOpen(sess, pkt, addr) // async: dial blocks
	case pkgudp.MsgStreamData:
		if sess.ARQ != nil {
			sess.ARQ.HandleData(pkt) // records PktNum + delivers to recvBuf
		}
	case pkgudp.MsgStreamClose:
		// Record PktNum for ACK — stream close is sent through ARQ
		if sess.ARQ != nil && pkt.PktNum > 0 {
			sess.ARQ.RecordPktNum(pkt.PktNum)
		}
		log.Printf("udp: stream close connID=%d stream=%d", connID, pkt.StreamID)
		l.handleStreamClose(sess, pkt)
	case pkgudp.MsgAck:
		if sess.ARQ != nil {
			sess.ARQ.HandleAck(pkt.Data)
		}
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

	// Initialize ARQ Controller for this session.
	// sendFn uses a short write deadline so retransmit storms from dead sessions
	// can't block the shared socket and starve processLoop.
	sess.ARQ = arq.New(token, sessionKey, func(data []byte) error {
		sess.mu.Lock()
		clientAddr := sess.ClientAddr
		sess.mu.Unlock()
		_, err := l.conn.WriteTo(data, clientAddr)
		return err
	}, func(streamID uint32, data []byte) {
		st, ok := sess.GetStream(streamID)
		if !ok || st.WriteCh == nil {
			return
		}
		cp := make([]byte, len(data))
		copy(cp, data)
		select {
		case st.WriteCh <- cp:
		default:
			// write channel full — drop data to avoid blocking processLoop
			log.Printf("udp: stream %d write channel full, dropping %d bytes", streamID, len(data))
		}
	})

	// Start ARQ background goroutines
	go l.sessionRetransmitLoop(sess)
	go l.sessionAckLoop(sess)

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

// sessionRetransmitLoop periodically triggers ARQ retransmissions for a session.
func (l *Listener) sessionRetransmitLoop(sess *Session) {
	ticker := time.NewTicker(arqRetransmitInterval)
	defer ticker.Stop()
	statsTicker := time.NewTicker(2 * time.Second)
	defer statsTicker.Stop()
	done := sess.ARQ.Done()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			sess.ARQ.RetransmitTick()
		case <-statsTicker.C:
			cwnd, inFlight, slots, losses := sess.ARQ.CwndStats()
			sendBuf := sess.ARQ.SendBufLen()
			if inFlight > 0 || sendBuf > 0 {
				log.Printf("udp: [%d] cwnd=%d inFlight=%d slots=%d sendBuf=%d losses=%d", sess.Token, cwnd, inFlight, slots, sendBuf, losses)
			}
		}
	}
}

// sessionAckLoop periodically sends delayed ACKs for a session.
func (l *Listener) sessionAckLoop(sess *Session) {
	ticker := time.NewTicker(arqAckInterval)
	defer ticker.Stop()
	done := sess.ARQ.Done()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			sess.ARQ.AckTick()
		}
	}
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

	// Deduplicate: skip if stream already being processed or connected (retransmit)
	sess.mu.Lock()
	if st, exists := sess.streams[streamID]; exists && (st.Conn != nil || st.Dialing) {
		sess.mu.Unlock()
		return
	}
	if _, exists := sess.streams[streamID]; !exists {
		sess.streams[streamID] = &StreamState{
			Created: time.Now(),
			WriteCh: make(chan []byte, 1024), // create early so deliverFn can buffer data during dial
		}
		seq := uint32(0)
		sess.nextSeq[streamID] = &seq
	}
	st := sess.streams[streamID]
	if st.WriteCh == nil {
		st.WriteCh = make(chan []byte, 256)
	}
	st.Dialing = true
	sess.mu.Unlock()

	st.Type = msg.StreamType
	st.Addr = msg.Addr
	st.Port = msg.Port

	if sess.ARQ != nil {
		if err := sess.ARQ.CreateRecvBuffer(streamID); err != nil {
			// Already exists from previous attempt — not an error
		}
	}

	target := net.JoinHostPort(msg.Addr, fmt.Sprintf("%d", msg.Port))

	switch msg.StreamType {
	case pkgudp.StreamTypeTCP:
		conn, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial TCP %s: %v", target, err)
			l.sendResult(sess, streamID, false)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		go l.streamWriter(sess, streamID, st) // WriteCh already created
		l.sendResult(sess, streamID, true)
		go l.relayFromDest(sess, streamID, conn)

	case pkgudp.StreamTypeUDP:
		conn, err := net.DialTimeout("udp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial UDP %s: %v", target, err)
			l.sendResult(sess, streamID, false)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		go l.streamWriter(sess, streamID, st) // WriteCh already created
		l.sendResult(sess, streamID, true)
		go l.relayFromDest(sess, streamID, conn)

	default:
		log.Printf("udp: unknown stream type 0x%02x", msg.StreamType)
		l.sendResult(sess, streamID, false)
		sess.RemoveStream(streamID)
	}
}

// streamWriter drains the per-stream write channel and writes to the destination.
// This runs in its own goroutine so that slow destinations don't block processLoop.
func (l *Listener) streamWriter(sess *Session, streamID uint32, st *StreamState) {
	for data := range st.WriteCh {
		n, err := st.Conn.Write(data)
		if err != nil {
			log.Printf("udp: stream %d write to dest: %v", streamID, err)
			sess.RemoveStream(streamID)
			l.sendClose(sess, streamID)
			return
		}
		st.BytesIn += int64(n)
	}
}

// handleStreamClose closes the outbound connection for a stream.
func (l *Listener) handleStreamClose(sess *Session, pkt *pkgudp.Packet) {
	sess.RemoveStream(pkt.StreamID)
}

// relayFromDest reads from the destination connection and sends StreamData packets to the client via ARQ.
func (l *Listener) relayFromDest(sess *Session, streamID uint32, conn net.Conn) {
	defer func() {
		sess.RemoveStream(streamID)
		l.sendClose(sess, streamID)
	}()

	buf := make([]byte, 1340)
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buf)
		if n > 0 {
			st, ok := sess.GetStream(streamID)
			if !ok {
				return
			}
			st.BytesOut += int64(n)

			seq := sess.NextSeq(streamID)

			if sess.ARQ != nil {
				if sendErr := sess.ARQ.Send(pkgudp.MsgStreamData, streamID, seq, buf[:n]); sendErr != nil {
					log.Printf("udp: arq send stream=%d: %v", streamID, sendErr)
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// sendResult sends a single-byte result (0x01=ok, 0x00=fail) as StreamData to the client via ARQ.
// Uses NextSeq to avoid seq collision with relayFromDest which also uses NextSeq.
func (l *Listener) sendResult(sess *Session, streamID uint32, ok bool) {
	b := byte(0x00)
	if ok {
		b = 0x01
	}
	if sess.ARQ != nil {
		seq := sess.NextSeq(streamID)
		sess.ARQ.Send(pkgudp.MsgStreamData, streamID, seq, []byte{b}) //nolint:errcheck
	}
}

// sendClose sends a StreamClose packet to the client via ARQ.
func (l *Listener) sendClose(sess *Session, streamID uint32) {
	if sess.ARQ != nil {
		sess.ARQ.Send(pkgudp.MsgStreamClose, streamID, 0, nil) //nolint:errcheck
	}
}
