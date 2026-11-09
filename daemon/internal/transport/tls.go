package transport

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"proxyness/pkg/proto"
)

// TLSTransport carries streams to the server over TLS on :443.
//
// Against a mux-capable server (PRXNS-33) streams run inside a small pool of
// long-lived TLS connections, each one a yamux session (see tls_mux.go): the
// home ISP drops new TCP connections by rate, so one TCP+TLS per stream —
// every DNS query included — was what kept tripping D3. Against a pre-mux
// server every OpenStream still dials its own TLS connection, as before.
type TLSTransport struct {
	server    string
	key       string
	machineID [16]byte
	connected atomic.Bool
	mu        sync.Mutex
	nextID    uint32

	// Snapshotted from the package vars in Connect, so goroutines a transport
	// leaves running never read a var a later test is changing.
	setupTimeout time.Duration
	dialTCP      func(network, address string) (net.Conn, error)
	limits       muxLimits

	// perStream is set once the server turned out to have no mux; it holds
	// for the rest of this transport's life (the engine builds a fresh
	// transport on every reconnect, and that one probes again).
	perStream atomic.Bool
	muxPool
	// done is closed on Close() so the engine's healthLoop D1 detector can
	// notice a dead TLS transport via `<-DoneChan()`. Before this existed,
	// `(*AutoTransport).DoneChan()` returned a nil channel when the active
	// transport was TLS, and a receive from nil blocks forever — so a TLS
	// transport that was Close()'d (e.g. by the engine's D3 force-close
	// path) never woke up the D1 branch, and the engine sat in Reconnecting
	// until D2 exhausted and the whole thing died. See tun/engine.go
	// healthLoop for the detector wiring.
	done      chan struct{}
	closeOnce sync.Once
}

func NewTLSTransport() *TLSTransport {
	return &TLSTransport{done: make(chan struct{})}
}

// tlsSetupTimeout bounds TLS handshake + auth + machine-id exchange on a
// fresh connection. A var so tests can shrink it.
var tlsSetupTimeout = 10 * time.Second

// tlsDialTCP opens the raw TCP socket under every TLS connection. A var so
// tests can inject dial failures (ENETUNREACH) without touching routes.
var tlsDialTCP = protectedDialUDP

func (t *TLSTransport) Connect(server, key string, machineID [16]byte) error {
	t.server = server
	t.key = key
	t.machineID = machineID
	t.setupTimeout = tlsSetupTimeout
	t.dialTCP = tlsDialTCP
	t.limits = currentMuxLimits()
	t.perStream.Store(false)

	// The first mux session doubles as the reachability probe and stays up.
	s, err := t.dialSession()
	if errors.Is(err, errNoMux) {
		t.switchToPerStream()
		// Pre-mux server: verify it with the probe this path always used,
		// so the engine never sees "no mux" as a failure.
		conn, err := t.dial()
		if err != nil {
			return err
		}
		conn.Close()
	} else if err != nil {
		return err
	}

	t.connected.Store(true)
	if s != nil {
		t.adoptSession(s)
	}
	return nil
}

// dial opens a ready-to-use per-stream connection: TLS, auth and machine-id
// done, deadline lifted.
func (t *TLSTransport) dial() (net.Conn, error) {
	conn, rawConn, err := t.handshake()
	if err != nil {
		return nil, err
	}
	rawConn.SetDeadline(time.Time{})
	return conn, nil
}

// handshake dials TCP+TLS and runs auth and machine-id. The setup deadline is
// still armed on return: the caller lifts it once it is done talking setup.
// The error texts ("tcp dial: …", "tls handshake: …", "auth rejected",
// "machine id rejected") are what the engine matches on.
func (t *TLSTransport) handshake() (*tls.Conn, net.Conn, error) {
	// Use the protected dialer for the underlying TCP socket so it gets
	// bound to the physical interface (Windows IP_UNICAST_IF / macOS
	// IP_BOUND_IF). Without this the daemon's own TLS-to-server traffic
	// follows the TUN default route once the engine is up — same feedback
	// loop as the UDP transport bug fixed in 1.28.15.
	rawConn, err := t.dialTCP("tcp", t.server)
	if err != nil {
		return nil, nil, fmt.Errorf("tcp dial: %w", err)
	}
	// The dialer only bounds the TCP connect. An ISP that lets the handshake
	// through and then drops every data packet (SkyNet → the Aeza subnet,
	// 2026-09-15) leaves Handshake()/ReadResult() waiting on the kernel's
	// retransmit budget — minutes — and a multi-server ring never moves on
	// to the next exit. Bound the whole setup, then lift the deadline for
	// the relay proper.
	rawConn.SetDeadline(time.Now().Add(t.setupTimeout))
	conn := tls.Client(rawConn, &tls.Config{InsecureSkipVerify: true, ServerName: ""})
	if err := conn.Handshake(); err != nil {
		rawConn.Close()
		return nil, nil, fmt.Errorf("tls handshake: %w", err)
	}

	if err := proto.WriteAuth(conn, t.key); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("auth: %w", err)
	}
	ok, err := proto.ReadResult(conn)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("auth: %w", err)
	}
	if !ok {
		conn.Close()
		return nil, nil, fmt.Errorf("auth rejected")
	}

	if err := proto.WriteMachineID(conn, t.machineID); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("machine id: %w", err)
	}
	ok, err = proto.ReadResult(conn)
	if err != nil || !ok {
		conn.Close()
		return nil, nil, fmt.Errorf("machine id rejected")
	}

	return conn, rawConn, nil
}

func (t *TLSTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if !t.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}
	if !t.perStream.Load() {
		s, err := t.openMuxStream(streamType, addr, port)
		if !errors.Is(err, errNoMux) {
			return s, err
		}
		// A redial found a pre-mux server: carry on per-stream right away.
	}
	return t.openPerStream(streamType, addr, port)
}

// openPerStream is the pre-mux path: one TLS connection per stream.
func (t *TLSTransport) openPerStream(streamType byte, addr string, port uint16) (Stream, error) {
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

// Close stops new streams at once and fires DoneChan, as before. Streams
// already open keep running (PRXNS-32: a transport rebuild must not cut live
// connections): per-stream ones own their TLS connection, mux sessions with
// streams drain and close after their last stream; idle sessions close now.
func (t *TLSTransport) Close() error {
	t.connected.Store(false)
	t.closeOnce.Do(func() { close(t.done) })
	t.closePool()
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
