package transport

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The point of PRXNS-33: SkyNet drops new TCP connections by rate, so TLS mode
// must not open one per stream. 200 streams, TCP and UDP mixed, all held open
// at once, must arrive over at most four TCP connections (two base sessions,
// grown to four only under load).
func TestTLSMuxManyStreamsFewConnections(t *testing.T) {
	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	const total, workers = 200, 10
	streams := make([]Stream, total)
	errs := make(chan error, total)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < total; i += workers {
				typ := byte(0x01)
				if i%3 == 0 {
					typ = 0x02
				}
				s, err := tr.OpenStream(typ, "example.com", 443)
				if err != nil {
					errs <- fmt.Errorf("open #%d: %w", i, err)
					continue
				}
				streams[i] = s
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	for i, s := range streams {
		wg.Add(1)
		go func(i int, s Stream) {
			defer wg.Done()
			msg := fmt.Sprintf("stream-%d", i)
			var err error
			if i%3 == 0 {
				err = echoUDP(s, msg)
			} else {
				err = echoTCP(s, msg)
			}
			if err != nil {
				t.Errorf("stream %d: %v", i, err)
			}
		}(i, s)
	}
	wg.Wait()
	for _, s := range streams {
		s.Close()
	}

	if got := srv.accepted.Load(); got > 4 {
		t.Fatalf("%d streams took %d incoming TCP connections, want ≤ 4", total, got)
	}
}

// A pre-PRXNS-33 server closes the connection on MsgTypeMux. Connect must
// still succeed and every stream goes over its own TLS connection, as before.
func TestTLSMuxOldServerFallsBackToPerStream(t *testing.T) {
	srv := newFakeServer(t, true)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatalf("Connect to a pre-mux server: %v", err)
	}
	defer tr.Close()
	if !tr.perStream.Load() {
		t.Fatal("transport did not switch to per-stream mode")
	}
	afterConnect := srv.accepted.Load() // the mux probe + the classic probe

	for i := 0; i < 5; i++ {
		s, err := tr.OpenStream(0x01, "example.com", 443)
		if err != nil {
			t.Fatalf("tcp stream %d: %v", i, err)
		}
		if err := echoTCP(s, fmt.Sprintf("tcp-%d", i)); err != nil {
			t.Fatal(err)
		}
		s.Close()
		u, err := tr.OpenStream(0x02, "", 0)
		if err != nil {
			t.Fatalf("udp stream %d: %v", i, err)
		}
		if err := echoUDP(u, fmt.Sprintf("udp-%d", i)); err != nil {
			t.Fatal(err)
		}
		u.Close()
	}
	if got := srv.accepted.Load() - afterConnect; got != 10 {
		t.Fatalf("10 streams took %d connections, want one each", got)
	}
	if _, err := tr.OpenStream(0x01, refusedHost, 443); err == nil || !strings.Contains(err.Error(), "connect rejected") {
		t.Fatalf("refused target: err = %v, want connect rejected", err)
	}
	if tr.pooledSessions() != 0 {
		t.Fatal("per-stream transport holds mux sessions")
	}
}

// One session's connection drops: new streams go through the other one
// without an error, the pool redials, and the transport stays up (D1 must
// only fire on Close()).
func TestTLSMuxSessionDropKeepsTransport(t *testing.T) {
	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	waitFor(t, "two sessions", 5*time.Second, func() bool { return tr.liveSessions() == 2 })

	// A stream on the session that survives keeps working.
	srv.killMuxConn(0)
	for i := 0; i < 20; i++ {
		s, err := tr.OpenStream(0x01, "example.com", 443)
		if err != nil {
			t.Fatalf("stream %d after a session drop: %v", i, err)
		}
		if err := echoTCP(s, fmt.Sprintf("after-drop-%d", i)); err != nil {
			t.Fatalf("stream %d after a session drop: %v", i, err)
		}
		s.Close()
	}
	select {
	case <-tr.DoneChan():
		t.Fatal("a session drop closed the transport's DoneChan")
	default:
	}
	if !tr.Alive() {
		t.Fatal("a session drop made the transport not Alive")
	}
	waitFor(t, "the pool to redial", 5*time.Second, func() bool { return tr.liveSessions() == 2 })
	if got := srv.muxConnCount(); got != 3 {
		t.Fatalf("server saw %d mux sessions, want 3 (two + one redial)", got)
	}
}

// Close() stops new streams and fires DoneChan at once, but a stream opened
// before it keeps carrying data (PRXNS-32); its session closes after the last
// stream, and nothing the pool started is left running.
func TestTLSMuxCloseKeepsLiveStream(t *testing.T) {
	before := runtime.NumGoroutine()
	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "two sessions", 5*time.Second, func() bool { return tr.liveSessions() == 2 })
	live, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	if err := echoTCP(live, "before close"); err != nil {
		t.Fatal(err)
	}
	accepted := srv.accepted.Load()

	tr.Close()
	tr.Close() // the engine may close twice; sessions close once
	select {
	case <-tr.DoneChan():
	default:
		t.Fatal("DoneChan not closed by Close()")
	}
	waitFor(t, "the idle session to close", 5*time.Second, func() bool { return tr.liveSessions() == 1 })

	for i := 0; i < 3; i++ {
		if err := echoTCP(live, fmt.Sprintf("after close %d", i)); err != nil {
			t.Fatalf("live stream after Close(): %v", err)
		}
	}
	if _, err := tr.OpenStream(0x01, "example.com", 443); err == nil {
		t.Fatal("OpenStream after Close() succeeded")
	}
	if _, err := tr.openMuxStream(0x01, "example.com", 443); err == nil {
		t.Fatal("the pool opened a stream after Close()")
	}
	if got := srv.accepted.Load(); got != accepted {
		t.Fatalf("Close()d transport dialed %d new connections", got-accepted)
	}

	live.Close()
	waitFor(t, "the drained session to close", 5*time.Second, func() bool { return tr.pooledSessions() == 0 })
	srv.shutdown()
	waitFor(t, "goroutines to wind down", 5*time.Second, func() bool { return runtime.NumGoroutine() <= before })
}

// A TCP connect the server never answers fails that stream only, after the
// connect-result deadline, with the per-stream wording; the session lives on.
func TestTLSMuxSilentConnectFailsStreamOnly(t *testing.T) {
	old := tlsMuxConnectResultTimeout
	tlsMuxConnectResultTimeout = 200 * time.Millisecond
	defer func() { tlsMuxConnectResultTimeout = old }()

	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	waitFor(t, "two sessions", 5*time.Second, func() bool { return tr.liveSessions() == 2 })

	start := time.Now()
	_, err := tr.OpenStream(0x01, silentHost, 443)
	if err == nil || !strings.Contains(err.Error(), "connect rejected") {
		t.Fatalf("silent target: err = %v, want connect rejected", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("silent target took %v", d)
	}
	if _, err := tr.OpenStream(0x01, refusedHost, 443); err == nil || !strings.Contains(err.Error(), "connect rejected") {
		t.Fatalf("refused target: err = %v, want connect rejected", err)
	}
	if tr.liveSessions() != 2 || srv.muxConnCount() != 2 {
		t.Fatalf("a failed connect cost a session: live=%d server=%d", tr.liveSessions(), srv.muxConnCount())
	}
	s, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := echoTCP(s, "still fine"); err != nil {
		t.Fatal(err)
	}
}

// 50 goroutines open a stream while the pool has no live session: they all
// wait for one shared dial instead of dialing one each.
func TestTLSMuxConcurrentOpenOnEmptyPoolDialsOnce(t *testing.T) {
	setMuxLimits(t, 1, 4)
	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	srv.handshakeDelay.Store(int64(300 * time.Millisecond))
	srv.killMuxConn(0)
	waitFor(t, "the pool to empty", 5*time.Second, func() bool { return tr.liveSessions() == 0 })

	const n = 50
	var wg sync.WaitGroup
	streams := make([]Stream, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := tr.OpenStream(0x02, "", 0)
			if err == nil {
				err = echoUDP(s, fmt.Sprintf("q-%d", i))
			}
			streams[i], errs[i] = s, err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("stream %d: %v", i, err)
		}
		if streams[i] != nil {
			streams[i].Close()
		}
	}
	if got := srv.accepted.Load(); got != 2 {
		t.Fatalf("server accepted %d connections, want 2 (Connect + one shared redial)", got)
	}
}

// Dial errors reach the engine worded and wrapped as before: ENETUNREACH
// through IsNetworkUnreachable and "tcp dial:", "machine id rejected" as is —
// from Connect and from OpenStream on an empty pool.
func TestTLSMuxDialErrorsKeepTheirShape(t *testing.T) {
	setMuxLimits(t, 1, 4)
	var unreachable atomic.Bool
	oldDial := tlsDialTCP
	tlsDialTCP = func(network, address string) (net.Conn, error) {
		if unreachable.Load() {
			return nil, &net.OpError{Op: "dial", Net: network, Err: os.NewSyscallError("connect", syscall.ENETUNREACH)}
		}
		return net.Dial(network, address)
	}
	defer func() { tlsDialTCP = oldDial }()
	srv := newFakeServer(t, false)

	unreachable.Store(true)
	err := NewTLSTransport().Connect(srv.addr(), testKey, [16]byte{1})
	if err == nil || !IsNetworkUnreachable(err) || !strings.HasPrefix(err.Error(), "tcp dial: ") {
		t.Fatalf("Connect: err = %v, want tcp dial: … ENETUNREACH", err)
	}
	unreachable.Store(false)

	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	unreachable.Store(true)
	srv.killMuxConn(0)
	waitFor(t, "the pool to empty", 5*time.Second, func() bool { return tr.liveSessions() == 0 })
	_, err = tr.OpenStream(0x01, "example.com", 443)
	if err == nil || !IsNetworkUnreachable(err) || !strings.HasPrefix(err.Error(), "tcp dial: ") {
		t.Fatalf("OpenStream on an empty pool: err = %v, want tcp dial: … ENETUNREACH", err)
	}

	unreachable.Store(false)
	srv.rejectMID.Store(true)
	_, err = tr.OpenStream(0x02, "", 0)
	if err == nil || err.Error() != "machine id rejected" {
		t.Fatalf("OpenStream with the device rebound: err = %v, want machine id rejected", err)
	}
	select {
	case <-tr.DoneChan():
		t.Fatal("dial failures closed the transport")
	default:
	}

	srv.rejectMID.Store(false)
	s, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatalf("OpenStream once the server lets us in again: %v", err)
	}
	defer s.Close()
	if err := echoTCP(s, "back"); err != nil {
		t.Fatal(err)
	}
}

// yamux v0.1.2 closes the whole session when a keepalive ping goes unanswered
// for ConnectionWriteTimeout, or a stream's SYN stays unacked for
// StreamOpenTimeout. 10s would let a 10–20s stall (Wi-Fi roaming, LTE handover)
// kill every stream at once, where a TCP per stream used to outlive ~150s.
// Keep in pair with server/internal/proxy (TestMuxConfigTimeouts).
func TestMuxClientConfigTimeouts(t *testing.T) {
	cfg := muxClientConfig(&muxLogger{})
	if !cfg.EnableKeepAlive || cfg.KeepAliveInterval != 10*time.Second {
		t.Errorf("keepalive = %v every %s, want on, every 10s", cfg.EnableKeepAlive, cfg.KeepAliveInterval)
	}
	if cfg.ConnectionWriteTimeout != 30*time.Second {
		t.Errorf("ConnectionWriteTimeout = %s, want 30s", cfg.ConnectionWriteTimeout)
	}
	if cfg.StreamOpenTimeout != 30*time.Second {
		t.Errorf("StreamOpenTimeout = %s, want 30s", cfg.StreamOpenTimeout)
	}
	if cfg.MaxStreamWindowSize != 4<<20 {
		t.Errorf("MaxStreamWindowSize = %d, want 4 MiB", cfg.MaxStreamWindowSize)
	}
}
