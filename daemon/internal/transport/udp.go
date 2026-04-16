package transport

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pkgudp "proxyness/pkg/udp"
)

const (
	udpMaxPayload  = 1340 // max data bytes per MsgStreamData packet
	udpKeepalive   = 3 * time.Second
	udpHandshakeTO = 3 * time.Second
	udpReadBuf     = 65535

	udpStreamOpenRetries = 3
	udpStreamOpenTimeout = 1 * time.Second
)

// UDPTransport implements Transport over a single multiplexed UDP connection.
type UDPTransport struct {
	conn       *net.UDPConn
	sessionKey []byte
	connID     uint32
	devKey     []byte // 32-byte device key derived from hex string

	mu      sync.Mutex
	streams map[uint32]*udpStream
	nextID  uint32

	closed   atomic.Bool
	done     chan struct{}
	lastRecv atomic.Int64 // unix nano of last received packet
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

	// Use the protected dialer so the UDP socket is bound to the physical
	// interface (Windows IP_UNICAST_IF / macOS IP_BOUND_IF). Without this,
	// when the TUN engine is active our outbound proxy datagrams hit the
	// kernel routing table and follow the default route — which is the TUN
	// device. The daemon ends up reading its own packets out of bridgeInbound,
	// the self-detection branch reflects them through NAT bypass, and CPU
	// spikes to ~100% on a feedback loop.
	rawConn, err := protectedDialUDP("udp", server)
	if err != nil {
		return fmt.Errorf("udp dial: %w", err)
	}
	conn, ok := rawConn.(*net.UDPConn)
	if !ok {
		rawConn.Close()
		return fmt.Errorf("udp dial: expected *net.UDPConn, got %T", rawConn)
	}
	conn.SetReadBuffer(4 * 1024 * 1024)
	conn.SetWriteBuffer(4 * 1024 * 1024)
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

	t.lastRecv.Store(time.Now().UnixNano())
	go t.recvLoop()
	go t.keepaliveLoop()

	return nil
}

// recvLoop reads incoming UDP packets and delivers them directly to streams.
func (t *UDPTransport) recvLoop() {
	buf := make([]byte, udpReadBuf)
	for {
		n, err := t.conn.Read(buf)
		if err != nil {
			if !t.closed.Load() {
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
		t.lastRecv.Store(time.Now().UnixNano())

		switch pkt.Type {
		case pkgudp.MsgSessionClose:
			// Server is shutting down gracefully — close the transport now so
			// engine.healthLoop picks up the done signal and reconnects
			// immediately, instead of waiting 20s for the keepalive dead-ticker.
			log.Printf("udp: received session close from server, closing transport")
			go t.Close()
			return
		case pkgudp.MsgStreamData:
			t.mu.Lock()
			s := t.streams[pkt.StreamID]
			t.mu.Unlock()
			if s == nil {
				continue
			}
			select {
			case s.recvCh <- pkt.Data:
			default:
				// Drop — consumer slow, upper TCP will retransmit
			}
		case pkgudp.MsgStreamClose:
			t.mu.Lock()
			s, ok := t.streams[pkt.StreamID]
			t.mu.Unlock()
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

const udpDeadTimeout = 20 * time.Second

// keepaliveLoop sends MsgKeepalive packets every 15s to prevent NAT timeout.
// Also detects dead sessions: if no packet received for 5s, closes the transport.
func (t *UDPTransport) keepaliveLoop() {
	keepaliveTicker := time.NewTicker(udpKeepalive)
	defer keepaliveTicker.Stop()
	deadTicker := time.NewTicker(1 * time.Second)
	defer deadTicker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-deadTicker.C:
			last := time.Unix(0, t.lastRecv.Load())
			if time.Since(last) > udpDeadTimeout {
				log.Printf("udp: no packets received for %s, closing dead session", udpDeadTimeout)
				t.Close()
				return
			}
		case <-keepaliveTicker.C:
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
// For TCP streams, waits for a connect result with up to 3 retries × 1s timeout.
// For UDP streams, returns immediately after sending MsgStreamOpen.
func (t *UDPTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("transport closed")
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	// 4096 × ~1340 bytes ≈ 5.4 MB of per-stream ingress buffer. At observed
	// wire rates of 5+ MB/s on saturated downloads the previous 1024-entry
	// buffer (~1.3 MB) filled within ~250ms of consumer stall, after which
	// the drop-on-full kicked in and user-visible goodput capped at the drain
	// rate rather than the wire rate. A deeper queue absorbs bursts without
	// dropping; per-stream memory is bounded by transport MaxStreams (256) ×
	// 5.4 MB = 1.4 GB worst case, but in practice only active download
	// streams hold the full buffer.
	recvCh := make(chan []byte, 4096)
	s := &udpStream{
		t:      t,
		id:     id,
		recvCh: recvCh,
		done:   make(chan struct{}),
	}
	t.streams[id] = s
	t.mu.Unlock()

	// Send MsgStreamOpen
	payload := (&pkgudp.StreamOpenMsg{
		StreamType: streamType,
		Addr:       addr,
		Port:       port,
	}).Encode()

	if err := t.sendPacketDirect(pkgudp.MsgStreamOpen, id, payload); err != nil {
		t.mu.Lock()
		delete(t.streams, id)
		t.mu.Unlock()
		return nil, fmt.Errorf("send stream open: %w", err)
	}

	// For TCP streams wait for a single-byte connect result.
	// Retry up to udpStreamOpenRetries times with udpStreamOpenTimeout each.
	if streamType == pkgudp.StreamTypeTCP {
		var lastErr error
		for attempt := 0; attempt < udpStreamOpenRetries; attempt++ {
			select {
			case data := <-recvCh:
				if len(data) == 0 || data[0] != 0x01 {
					lastErr = fmt.Errorf("connect rejected: %s:%d", addr, port)
					// No point retrying a server-side rejection
					t.mu.Lock()
					delete(t.streams, id)
					t.mu.Unlock()
					return nil, lastErr
				}
				return s, nil
			case <-s.done:
				t.mu.Lock()
				delete(t.streams, id)
				t.mu.Unlock()
				return nil, fmt.Errorf("stream closed before connect result")
			case <-time.After(udpStreamOpenTimeout):
				lastErr = fmt.Errorf("connect timeout: %s:%d", addr, port)
				// Retry: resend MsgStreamOpen
				if attempt < udpStreamOpenRetries-1 {
					_ = t.sendPacketDirect(pkgudp.MsgStreamOpen, id, payload)
				}
			}
		}
		t.mu.Lock()
		delete(t.streams, id)
		t.mu.Unlock()
		return nil, lastErr
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

func (t *UDPTransport) Mode() string            { return ModeUDP }
func (t *UDPTransport) Alive() bool             { return !t.closed.Load() }
func (t *UDPTransport) DoneChan() <-chan struct{} { return t.done }

// sendPacketDirect encodes and sends a packet directly on the shared connection.
func (t *UDPTransport) sendPacketDirect(typ byte, streamID uint32, data []byte) error {
	pkt := &pkgudp.Packet{
		ConnID:   t.connID,
		Type:     typ,
		StreamID: streamID,
		Data:     data,
	}
	raw, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		return err
	}
	_, err = t.conn.Write(raw)
	return err
}

// ---------------------------------------------------------------------------
// udpStream
// ---------------------------------------------------------------------------

type udpStream struct {
	t      *UDPTransport
	id     uint32
	recvCh chan []byte
	// done is closed exactly once when the stream is being torn down. It
	// lets Read() exit cleanly. We never close recvCh itself — closing a
	// channel while another goroutine is mid-send panics the process, which
	// is what happened before 1.36.2 whenever a stream teardown raced with
	// an in-flight delivery.
	done chan struct{}

	mu         sync.Mutex
	buf        []byte // leftover bytes from previous Read
	closed     bool
	recvClosed bool // guards against double-close of done
}

func (s *udpStream) ID() uint32 { return s.id }

// closeRecvChLocked signals the stream is torn down. Closes s.done exactly
// once; recvCh itself is left unclosed because closing it would race with
// any in-flight delivery trying to send a payload. The name is retained from
// the pre-1.36.2 API for call-site compatibility.
func (s *udpStream) closeRecvChLocked() {
	if !s.recvClosed {
		s.recvClosed = true
		close(s.done)
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

		select {
		case data := <-s.recvCh:
			s.mu.Lock()
			s.buf = append(s.buf, data...)
			s.mu.Unlock()
		case <-s.done:
			// Drain anything already queued before giving up, so a fast
			// close on the delivery side doesn't lose tail bytes that
			// made it into recvCh before done fired.
			select {
			case data := <-s.recvCh:
				s.mu.Lock()
				s.buf = append(s.buf, data...)
				s.mu.Unlock()
			default:
				return 0, fmt.Errorf("stream closed")
			}
		}
	}
}

// Write implements io.Writer. Chunks data into 1340-byte segments and sends
// each directly: encrypt → send → forget. TCP reliability is handled by
// gVisor inside the tunnel.
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
		if err := s.t.sendPacketDirect(pkgudp.MsgStreamData, s.id, chunk); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// Close sends MsgStreamClose and cleans up. Also closes s.done so any
// goroutine blocked in Read() unblocks.
func (s *udpStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.closeRecvChLocked()
	s.mu.Unlock()

	// Remove from transport streams map
	s.t.mu.Lock()
	delete(s.t.streams, s.id)
	s.t.mu.Unlock()

	return s.t.sendPacketDirect(pkgudp.MsgStreamClose, s.id, nil)
}
