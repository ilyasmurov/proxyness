package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
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

// newBareTransport is a TLSTransport set up the way Connect does, minus the
// first dial and the pool.
func newBareTransport(addr string) *TLSTransport {
	tr := NewTLSTransport()
	tr.server = addr
	tr.key = testKey
	tr.machineID = [16]byte{1}
	tr.setupTimeout = tlsSetupTimeout
	tr.dialTCP = tlsDialTCP
	tr.limits = currentMuxLimits()
	return tr
}

// captureLog collects the package's log output for one test.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLog(t *testing.T) *logBuf {
	t.Helper()
	lb := &logBuf{}
	log.SetOutput(lb)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return lb
}

// "No mux" means the server closed the connection on MsgTypeMux (or said no).
// What tls.Conn reports for a close with and without close_notify is EOF or
// ErrUnexpectedEOF, and only those. A reset is a network failure and must
// keep its own error — otherwise one RST from a middlebox would send the
// transport into per-stream mode.
func TestTLSMuxOnlyEOFMeansNoMux(t *testing.T) {
	cases := []struct {
		name    string
		style   oldStyle
		noMux   bool // dialSession must report errNoMux
		readEOF bool // the raw read after 0x04 must be EOF / ErrUnexpectedEOF
	}{
		{"close_notify then FIN (real Handle)", oldCloseNotify, true, true},
		{"FIN without close_notify", oldCloseNoNotify, true, true},
		{"server answers false", oldReplyFalse, true, false},
		{"RST", oldReset, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newFakeServer(t, true)
			srv.oldStyle.Store(int32(c.style))

			// What tls.Conn hands the client after the 0x04.
			tr := newBareTransport(srv.addr())
			conn, _, err := tr.handshake()
			if err != nil {
				t.Fatal(err)
			}
			if err := proto.WriteMsgType(conn, proto.MsgTypeMux); err != nil {
				t.Fatal(err)
			}
			_, rerr := proto.ReadResult(conn)
			conn.Close()
			t.Logf("ReadResult after 0x04: %v", rerr)
			if isEOF := errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF); c.readEOF && !isEOF {
				t.Errorf("raw read error = %v, want EOF or ErrUnexpectedEOF", rerr)
			} else if c.style == oldReset && (isEOF || rerr == nil) {
				t.Errorf("raw read error after RST = %v, want a network error", rerr)
			}

			// What dialSession makes of it.
			s, err := newBareTransport(srv.addr()).dialSession()
			if s != nil {
				s.sess.Close()
			}
			if c.noMux {
				if !errors.Is(err, errNoMux) {
					t.Fatalf("dialSession err = %v, want errNoMux", err)
				}
				return
			}
			if err == nil || errors.Is(err, errNoMux) {
				t.Fatalf("dialSession err = %v, want a network error, not errNoMux", err)
			}
			if !strings.HasPrefix(err.Error(), "mux: ") {
				t.Errorf("dialSession err = %q, want the \"mux: \" prefix", err)
			}
		})
	}
}

// A close without close_notify is still a pre-mux server: Connect falls back
// to one connection per stream, same as for the clean close.
func TestTLSMuxOldServerWithoutCloseNotifyFallsBack(t *testing.T) {
	srv := newFakeServer(t, true)
	srv.oldStyle.Store(int32(oldCloseNoNotify))
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer tr.Close()
	if !tr.perStream.Load() {
		t.Fatal("transport did not switch to per-stream mode")
	}
	s, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := echoTCP(s, "hello"); err != nil {
		t.Fatal(err)
	}
}

// A server that resets the connection after 0x04 is a failed dial, not an old
// server: Connect reports it, the transport stays in mux mode and does not
// quietly dial per-stream.
func TestTLSMuxResetOnMuxRequestIsDialFailure(t *testing.T) {
	srv := newFakeServer(t, true)
	srv.oldStyle.Store(int32(oldReset))
	tr := NewTLSTransport()
	err := tr.Connect(srv.addr(), testKey, [16]byte{1})
	if err == nil {
		tr.Close()
		t.Fatal("Connect succeeded against a server that resets on 0x04")
	}
	if errors.Is(err, errNoMux) || !strings.HasPrefix(err.Error(), "mux: ") {
		t.Errorf("Connect err = %q, want a \"mux: …\" network error", err)
	}
	if tr.perStream.Load() {
		t.Error("transport switched to per-stream mode on a reset")
	}
	if got := srv.accepted.Load(); got != 1 {
		t.Errorf("server accepted %d connections, want 1 (no per-stream retry)", got)
	}
}

// With a live session the pool has proof that the server speaks mux, so a
// background dial that meets EOF on 0x04 (a deploy restarting the server, a
// middlebox) is a failed dial with the usual pause, never a switch to
// per-stream for the rest of the transport's life.
func TestTLSMuxNoMuxAnswerWithLiveSessionIsDialFailure(t *testing.T) {
	logs := captureLog(t)
	srv := newFakeServer(t, false)
	srv.muxFirst.Store(1) // session #1 is served, every later 0x04 gets EOF
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	// The background dial for the second session hits the EOF.
	waitFor(t, "the background dial to be refused", 5*time.Second, func() bool {
		if srv.accepted.Load() < 2 {
			return false
		}
		tr.poolMu.Lock()
		defer tr.poolMu.Unlock()
		return tr.inflight == nil && (tr.backoff > 0 || tr.perStream.Load())
	})
	if tr.perStream.Load() {
		t.Fatal("background dial switched the transport to per-stream while a session was alive")
	}
	if got := tr.liveSessions(); got != 1 {
		t.Fatalf("live sessions = %d, want 1", got)
	}
	// The line is written just after the pool state is updated.
	waitFor(t, "the dial failure line in the log", 5*time.Second, func() bool {
		out := logs.String()
		return strings.Contains(out, "[tls] mux dial failed") && strings.Contains(out, "(server said no mux while 1 sessions alive)")
	})

	// Streams keep riding the mux session.
	s, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := echoTCP(s, "still muxed"); err != nil {
		t.Fatal(err)
	}
}

// …but when no session is alive (the server was rolled back to a pre-mux
// build while the daemon ran), the same EOF does mean "old server".
func TestTLSMuxNoMuxAnswerWithoutLiveSessionSwitchesToPerStream(t *testing.T) {
	setMuxLimits(t, 1, 1)
	srv := newFakeServer(t, false)
	srv.muxFirst.Store(1)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	srv.killMuxConn(0)
	waitFor(t, "the pool to empty", 5*time.Second, func() bool { return tr.liveSessions() == 0 })

	// The next stream needs a dial; the server now has no mux.
	s, err := tr.OpenStream(0x01, "example.com", 443)
	if err != nil {
		t.Fatalf("OpenStream after the server lost its mux: %v", err)
	}
	defer s.Close()
	if !tr.perStream.Load() {
		t.Fatal("transport stayed in mux mode with no live session and no mux on the server")
	}
	if err := echoTCP(s, "per-stream now"); err != nil {
		t.Fatal(err)
	}
}

// A server that accepts 0x04 and then drops the session (a crash loop) costs
// one OpenStream at most two dials of its own. Background dials may add one
// more while it waits; before the cap a single call made up to ten.
func TestTLSMuxOpenStreamDialsAtMostTwice(t *testing.T) {
	for _, mode := range []string{"drops the session at once", "drops the session on its first stream"} {
		t.Run(mode, func(t *testing.T) {
			setMuxLimits(t, 1, 8) // up to 9 attempts per call before the cap
			srv := newFakeServer(t, false)
			if strings.HasSuffix(mode, "once") {
				srv.dropMux.Store(true)
			} else {
				srv.dropOnStream.Store(true)
			}
			tr := NewTLSTransport()
			if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
				t.Fatal(err)
			}
			defer tr.Close()
			// Slow dials: every dial the call waits for is countable, and the
			// background redialer cannot run ahead of it.
			srv.handshakeDelay.Store(int64(80 * time.Millisecond))

			before := srv.accepted.Load()
			start := time.Now()
			s, err := tr.OpenStream(0x01, "example.com", 443)
			took := time.Since(start)
			dials := srv.accepted.Load() - before
			if err == nil {
				s.Close()
				t.Fatal("OpenStream succeeded against a server that drops every session")
			}
			if errors.Is(err, errNoMux) {
				t.Errorf("err = %v, a crash-looping mux server is not a pre-mux server", err)
			}
			t.Logf("OpenStream: %v after %s, %d connections", err, took.Round(time.Millisecond), dials)
			// 2 dials of the call + at most 1 background dial started meanwhile.
			if dials > 3 {
				t.Errorf("one OpenStream caused %d connections, want at most 3 (2 own + 1 background)", dials)
			}
		})
	}
}

// A server (or a middlebox) that takes a mux session and drops it moments
// later must not make the pool redial in a loop: every such dial "succeeds",
// but the session is a failure for the pause, so the pause grows and the
// line sees a handful of new TCP connections, not dozens a second — the rate
// SkyNet cuts the whole line for (PRXNS-33).
func TestTLSMuxShortLivedSessionsBackOff(t *testing.T) {
	oldMin, oldMax := tlsMuxBackoffMin, tlsMuxBackoffMax
	tlsMuxBackoffMin, tlsMuxBackoffMax = 200*time.Millisecond, 30*time.Second
	t.Cleanup(func() { tlsMuxBackoffMin, tlsMuxBackoffMax = oldMin, oldMax })

	srv := newFakeServer(t, false)
	srv.dropMuxAfter.Store(int64(50 * time.Millisecond))
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	time.Sleep(2 * time.Second) // no streams: only the background dialer runs
	got := srv.accepted.Load()
	t.Logf("incoming connections in 2s: %d", got)
	if got > 6 {
		t.Fatalf("a server that drops every session after 50ms took %d incoming connections in 2s, want ≤ 6", got)
	}
}

// setShortLived changes muxShortLived for one test; Connect snapshots it.
func setShortLived(t *testing.T, d time.Duration) {
	t.Helper()
	old := muxShortLived
	muxShortLived = d
	t.Cleanup(func() { muxShortLived = old })
}

// A session that outlived muxShortLived proves the server keeps sessions: when
// it dies the next dial goes out at once and the pause is back to zero, even
// with a long pause configured.
func TestTLSMuxLongLivedSessionDeathRedialsWithoutPause(t *testing.T) {
	setMuxLimits(t, 1, 1)
	setShortLived(t, 300*time.Millisecond)
	oldMin, oldMax := tlsMuxBackoffMin, tlsMuxBackoffMax
	tlsMuxBackoffMin, tlsMuxBackoffMax = 30*time.Second, 30*time.Second
	t.Cleanup(func() { tlsMuxBackoffMin, tlsMuxBackoffMax = oldMin, oldMax })

	srv := newFakeServer(t, false)
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	time.Sleep(450 * time.Millisecond) // older than muxShortLived
	srv.killMuxConn(0)
	// A short-lived death would put the next try 30s away.
	waitFor(t, "a redial right after the long-lived session died", 3*time.Second, func() bool {
		return srv.muxConnCount() == 2 && tr.liveSessions() == 1
	})
	tr.poolMu.Lock()
	backoff, retryAt := tr.backoff, tr.retryAt
	tr.poolMu.Unlock()
	if backoff != 0 || !retryAt.IsZero() {
		t.Fatalf("after a long-lived session: backoff = %s, retryAt = %v, want no pause", backoff, retryAt)
	}
}

// The chain "short session → dial → short session" grows the pause min → ×2 →
// max, the log says so, a successful dial alone does not reset it, and a
// session that outlives muxShortLived does.
func TestTLSMuxShortLivedChainGrowsPauseUntilASessionSticks(t *testing.T) {
	logs := captureLog(t)
	setMuxLimits(t, 1, 1)
	setShortLived(t, 400*time.Millisecond)
	oldMin, oldMax := tlsMuxBackoffMin, tlsMuxBackoffMax
	tlsMuxBackoffMin, tlsMuxBackoffMax = 50*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { tlsMuxBackoffMin, tlsMuxBackoffMax = oldMin, oldMax })

	srv := newFakeServer(t, false)
	srv.dropMuxAfter.Store(int64(20 * time.Millisecond))
	tr := NewTLSTransport()
	if err := tr.Connect(srv.addr(), testKey, [16]byte{1}); err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	backoff := func() time.Duration {
		tr.poolMu.Lock()
		defer tr.poolMu.Unlock()
		return tr.backoff
	}
	// The log line comes just after the pool state is updated.
	waitFor(t, "the pause to reach its maximum", 5*time.Second, func() bool {
		return backoff() == 200*time.Millisecond && strings.Contains(logs.String(), "(short-lived, next background try in 200ms)")
	})
	out := logs.String()
	for _, want := range []string{"(short-lived, next background try in 50ms)", "(short-lived, next background try in 100ms)", "(short-lived, next background try in 200ms)"} {
		if !strings.Contains(out, want) {
			t.Errorf("log has no %q:\n%s", want, out)
		}
	}

	// The server turns healthy: the next dial sticks. Landing a session does
	// not end the pause by itself…
	srv.dropMuxAfter.Store(0)
	// Sessions the fake server drops never reach muxConns, so a count of 1
	// with one live session is the lasting one.
	waitFor(t, "a lasting session", 5*time.Second, func() bool { return srv.muxConnCount() == 1 && tr.liveSessions() == 1 })
	if got := backoff(); got != 200*time.Millisecond {
		t.Fatalf("pause right after a good dial = %s, want it kept at 200ms", got)
	}
	// …living past muxShortLived and then dying does.
	time.Sleep(600 * time.Millisecond)
	srv.killMuxConn(0)
	waitFor(t, "the pause to end", 3*time.Second, func() bool { return backoff() == 0 })
	waitFor(t, "a redial", 3*time.Second, func() bool { return tr.liveSessions() == 1 })
}

// A live session older than muxShortLived ends a stale pause the next time it
// would grow: the failed dial then starts again from backoffMin.
func TestTLSMuxSettleBackoffByLiveLongSession(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	ys, err := yamuxClientForTest(c1)
	if err != nil {
		t.Fatal(err)
	}
	defer ys.Close()

	tr := newBareTransport("127.0.0.1:1")
	tr.limits.shortLived = time.Second
	now := time.Now()
	tr.sessions = []*muxSession{{sess: ys, started: now.Add(-5 * time.Second)}}

	tr.backoff = 8 * time.Second
	tr.growBackoffLocked(now) // no settle: plain growth
	if tr.backoff != 16*time.Second {
		t.Fatalf("plain growth: backoff = %s, want 16s", tr.backoff)
	}
	tr.settleBackoffLocked(now)
	if tr.backoff != 0 {
		t.Fatalf("a live session older than muxShortLived left backoff = %s", tr.backoff)
	}
	tr.sessions[0].started = now // too young to prove anything
	tr.backoff = 8 * time.Second
	tr.settleBackoffLocked(now)
	if tr.backoff != 8*time.Second {
		t.Fatalf("a young session reset backoff to %s", tr.backoff)
	}
}

func yamuxClientForTest(c net.Conn) (*yamux.Session, error) {
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = false
	cfg.LogOutput = io.Discard
	return yamux.Client(c, cfg)
}
