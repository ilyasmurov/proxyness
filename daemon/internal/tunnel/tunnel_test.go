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
