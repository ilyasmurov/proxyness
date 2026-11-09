package transport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/yamux"

	"proxyness/pkg/proto"
)

var testKey = strings.Repeat("0", 64)

// refusedHost is the TCP destination the fake server refuses, the way the real
// one answers when the target does not accept the connection. silentHost
// never gets an answer at all.
const (
	refusedHost = "refused.test"
	silentHost  = "silent.test"
)

// fakeServer speaks the proxy protocol over TLS on 127.0.0.1 and counts every
// incoming TCP connection. TCP streams echo whatever the client writes; UDP
// streams echo every UDP frame back. With noMux it behaves like a pre-PRXNS-33
// server: MsgTypeMux is an unknown type and the connection is closed.
type fakeServer struct {
	t     *testing.T
	ln    net.Listener
	noMux bool

	accepted       atomic.Int64
	rejectMID      atomic.Bool
	handshakeDelay atomic.Int64 // ns, slept before reading auth

	mu       sync.Mutex
	conns    []net.Conn // every accepted conn, closed on shutdown
	muxConns []net.Conn // conns that switched to a yamux session
	wg       sync.WaitGroup
}

func newFakeServer(t *testing.T, noMux bool) *fakeServer {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: priv}},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{t: t, ln: ln, noMux: noMux}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			f.accepted.Add(1)
			c := tls.Server(raw, cfg)
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			f.wg.Add(1)
			go func() {
				defer f.wg.Done()
				f.serve(c)
			}()
		}
	}()
	t.Cleanup(f.shutdown)
	return f
}

func (f *fakeServer) addr() string { return f.ln.Addr().String() }

func (f *fakeServer) shutdown() {
	f.ln.Close()
	f.mu.Lock()
	for _, c := range f.conns {
		c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

// killMuxConn drops the server side of the i-th yamux session's connection.
func (f *fakeServer) killMuxConn(i int) {
	f.mu.Lock()
	c := f.muxConns[i]
	f.mu.Unlock()
	c.Close()
}

func (f *fakeServer) muxConnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.muxConns)
}

func testServerMuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = 10 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.StreamOpenTimeout = 10 * time.Second
	cfg.MaxStreamWindowSize = 4 << 20
	cfg.LogOutput = io.Discard
	return cfg
}

func (f *fakeServer) serve(c net.Conn) {
	defer c.Close()
	if d := f.handshakeDelay.Load(); d > 0 {
		time.Sleep(time.Duration(d))
	}
	if err := proto.ReadAuth(c, testKey); err != nil {
		proto.WriteResult(c, false)
		return
	}
	proto.WriteResult(c, true)
	msgType, err := proto.ReadMsgType(c)
	if err != nil {
		return
	}
	if msgType == proto.MsgTypeMachineID {
		if _, err := proto.ReadMachineID(c); err != nil {
			return
		}
		if f.rejectMID.Load() {
			proto.WriteResult(c, false)
			return
		}
		proto.WriteResult(c, true)
		if msgType, err = proto.ReadMsgType(c); err != nil {
			return
		}
	}
	if msgType != proto.MsgTypeMux {
		f.handleStream(c, msgType)
		return
	}
	if f.noMux {
		return // pre-mux server: "unknown msg type: 0x04", connection closed
	}
	proto.WriteResult(c, true)
	sess, err := yamux.Server(c, testServerMuxConfig())
	if err != nil {
		return
	}
	defer sess.Close()
	f.mu.Lock()
	f.muxConns = append(f.muxConns, c)
	f.mu.Unlock()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		st, err := sess.AcceptStream()
		if err != nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer st.Close()
			t, err := proto.ReadMsgType(st)
			if err != nil {
				return
			}
			f.handleStream(st, t)
		}()
	}
}

func (f *fakeServer) handleStream(c net.Conn, msgType byte) {
	switch msgType {
	case proto.MsgTypeTCP:
		host, _, err := proto.ReadConnect(c)
		if err != nil {
			return
		}
		if host == refusedHost {
			proto.WriteResult(c, false)
			return
		}
		if host == silentHost {
			io.Copy(io.Discard, c)
			return
		}
		proto.WriteResult(c, true)
		io.Copy(c, c)
	case proto.MsgTypeUDP:
		for {
			addr, port, payload, err := proto.ReadUDPFrame(c)
			if err != nil {
				return
			}
			if err := proto.WriteUDPFrame(c, addr, port, payload); err != nil {
				return
			}
		}
	}
}

// echoTCP writes msg into a TCP stream and checks it comes back unchanged.
func echoTCP(s Stream, msg string) error {
	if _, err := s.Write([]byte(msg)); err != nil {
		return err
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(s, buf); err != nil {
		return err
	}
	if string(buf) != msg {
		return &echoMismatch{want: msg, got: string(buf)}
	}
	return nil
}

// echoUDP sends one UDP frame through a UDP stream and checks the echo.
func echoUDP(s Stream, msg string) error {
	if err := proto.WriteUDPFrame(s, "1.1.1.1", 53, []byte(msg)); err != nil {
		return err
	}
	_, _, payload, err := proto.ReadUDPFrame(s)
	if err != nil {
		return err
	}
	if string(payload) != msg {
		return &echoMismatch{want: msg, got: string(payload)}
	}
	return nil
}

type echoMismatch struct{ want, got string }

func (e *echoMismatch) Error() string { return "echo mismatch: want " + e.want + ", got " + e.got }

// liveSessions counts the pool's sessions yamux still runs.
func (t *TLSTransport) liveSessions() int {
	t.poolMu.Lock()
	defer t.poolMu.Unlock()
	live, _ := t.liveLocked()
	return live
}

// pooledSessions counts sessions still in the pool, live or not.
func (t *TLSTransport) pooledSessions() int {
	t.poolMu.Lock()
	defer t.poolMu.Unlock()
	return len(t.sessions)
}

// setMuxLimits changes the pool knobs for one test.
func setMuxLimits(t *testing.T, base, max int) {
	t.Helper()
	oldBase, oldMax := tlsMuxBaseSessions, tlsMuxMaxSessions
	tlsMuxBaseSessions, tlsMuxMaxSessions = base, max
	t.Cleanup(func() { tlsMuxBaseSessions, tlsMuxMaxSessions = oldBase, oldMax })
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
