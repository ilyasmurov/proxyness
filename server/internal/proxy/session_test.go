package proxy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

// fakeDispatch stands in for Handler.dispatchStream (which needs a database):
// TCP streams are connected to the real target and relayed, UDP frames are
// forwarded to the real UDP target and the replies framed back.
type fakeDispatch struct {
	tcp, udp, other atomic.Int64
	remotes         sync.Map // stream RemoteAddr().String() → true
}

func (d *fakeDispatch) dispatch(st net.Conn, msgType byte) {
	d.remotes.Store(st.RemoteAddr().String(), true)
	switch msgType {
	case proto.MsgTypeTCP:
		d.tcp.Add(1)
		host, port, err := proto.ReadConnect(st)
		if err != nil {
			return
		}
		target, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(int(port))), 5*time.Second)
		if err != nil {
			proto.WriteResult(st, false)
			return
		}
		defer target.Close()
		proto.WriteResult(st, true)
		proto.Relay(st, target)
	case proto.MsgTypeUDP:
		d.udp.Add(1)
		for {
			host, port, payload, err := proto.ReadUDPFrame(st)
			if err != nil {
				return
			}
			uc, err := net.Dial("udp", net.JoinHostPort(host, strconv.Itoa(int(port))))
			if err != nil {
				return
			}
			uc.Write(payload)
			uc.SetReadDeadline(time.Now().Add(5 * time.Second))
			buf := make([]byte, 2048)
			n, err := uc.Read(buf)
			uc.Close()
			if err != nil {
				return
			}
			if err := proto.WriteUDPFrame(st, host, port, buf[:n]); err != nil {
				return
			}
		}
	default:
		d.other.Add(1)
	}
}

// startTargets runs a TCP and a UDP server that answer "<proto>:<input>".
func startTargets(t *testing.T) (*net.TCPAddr, *net.UDPAddr) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if err != nil {
						return
					}
					c.Write([]byte("tcp:" + string(buf[:n])))
				}
			}()
		}
	}()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pc.WriteTo([]byte("udp:"+string(buf[:n])), from)
		}
	}()
	return ln.Addr().(*net.TCPAddr), pc.LocalAddr().(*net.UDPAddr)
}

type serveResult struct {
	streams int
	err     error
}

// muxPair connects a yamux client session to serveMux over loopback TCP and
// returns the client session, its conn and a channel with serveMux's result.
func muxPair(t *testing.T, d *fakeDispatch) (*yamux.Session, net.Conn, chan serveResult) {
	t.Helper()
	return muxPairRecheck(t, d, nil)
}

// muxPairRecheck is muxPair with a device recheck wired into serveMux.
func muxPairRecheck(t *testing.T, d *fakeDispatch, recheck func() bool) (*yamux.Session, net.Conn, chan serveResult) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cc, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	sc, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	res := make(chan serveResult, 1)
	go func() {
		defer sc.Close()
		n, err := serveMux(sc, d.dispatch, recheck)
		res <- serveResult{n, err}
	}()
	sess, err := yamux.Client(cc, muxConfig())
	if err != nil {
		t.Fatal(err)
	}
	return sess, cc, res
}

// A 0x04 session carries TCP and UDP streams side by side, each one reaching
// its real target, and every stream reports the client's address (stats
// Tracker.AddRemote uses it).
func TestServeMuxCarriesTCPAndUDPStreams(t *testing.T) {
	tcpTarget, udpTarget := startTargets(t)
	d := &fakeDispatch{}
	sess, cc, res := muxPair(t, d)

	const perType = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*perType)
	for i := 0; i < perType; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			st, err := sess.OpenStream()
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			proto.WriteMsgType(st, proto.MsgTypeTCP)
			proto.WriteConnect(st, "127.0.0.1", uint16(tcpTarget.Port))
			if ok, err := proto.ReadResult(st); err != nil || !ok {
				errs <- fmt.Errorf("tcp %d: connect result ok=%v err=%v", i, ok, err)
				return
			}
			msg := fmt.Sprintf("hello-%d", i)
			st.Write([]byte(msg))
			buf := make([]byte, len("tcp:")+len(msg))
			if _, err := io.ReadFull(st, buf); err != nil || string(buf) != "tcp:"+msg {
				errs <- fmt.Errorf("tcp %d: got %q err=%v", i, buf, err)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			st, err := sess.OpenStream()
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			proto.WriteMsgType(st, proto.MsgTypeUDP)
			msg := fmt.Sprintf("dns-%d", i)
			proto.WriteUDPFrame(st, "127.0.0.1", uint16(udpTarget.Port), []byte(msg))
			_, port, payload, err := proto.ReadUDPFrame(st)
			if err != nil || string(payload) != "udp:"+msg || int(port) != udpTarget.Port {
				errs <- fmt.Errorf("udp %d: got %q port=%d err=%v", i, payload, port, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if d.tcp.Load() != perType || d.udp.Load() != perType {
		t.Errorf("dispatched tcp=%d udp=%d, want %d each", d.tcp.Load(), d.udp.Load(), perType)
	}
	d.remotes.Range(func(k, _ any) bool {
		if k.(string) != cc.LocalAddr().String() {
			t.Errorf("stream RemoteAddr = %s, want client %s", k, cc.LocalAddr())
		}
		return true
	})

	// Dropping the connection ends the session; serveMux reports the streams
	// it served once their handlers are done.
	sess.Close()
	select {
	case r := <-res:
		if r.streams != 2*perType {
			t.Errorf("serveMux served %d streams, want %d", r.streams, 2*perType)
		}
		if r.err == nil {
			t.Error("serveMux returned no reason")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveMux did not return after the connection closed")
	}
}

// A stream with an unknown type is dropped on its own; the session and its
// other streams keep working.
func TestServeMuxUnknownStreamTypeKeepsSession(t *testing.T) {
	tcpTarget, _ := startTargets(t)
	d := &fakeDispatch{}
	sess, _, res := muxPair(t, d)
	defer func() {
		sess.Close()
		<-res
	}()

	bad, err := sess.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	proto.WriteMsgType(bad, 0x7f)
	bad.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := bad.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("unknown-type stream: read err = %v, want EOF", err)
	}
	bad.Close()

	st, err := sess.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proto.WriteMsgType(st, proto.MsgTypeTCP)
	proto.WriteConnect(st, "127.0.0.1", uint16(tcpTarget.Port))
	if ok, err := proto.ReadResult(st); err != nil || !ok {
		t.Fatalf("connect after bad stream: ok=%v err=%v", ok, err)
	}
	st.Write([]byte("x"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(st, buf); err != nil || !strings.HasPrefix(string(buf), "tcp:x") {
		t.Fatalf("echo after bad stream: %q %v", buf, err)
	}
	if d.other.Load() != 1 {
		t.Errorf("unknown-type dispatches = %d, want 1", d.other.Load())
	}
}

// shrinkRecheck makes sessions re-check their device every d for one test.
func shrinkRecheck(t *testing.T, d time.Duration) {
	t.Helper()
	old := muxRecheckInterval
	muxRecheckInterval = d
	t.Cleanup(func() { muxRecheckInterval = old })
}

// When the recheck says the device is gone, serveMux closes the session: the
// stream a client has open errors out, the client session dies, and serveMux
// itself returns.
func TestServeMuxClosesSessionWhenRecheckFails(t *testing.T) {
	shrinkRecheck(t, 20*time.Millisecond)
	tcpTarget, _ := startTargets(t)
	d := &fakeDispatch{}
	var calls atomic.Int64
	var revoked atomic.Bool
	sess, _, res := muxPairRecheck(t, d, func() bool {
		calls.Add(1)
		return !revoked.Load()
	})
	defer sess.Close()

	st, err := sess.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proto.WriteMsgType(st, proto.MsgTypeTCP)
	proto.WriteConnect(st, "127.0.0.1", uint16(tcpTarget.Port))
	if ok, err := proto.ReadResult(st); err != nil || !ok {
		t.Fatalf("connect: ok=%v err=%v", ok, err)
	}

	// Still allowed: the stream works across a few recheck intervals.
	time.Sleep(100 * time.Millisecond)
	st.Write([]byte("x"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(st, buf); err != nil || string(buf) != "tcp:x" {
		t.Fatalf("echo before revoke: %q %v", buf, err)
	}

	revoked.Store(true)
	st.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := st.Read(make([]byte, 1)); err == nil {
		t.Fatal("open stream read succeeded after the device was revoked")
	}
	select {
	case <-sess.CloseChan():
	case <-time.After(5 * time.Second):
		t.Fatal("client session still alive after the device was revoked")
	}
	select {
	case r := <-res:
		if r.streams != 1 {
			t.Errorf("serveMux served %d streams, want 1", r.streams)
		}
		if r.err == nil {
			t.Error("serveMux returned no reason")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveMux did not return after the device was revoked")
	}
	if _, err := sess.OpenStream(); err == nil {
		t.Error("OpenStream on a revoked session succeeded")
	}
}

// A recheck that keeps saying yes never ends the session, however many
// intervals pass.
func TestServeMuxKeepsSessionWhileRecheckPasses(t *testing.T) {
	shrinkRecheck(t, 20*time.Millisecond)
	tcpTarget, _ := startTargets(t)
	d := &fakeDispatch{}
	var calls atomic.Int64
	sess, _, res := muxPairRecheck(t, d, func() bool {
		calls.Add(1)
		return true
	})
	defer func() {
		sess.Close()
		<-res
	}()

	time.Sleep(300 * time.Millisecond) // 15 intervals
	if n := calls.Load(); n < 5 {
		t.Fatalf("recheck ran %d times in 15 intervals, want at least 5", n)
	}
	if sess.IsClosed() {
		t.Fatal("session closed although every recheck passed")
	}
	select {
	case r := <-res:
		t.Fatalf("serveMux returned (%v) although every recheck passed", r.err)
	default:
	}
	st, err := sess.OpenStream()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	proto.WriteMsgType(st, proto.MsgTypeTCP)
	proto.WriteConnect(st, "127.0.0.1", uint16(tcpTarget.Port))
	if ok, err := proto.ReadResult(st); err != nil || !ok {
		t.Fatalf("connect after 15 intervals: ok=%v err=%v", ok, err)
	}
}

// The recheck goroutine ends with the session: once serveMux has returned
// (the client hung up, nothing revoked) recheck is never called again and no
// goroutine is left behind.
func TestServeMuxRecheckEndsWithSession(t *testing.T) {
	shrinkRecheck(t, 10*time.Millisecond)
	d := &fakeDispatch{}
	before := runtime.NumGoroutine()

	var calls atomic.Int64
	sess, _, res := muxPairRecheck(t, d, func() bool {
		calls.Add(1)
		return true
	})
	time.Sleep(60 * time.Millisecond)
	if calls.Load() == 0 {
		t.Fatal("recheck never ran")
	}
	sess.Close()
	select {
	case <-res:
	case <-time.After(5 * time.Second):
		t.Fatal("serveMux did not return after the client closed the session")
	}

	after := calls.Load()
	time.Sleep(100 * time.Millisecond) // 10 intervals
	if n := calls.Load(); n != after {
		t.Fatalf("recheck ran %d more times after serveMux returned", n-after)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("goroutines: %d before, %d after\n%s", before, runtime.NumGoroutine(), buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Same for a session the recheck itself cut: serveMux has returned and the
// recheck is not called again.
func TestServeMuxRecheckStopsAfterRevoke(t *testing.T) {
	shrinkRecheck(t, 10*time.Millisecond)
	d := &fakeDispatch{}
	var calls atomic.Int64
	sess, _, res := muxPairRecheck(t, d, func() bool { return calls.Add(1) < 3 })
	defer sess.Close()
	select {
	case <-res:
	case <-time.After(5 * time.Second):
		t.Fatal("serveMux did not return after the recheck failed")
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("recheck ran %d times, want 3 (two passes, one fail)", n)
	}
	time.Sleep(100 * time.Millisecond)
	if n := calls.Load(); n != 3 {
		t.Fatalf("recheck ran %d times after the session was cut", n-3)
	}
}

// Which (device, error) pairs from GetDeviceByKey cut a live session: a
// disabled or deleted device does, a database failure does not.
func TestMuxRevokeReason(t *testing.T) {
	notFound := fmt.Errorf("%w for key %q", db.ErrDeviceNotFound, "abcd")
	cases := []struct {
		name string
		dev  db.Device
		err  error
		want string
	}{
		{"active device", db.Device{Active: true}, nil, ""},
		{"disabled device", db.Device{Active: false}, nil, "disabled"},
		{"deleted, bare sentinel", db.Device{}, db.ErrDeviceNotFound, "deleted"},
		{"deleted, as getDeviceByKeyDB words it", db.Device{}, notFound, "deleted"},
		{"deleted, wrapped again", db.Device{}, fmt.Errorf("lookup: %w", notFound), "deleted"},
		{"deleted, joined", db.Device{}, errors.Join(errors.New("x"), notFound), "deleted"},
		{"database down", db.Device{}, errors.New("get device by key: connection refused"), ""},
		{"database error wrapped", db.Device{}, fmt.Errorf("get device by key: %w", sql.ErrConnDone), ""},
		{"deadline exceeded", db.Device{}, context.DeadlineExceeded, ""},
		{"error with a stale inactive device", db.Device{Active: false}, errors.New("boom"), ""},
		{"sql.ErrNoRows is not our sentinel", db.Device{}, sql.ErrNoRows, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := muxRevokeReason(c.dev, c.err); got != c.want {
				t.Errorf("muxRevokeReason(active=%v, %v) = %q, want %q", c.dev.Active, c.err, got, c.want)
			}
		})
	}
}

// yamux v0.1.2 closes the whole session when a keepalive ping goes unanswered
// for ConnectionWriteTimeout, or a stream's SYN stays unacked for
// StreamOpenTimeout. 10s would let a 10–20s stall (Wi-Fi roaming, LTE handover)
// kill every stream at once, where a TCP per stream used to outlive ~150s.
// Keep in pair with daemon/internal/transport (TestMuxClientConfig).
func TestMuxConfigTimeouts(t *testing.T) {
	cfg := muxConfig()
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
