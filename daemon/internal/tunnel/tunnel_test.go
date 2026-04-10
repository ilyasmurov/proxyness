package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
)

const testKey = "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd"

// startMockServer starts a TLS server that accepts auth and responds OK.
func startMockServer(t *testing.T) string {
	t.Helper()
	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  privKey,
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				msg := make([]byte, auth.AuthMsgLen)
				if _, err := c.Read(msg); err != nil {
					return
				}
				proto.WriteResult(c, true)
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func TestNew(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
	if tun.Uptime() != 0 {
		t.Fatalf("expected 0 uptime, got %d", tun.Uptime())
	}
}

func TestStartStop(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())

	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tun.GetStatus() != Connected {
		t.Fatalf("expected connected, got %s", tun.GetStatus())
	}

	tun.Stop()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
}

func TestDoubleStart(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())

	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	err = tun.Start("127.0.0.1:0", addr, testKey)
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestUptime(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())
	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	if tun.Uptime() < 0 {
		t.Fatal("uptime should be >= 0")
	}
}

// TestTouchHostExpiry verifies that hosts which haven't seen any traffic
// for hostLiveWindow are removed from GetActiveHosts. This is the core
// fix for stale LIVE indicators when a browser closes a tab but keeps
// the SOCKS5 connection idle in its HTTP/2 pool.
func TestTouchHostExpiry(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	tun.hostLiveWindow = 30 * time.Millisecond

	tun.touchHost("instagram.com")
	tun.touchHost("youtube.com")

	hosts := tun.GetActiveHosts()
	if len(hosts) != 2 {
		t.Fatalf("expected 2 active hosts, got %d: %v", len(hosts), hosts)
	}

	time.Sleep(50 * time.Millisecond)

	hosts = tun.GetActiveHosts()
	if len(hosts) != 0 {
		t.Fatalf("expected 0 active hosts after window expiry, got %d: %v", len(hosts), hosts)
	}
}

// TestTouchHostRefresh verifies that re-touching a host before the window
// expires keeps it alive. This is what happens when bytes are still
// flowing for an active site.
func TestTouchHostRefresh(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	tun.hostLiveWindow = 60 * time.Millisecond

	tun.touchHost("instagram.com")
	time.Sleep(40 * time.Millisecond)
	tun.touchHost("instagram.com") // refresh before expiry
	time.Sleep(40 * time.Millisecond)

	hosts := tun.GetActiveHosts()
	if len(hosts) != 1 || hosts[0] != "instagram.com" {
		t.Fatalf("expected refreshed host to stay alive, got %v", hosts)
	}
}

// TestTouchHostEmpty verifies that empty hostnames are silently ignored
// (defensive: socks5 handshake could in theory yield an empty addr).
func TestTouchHostEmpty(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	tun.touchHost("")
	if hosts := tun.GetActiveHosts(); len(hosts) != 0 {
		t.Fatalf("empty host should be ignored, got %v", hosts)
	}
}

// TestGetActiveHostsSweepsStale verifies that GetActiveHosts removes
// stale entries from the underlying map (not just filters them out), so
// the map does not grow unbounded over a long session.
func TestGetActiveHostsSweepsStale(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	tun.hostLiveWindow = 20 * time.Millisecond

	for i := 0; i < 100; i++ {
		tun.touchHost("host-" + string(rune('a'+i%26)))
	}

	time.Sleep(40 * time.Millisecond)
	tun.GetActiveHosts() // sweep

	tun.activeHostsMu.Lock()
	remaining := len(tun.activeHosts)
	tun.activeHostsMu.Unlock()

	if remaining != 0 {
		t.Fatalf("expected map to be swept clean, got %d entries", remaining)
	}
}

func TestSetReconnectingOnlyFromConnected(t *testing.T) {
	tn := New(dstats.NewRateMeter())

	// status starts as Disconnected → setReconnecting must be a no-op
	tn.setReconnecting()
	if tn.GetStatus() != Disconnected {
		t.Fatalf("expected Disconnected, got %s", tn.GetStatus())
	}

	// Force Connected
	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()

	tn.setReconnecting()
	if tn.GetStatus() != Reconnecting {
		t.Fatalf("expected Reconnecting, got %s", tn.GetStatus())
	}

	// Idempotent: a second call from Reconnecting must not panic / change state
	tn.setReconnecting()
	if tn.GetStatus() != Reconnecting {
		t.Fatalf("setReconnecting should be idempotent, got %s", tn.GetStatus())
	}
}

func TestSetConnectedOnlyFromReconnecting(t *testing.T) {
	tn := New(dstats.NewRateMeter())

	tn.setConnected()
	if tn.GetStatus() != Disconnected {
		t.Fatalf("setConnected from Disconnected must be a no-op, got %s", tn.GetStatus())
	}

	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()
	tn.setConnected()
	if tn.GetStatus() != Connected {
		t.Fatalf("setConnected from Connected must be a no-op, got %s", tn.GetStatus())
	}

	tn.mu.Lock()
	tn.status = Reconnecting
	tn.mu.Unlock()
	tn.setConnected()
	if tn.GetStatus() != Connected {
		t.Fatalf("expected Connected after recovery, got %s", tn.GetStatus())
	}
}

func TestStallDetectedRequiresActiveHosts(t *testing.T) {
	meter := dstats.NewRateMeter()
	tn := New(meter)

	// Even with stale lastByteAt, an idle session (no active hosts)
	// must not trip the stall detector — otherwise we'd flicker
	// Reconnecting forever during inactive periods.
	meter.Add(1, 0)
	meter.SeedLastByteAtForTest(time.Now().Add(-2 * stallThreshold))
	if tn.stallDetected() {
		t.Fatalf("idle session must not trigger stall detector")
	}

	// Touch a host but with a fresh lastByteAt → still no stall
	tn.touchHost("example.com")
	meter.SeedLastByteAtForTest(time.Now())
	if tn.stallDetected() {
		t.Fatalf("fresh meter must not trigger stall detector")
	}
}

func TestStallDetectedTripsWhenStale(t *testing.T) {
	meter := dstats.NewRateMeter()
	tn := New(meter)
	tn.touchHost("example.com")
	meter.Add(1, 0)
	meter.SeedLastByteAtForTest(time.Now().Add(-2 * stallThreshold))

	if !tn.stallDetected() {
		t.Fatalf("expected stallDetected=true with stale lastByteAt and active hosts")
	}
}

func TestStallDetectedIgnoresStaleMapEntries(t *testing.T) {
	meter := dstats.NewRateMeter()
	tn := New(meter)

	// Simulate the false-positive scenario: a host was touched long ago
	// and never swept from the raw map. Without the sweep fix, raw
	// len(activeHosts) == 1 and D3 would fire. With the fix,
	// GetActiveHosts sweeps the stale entry → hostCount 0 → no stall.
	tn.activeHostsMu.Lock()
	tn.activeHosts["stale.example.com"] = time.Now().Add(-2 * defaultHostLiveWindow)
	tn.activeHostsMu.Unlock()

	meter.Add(1, 0)
	meter.SeedLastByteAtForTest(time.Now().Add(-2 * stallThreshold))

	if tn.stallDetected() {
		t.Fatalf("stale map entry must not trigger stall detector")
	}
}

func TestSetReconnectingClosesActiveConns(t *testing.T) {
	tn := New(dstats.NewRateMeter())
	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()

	a, b := net.Pipe()
	defer b.Close()
	tn.trackConn(a)

	tn.setReconnecting()

	// `a` should now be closed — Read returns immediately with an error.
	a.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := a.Read(buf); err == nil {
		t.Fatalf("expected closed conn to error on Read")
	}
}

func TestHandleSOCKSRejectsDuringReconnecting(t *testing.T) {
	tn := New(dstats.NewRateMeter())
	tn.mu.Lock()
	tn.status = Reconnecting
	tn.mu.Unlock()

	// Pipe pair: we play the SOCKS5 client on clientEnd and let
	// handleSOCKS serve serverEnd.
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()

	// Channel carries the CONNECT reply bytes read by the client goroutine.
	type result struct {
		data []byte
		err  error
	}
	replyCh := make(chan result, 1)

	// The client goroutine drives the full handshake and collects the
	// CONNECT reply so there is no read race with the test body.
	go func() {
		defer clientEnd.Close()
		// auth method negotiation: ver=5, nmethods=1, method=NOAUTH
		if _, err := clientEnd.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			replyCh <- result{nil, err}
			return
		}
		// read the 2-byte method selection response
		methodResp := make([]byte, 2)
		if _, err := clientEnd.Read(methodResp); err != nil {
			replyCh <- result{nil, err}
			return
		}
		// connect request: ver=5, cmd=CONNECT, rsv=0, atyp=IPv4, 1.2.3.4, port 80
		if _, err := clientEnd.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0, 80}); err != nil {
			replyCh <- result{nil, err}
			return
		}
		// read the CONNECT reply (10 bytes from SendFailure)
		clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 10)
		n, err := clientEnd.Read(buf)
		replyCh <- result{buf[:n], err}
	}()

	done := make(chan struct{})
	go func() {
		tn.handleSOCKS(serverEnd)
		close(done)
	}()

	res := <-replyCh
	if res.err != nil {
		t.Fatalf("expected SOCKS5 reply, got err=%v", res.err)
	}
	if len(res.data) < 2 {
		t.Fatalf("expected at least 2 bytes, got %d", len(res.data))
	}
	if res.data[0] != 0x05 {
		t.Fatalf("expected SOCKS5 version 0x05, got 0x%02x", res.data[0])
	}
	if res.data[1] == 0x00 {
		t.Fatalf("expected SOCKS5 failure (REP != 0x00), got reply=%v", res.data)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSOCKS did not return")
	}
}
