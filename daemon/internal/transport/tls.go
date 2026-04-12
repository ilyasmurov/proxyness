package transport

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"proxyness/pkg/proto"
)

// TLSTransport wraps the existing per-connection TLS approach.
// Each OpenStream creates a new TLS connection (same as current behavior).
type TLSTransport struct {
	server    string
	key       string
	machineID [16]byte
	connected atomic.Bool
	mu        sync.Mutex
	nextID    uint32
	// done is closed on Close() so the engine's healthLoop D1 detector can
	// notice a dead TLS transport via `<-DoneChan()`. Before this existed,
	// `(*AutoTransport).DoneChan()` returned a nil channel when the active
	// transport was TLS, and a receive from nil blocks forever — so a TLS
	// transport that was Close()'d (e.g. by the engine's D3 force-close
	// path) never woke up the D1 branch, and the engine sat in Reconnecting
	// until D2 exhausted and the whole thing died. See tun/engine.go
	// healthLoop for the detector wiring.
	done     chan struct{}
	closeOnce sync.Once
}

func NewTLSTransport() *TLSTransport {
	return &TLSTransport{done: make(chan struct{})}
}

func (t *TLSTransport) Connect(server, key string, machineID [16]byte) error {
	t.server = server
	t.key = key
	t.machineID = machineID

	// Verify server is reachable with a test auth
	conn, err := t.dial()
	if err != nil {
		return err
	}
	conn.Close()

	t.connected.Store(true)
	return nil
}

func (t *TLSTransport) dial() (net.Conn, error) {
	// Use the protected dialer for the underlying TCP socket so it gets
	// bound to the physical interface (Windows IP_UNICAST_IF / macOS
	// IP_BOUND_IF). Without this the daemon's own TLS-to-server traffic
	// follows the TUN default route once the engine is up — same feedback
	// loop as the UDP transport bug fixed in 1.28.15.
	rawConn, err := protectedDialUDP("tcp", t.server)
	if err != nil {
		return nil, fmt.Errorf("tcp dial: %w", err)
	}
	conn := tls.Client(rawConn, &tls.Config{InsecureSkipVerify: true, ServerName: ""})
	if err := conn.Handshake(); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}

	if err := proto.WriteAuth(conn, t.key); err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}
	ok, err := proto.ReadResult(conn)
	if err != nil || !ok {
		conn.Close()
		return nil, fmt.Errorf("auth rejected")
	}

	if err := proto.WriteMachineID(conn, t.machineID); err != nil {
		conn.Close()
		return nil, fmt.Errorf("machine id: %w", err)
	}
	ok, err = proto.ReadResult(conn)
	if err != nil || !ok {
		conn.Close()
		return nil, fmt.Errorf("machine id rejected")
	}

	return conn, nil
}

func (t *TLSTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if !t.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}

	conn, err := t.dial()
	if err != nil {
		return nil, err
	}

	msgType := byte(proto.MsgTypeTCP)
	if streamType == 0x02 {
		msgType = proto.MsgTypeUDP
	}

	if err := proto.WriteMsgType(conn, msgType); err != nil {
		conn.Close()
		return nil, err
	}

	// For TCP, send connect and read result
	if streamType == 0x01 {
		if err := proto.WriteConnect(conn, addr, port); err != nil {
			conn.Close()
			return nil, err
		}
		ok, err := proto.ReadResult(conn)
		if err != nil || !ok {
			conn.Close()
			return nil, fmt.Errorf("connect rejected: %s:%d", addr, port)
		}
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	return &tlsStream{conn: conn, id: id}, nil
}

func (t *TLSTransport) Close() error {
	t.connected.Store(false)
	t.closeOnce.Do(func() { close(t.done) })
	return nil
}

func (t *TLSTransport) Mode() string { return ModeTLS }

// DoneChan returns a channel closed when the transport has been Close()'d.
// Pairs with the engine's D1 detector in tun/engine.go — must match the
// shape of UDPTransport.DoneChan so AutoTransport.DoneChan can delegate to
// whichever transport is currently active.
func (t *TLSTransport) DoneChan() <-chan struct{} { return t.done }

// Alive reports whether the transport is usable. Mirrors UDPTransport.Alive
// so the engine's D2 detector can uniformly treat TLS and UDP.
func (t *TLSTransport) Alive() bool { return t.connected.Load() }

type tlsStream struct {
	conn net.Conn
	id   uint32
}

func (s *tlsStream) Read(p []byte) (int, error)  { return s.conn.Read(p) }
func (s *tlsStream) Write(p []byte) (int, error) { return s.conn.Write(p) }
func (s *tlsStream) Close() error                { return s.conn.Close() }
func (s *tlsStream) ID() uint32                  { return s.id }
