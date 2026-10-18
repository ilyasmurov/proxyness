package tun

import (
	"errors"
	"net"
	"testing"
	"time"

	dstats "proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/transport"
)

// modeFakeTransport connects instantly (or fails for chosen servers) and
// reports a fixed Mode — enough to drive the D3 rebuild end to end.
type modeFakeTransport struct {
	mode string
}

func (m *modeFakeTransport) Connect(server, key string, machineID [16]byte) error { return nil }
func (m *modeFakeTransport) OpenStream(streamType byte, addr string, port uint16) (transport.Stream, error) {
	return nil, errors.New("modeFakeTransport: no streams")
}
func (m *modeFakeTransport) Mode() string              { return m.mode }
func (m *modeFakeTransport) DoneChan() <-chan struct{} { return nil }
func (m *modeFakeTransport) Close() error              { return nil }

// failingServerTransport fails Connect for one server address.
type failingServerTransport struct {
	modeFakeTransport
	failOn string
}

func (f *failingServerTransport) Connect(server, key string, machineID [16]byte) error {
	if server == f.failOn {
		return errors.New("tcp dial: i/o timeout")
	}
	return nil
}

// newD3Engine returns an Active engine on `mode` with one tracked live conn
// and the D3 counter already past its threshold.
func newD3Engine(mode string, factory func() transport.Transport) (*Engine, net.Conn, net.Conn) {
	e := NewEngine(dstats.NewRateMeter())
	e.mu.Lock()
	e.status = StatusActive
	e.serverAddr = "exit-a:443"
	e.transport = &modeFakeTransport{mode: mode}
	e.transportFactory = factory
	e.mu.Unlock()
	app, peer := net.Pipe()
	e.trackConn(app, "/Applications/Telegram.app")
	e.streamOpenFailures.Store(streamFailureThreshold)
	return e, app, peer
}

// connClosed tells a closed net.Pipe end from one that is merely quiet.
func connClosed(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	_, err := c.Read(make([]byte, 1))
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	return err != nil
}

// PRXNS-32: on TLS every live flow owns its own TCP+TLS socket to the exit,
// so "three new streams failed to dial" says nothing about them. Closing them
// on D3 turned every ISP wave of lost SYNs into "everything dropped".
func TestEngineD3KeepsLiveConnsOnTLS(t *testing.T) {
	e, app, peer := newD3Engine(transport.ModeTLS, func() transport.Transport {
		return &modeFakeTransport{mode: transport.ModeTLS}
	})
	defer app.Close()
	defer peer.Close()

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if got := e.GetStatus(); got != StatusActive {
		t.Fatalf("status after D3 rebuild = %s, want %s", got, StatusActive)
	}
	if connClosed(app) {
		t.Fatalf("D3 on a TLS transport must keep live connections open")
	}
}

// UDP multiplexes every flow over one session that dies with the transport,
// so its flows are closed right away, as before.
func TestEngineD3ClosesConnsOnUDP(t *testing.T) {
	e, app, peer := newD3Engine(transport.ModeUDP, func() transport.Transport {
		return &modeFakeTransport{mode: transport.ModeUDP}
	})
	defer app.Close()
	defer peer.Close()

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if !connClosed(app) {
		t.Fatalf("D3 on a UDP transport must close live connections")
	}
}

// A wave of lost SYNs is enough to move the ring with one dial timeout, so
// failing over to another exit says nothing about flows on the old one —
// they are kept too and die on their own if that exit is really gone.
func TestEngineD3KeepsConnsAcrossFailoverOnTLS(t *testing.T) {
	e, app, peer := newD3Engine(transport.ModeTLS, func() transport.Transport {
		return &failingServerTransport{modeFakeTransport: modeFakeTransport{mode: transport.ModeTLS}, failOn: "exit-a:443"}
	})
	defer app.Close()
	defer peer.Close()
	e.mu.Lock()
	e.servers = transport.NewServerRing("exit-a:443", "exit-b:443")
	e.mu.Unlock()

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if got := e.GetServerAddr(); got != "exit-b:443" {
		t.Fatalf("server after failover = %q, want exit-b:443", got)
	}
	if connClosed(app) {
		t.Fatalf("a failover alone must not close live TLS connections")
	}
}

// unreachOnceTransport fails the first Connect with ENETUNREACH (the local
// link changed under us), then succeeds.
type unreachOnceTransport struct {
	modeFakeTransport
	calls *int
}

func (u *unreachOnceTransport) Connect(server, key string, machineID [16]byte) error {
	*u.calls++
	if *u.calls == 1 {
		return errors.New("tcp dial: dial tcp 192.168.1.203:0->exit-a:443: connect: network is unreachable")
	}
	return nil
}

// ENETUNREACH during the rebuild means the local link went away (Wi-Fi
// switch, new DHCP address): kept flows may sit on a dead local IP, so they
// are swept once the transport is back.
func TestEngineD3ClosesKeptConnsAfterLinkChange(t *testing.T) {
	calls := 0
	e, app, peer := newD3Engine(transport.ModeTLS, func() transport.Transport {
		return &unreachOnceTransport{modeFakeTransport: modeFakeTransport{mode: transport.ModeTLS}, calls: &calls}
	})
	defer app.Close()
	defer peer.Close()

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if calls < 2 {
		t.Fatalf("expected a failed and a successful reconnect, got %d Connect calls", calls)
	}
	if !connClosed(app) {
		t.Fatalf("connections kept across a rebuild that hit ENETUNREACH must be closed")
	}
}

// The link may change and heal before the first rebuild attempt: then only
// the stream failures that tripped D3 carry the ENETUNREACH. Those count too.
func TestEngineD3ClosesKeptConnsAfterUnreachStreak(t *testing.T) {
	e, app, peer := newD3Engine(transport.ModeTLS, func() transport.Transport {
		return &modeFakeTransport{mode: transport.ModeTLS}
	})
	defer app.Close()
	defer peer.Close()
	e.streamOpenFailures.Store(0)
	unreach := errors.New("tcp dial: dial tcp 192.168.1.203:0->exit-a:443: connect: network is unreachable")
	for i := 0; i < streamFailureThreshold; i++ {
		e.noteStreamOpenFailure(unreach)
	}

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if !connClosed(app) {
		t.Fatalf("connections kept across a D3 tripped by ENETUNREACH streams must be closed")
	}
}

// The flag describes the current streak only: an old ENETUNREACH followed
// by a reset and a fresh timeout-only streak must not sweep.
func TestEngineD3UnreachStreakDoesNotLeak(t *testing.T) {
	e, app, peer := newD3Engine(transport.ModeTLS, func() transport.Transport {
		return &modeFakeTransport{mode: transport.ModeTLS}
	})
	defer app.Close()
	defer peer.Close()
	e.streamOpenFailures.Store(0)
	e.noteStreamOpenFailure(errors.New("connect: network is unreachable"))
	e.streamOpenFailures.Store(0) // a stream succeeded
	timeout := errors.New("tcp dial: dial tcp 192.168.1.212:0->exit-a:443: i/o timeout")
	for i := 0; i < streamFailureThreshold; i++ {
		e.noteStreamOpenFailure(timeout)
	}

	if exit := e.runD3(); exit {
		t.Fatalf("runD3 must recover the transport, not stop the health loop")
	}
	if connClosed(app) {
		t.Fatalf("a stale ENETUNREACH from an earlier streak must not sweep live connections")
	}
}
