package proxy

import (
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
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
		n, err := serveMux(sc, d.dispatch)
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
