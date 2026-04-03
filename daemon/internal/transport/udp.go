package transport

import (
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pkgudp "smurov-proxy/pkg/udp"
)

const (
	udpMaxPayload  = 1344 // max data bytes per MsgStreamData packet
	udpKeepalive   = 15 * time.Second
	udpHandshakeTO = 3 * time.Second
	udpReadBuf     = 65535
)

// UDPTransport implements Transport over a single multiplexed UDP connection.
type UDPTransport struct {
	conn      *net.UDPConn
	sessionKey []byte
	connID    uint32
	devKey    []byte // 32-byte device key derived from hex string

	mu       sync.Mutex
	streams  map[uint32]*udpStream
	nextID   uint32

	closed   atomic.Bool
	done     chan struct{}
}

func NewUDPTransport() *UDPTransport {
	return &UDPTransport{
		streams: make(map[uint32]*udpStream),
		done:    make(chan struct{}),
	}
}

// Connect dials the server via UDP, performs ECDH handshake, starts background loops.
func (t *UDPTransport) Connect(server, key string, machineID [16]byte) error {
	devKey, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("decode device key: %w", err)
	}
	t.devKey = devKey

	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return fmt.Errorf("resolve server addr: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return fmt.Errorf("udp dial: %w", err)
	}
	t.conn = conn

	// Generate ephemeral X25519 keypair
	priv, pubBytes, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		conn.Close()
		return fmt.Errorf("generate ephemeral key: %w", err)
	}

	// Build and encode HandshakeRequest
	req := &pkgudp.HandshakeRequest{
		EphemeralPub: pubBytes,
		DeviceKey:    key,
		MachineID:    machineID,
	}
	reqData, err := req.Encode()
	if err != nil {
		conn.Close()
		return fmt.Errorf("encode handshake request: %w", err)
	}

	// Send as Packet with ConnID=0, Type=MsgHandshake, encrypted with device key
	pkt := &pkgudp.Packet{
		ConnID:   0,
		Type:     pkgudp.MsgHandshake,
		StreamID: 0,
		Seq:      0,
		Data:     reqData,
	}
	encoded, err := pkgudp.EncodePacket(pkt, devKey)
	if err != nil {
		conn.Close()
		return fmt.Errorf("encode handshake packet: %w", err)
	}

	if _, err := conn.Write(encoded); err != nil {
		conn.Close()
		return fmt.Errorf("send handshake: %w", err)
	}

	// Wait for handshake response (3s timeout)
	conn.SetReadDeadline(time.Now().Add(udpHandshakeTO))
	buf := make([]byte, udpReadBuf)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) // clear deadline
	if err != nil {
		conn.Close()
		return fmt.Errorf("read handshake response: %w", err)
	}

	// Decrypt response with device key
	respPkt, err := pkgudp.DecodePacket(buf[:n], devKey)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode handshake response packet: %w", err)
	}
	if respPkt.Type != pkgudp.MsgHandshake {
		conn.Close()
		return fmt.Errorf("unexpected handshake response type: 0x%02x", respPkt.Type)
	}

	resp, err := pkgudp.DecodeHandshakeResponse(respPkt.Data)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode handshake response: %w", err)
	}

	// Derive session key via ECDH
	sessionKey, err := pkgudp.DeriveSessionKey(priv, resp.EphemeralPub)
	if err != nil {
		conn.Close()
		return fmt.Errorf("derive session key: %w", err)
	}

	t.sessionKey = sessionKey
	t.connID = resp.SessionToken

	go t.recvLoop()
	go t.keepaliveLoop()

	return nil
}

// recvLoop reads incoming UDP packets and dispatches them to streams.
func (t *UDPTransport) recvLoop() {
	buf := make([]byte, udpReadBuf)
	for {
		n, err := t.conn.Read(buf)
		if err != nil {
			if !t.closed.Load() {
				// Close all streams on read error
				t.mu.Lock()
				for _, s := range t.streams {
					s.mu.Lock()
					s.closeRecvChLocked()
					s.mu.Unlock()
				}
				t.streams = make(map[uint32]*udpStream)
				t.mu.Unlock()
			}
			return
		}

		pkt, err := pkgudp.DecodePacket(buf[:n], t.sessionKey)
		if err != nil {
			continue
		}

		t.mu.Lock()
		s, ok := t.streams[pkt.StreamID]
		t.mu.Unlock()

		switch pkt.Type {
		case pkgudp.MsgStreamData:
			if ok {
				// Non-blocking send to avoid deadlock if stream is being closed
				select {
				case s.recvCh <- append([]byte(nil), pkt.Data...):
				default:
				}
			}
		case pkgudp.MsgStreamClose:
			if ok {
				t.mu.Lock()
				delete(t.streams, pkt.StreamID)
				t.mu.Unlock()
				s.mu.Lock()
				s.closeRecvChLocked()
				s.mu.Unlock()
			}
		}
	}
}

// keepaliveLoop sends MsgKeepalive packets every 15s to prevent NAT timeout.
func (t *UDPTransport) keepaliveLoop() {
	ticker := time.NewTicker(udpKeepalive)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			pkt := &pkgudp.Packet{
				ConnID: t.connID,
				Type:   pkgudp.MsgKeepalive,
			}
			data, err := pkgudp.EncodePacket(pkt, t.sessionKey)
			if err != nil {
				continue
			}
			t.conn.Write(data) //nolint:errcheck
		}
	}
}

// OpenStream allocates a stream ID, sends MsgStreamOpen, and returns a udpStream.
func (t *UDPTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("transport closed")
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	recvCh := make(chan []byte, 64)
	s := &udpStream{
		t:      t,
		id:     id,
		recvCh: recvCh,
	}
	t.streams[id] = s
	t.mu.Unlock()

	// Send MsgStreamOpen
	payload := (&pkgudp.StreamOpenMsg{
		StreamType: streamType,
		Addr:       addr,
		Port:       port,
	}).Encode()

	pkt := &pkgudp.Packet{
		ConnID:   t.connID,
		Type:     pkgudp.MsgStreamOpen,
		StreamID: id,
		Seq:      0,
		Data:     payload,
	}
	encoded, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		t.mu.Lock()
		delete(t.streams, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("encode stream open: %w", err)
	}
	if _, err := t.conn.Write(encoded); err != nil {
		t.mu.Lock()
		delete(t.streams, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("send stream open: %w", err)
	}

	// For TCP streams wait for a single-byte result on the receive channel
	if streamType == pkgudp.StreamTypeTCP {
		select {
		case data, ok := <-recvCh:
			if !ok {
				t.mu.Lock()
				delete(t.streams, id)
				t.mu.Unlock()
				return nil, fmt.Errorf("stream closed before connect result")
			}
			if len(data) == 0 || data[0] != 0x01 {
				t.mu.Lock()
				delete(t.streams, id)
				t.mu.Unlock()
				return nil, fmt.Errorf("connect rejected: %s:%d", addr, port)
			}
		case <-time.After(10 * time.Second):
			t.mu.Lock()
			delete(t.streams, id)
			t.mu.Unlock()
			return nil, fmt.Errorf("connect timeout: %s:%d", addr, port)
		}
	}

	return s, nil
}

// Close tears down the transport and all streams.
func (t *UDPTransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.done)

	t.mu.Lock()
	for _, s := range t.streams {
		s.mu.Lock()
		s.closeRecvChLocked()
		s.mu.Unlock()
	}
	t.streams = make(map[uint32]*udpStream)
	t.mu.Unlock()

	return t.conn.Close()
}

func (t *UDPTransport) Mode() string { return ModeUDP }

// sendPacket is a helper to encode and send a packet on the shared connection.
func (t *UDPTransport) sendPacket(pkt *pkgudp.Packet) error {
	data, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		return err
	}
	_, err = t.conn.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// udpStream
// ---------------------------------------------------------------------------

type udpStream struct {
	t      *UDPTransport
	id     uint32
	recvCh chan []byte

	mu         sync.Mutex
	buf        []byte // leftover bytes from previous Read
	seq        uint32
	closed     bool
	recvClosed bool // guards against double-close of recvCh
}

func (s *udpStream) ID() uint32 { return s.id }

// closeRecvCh closes recvCh exactly once; must be called with s.mu held or
// in a context where no concurrent close is possible.
func (s *udpStream) closeRecvChLocked() {
	if !s.recvClosed {
		s.recvClosed = true
		close(s.recvCh)
	}
}

// Read implements io.Reader. Blocks until data arrives or stream is closed.
func (s *udpStream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := copy(p, s.buf)
			s.buf = s.buf[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()

		data, ok := <-s.recvCh
		if !ok {
			return 0, fmt.Errorf("stream closed")
		}
		s.mu.Lock()
		s.buf = append(s.buf, data...)
		s.mu.Unlock()
	}
}

// Write implements io.Writer. Chunks data into 1344-byte segments.
func (s *udpStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, fmt.Errorf("write on closed stream")
	}
	s.mu.Unlock()

	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > udpMaxPayload {
			chunk = p[:udpMaxPayload]
		}

		s.mu.Lock()
		seq := s.seq
		s.seq++
		s.mu.Unlock()

		pkt := &pkgudp.Packet{
			ConnID:   s.t.connID,
			Type:     pkgudp.MsgStreamData,
			StreamID: s.id,
			Seq:      seq,
			Data:     chunk,
		}
		if err := s.t.sendPacket(pkt); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// Close sends MsgStreamClose and cleans up.
func (s *udpStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	// Remove from transport streams map
	s.t.mu.Lock()
	delete(s.t.streams, s.id)
	s.t.mu.Unlock()

	pkt := &pkgudp.Packet{
		ConnID:   s.t.connID,
		Type:     pkgudp.MsgStreamClose,
		StreamID: s.id,
	}
	return s.t.sendPacket(pkt)
}
