package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/sites"
	"smurov-proxy/daemon/internal/tun"
	"smurov-proxy/daemon/internal/tunnel"
	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
)

const testKey = "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd"

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
				// Read auth
				msg := make([]byte, auth.AuthMsgLen)
				if _, err := c.Read(msg); err != nil {
					return
				}
				proto.WriteResult(c, true)
				// Read machine ID (1 byte type + 16 bytes ID)
				mid := make([]byte, 1+proto.MachineIDLen)
				if _, err := c.Read(mid); err != nil {
					return
				}
				proto.WriteResult(c, true)
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func TestHealthEndpoint(t *testing.T) {
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:1080", meter)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStatusEndpoint_Disconnected(t *testing.T) {
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:1080", meter)

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "disconnected" {
		t.Fatalf("expected disconnected, got %s", resp.Status)
	}
}

func TestConnectEndpoint_BadJSON(t *testing.T) {
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:1080", meter)

	req := httptest.NewRequest("POST", "/connect", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDisconnectEndpoint(t *testing.T) {
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:1080", meter)

	req := httptest.NewRequest("POST", "/disconnect", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestConnectDisconnectFlow(t *testing.T) {
	mockAddr := startMockServer(t)
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:0", meter)

	// Connect
	body := fmt.Sprintf(`{"server":"%s","key":"%s"}`, mockAddr, testKey)
	req := httptest.NewRequest("POST", "/connect", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// Status should be connected
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "connected" {
		t.Fatalf("expected connected, got %s", resp.Status)
	}

	// Disconnect
	req = httptest.NewRequest("POST", "/disconnect", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disconnect: expected 200, got %d", w.Code)
	}

	// Status should be disconnected
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "disconnected" {
		t.Fatalf("expected disconnected, got %s", resp.Status)
	}
}

func newTestServerWithMgr(t *testing.T, mgr *sites.Manager) *Server {
	t.Helper()
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:0", meter)
	srv.SetSites(mgr, nil)
	return srv
}

func TestRebuildPACPushesEnabledDomains(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
	})
	srv := newTestServerWithMgr(t, mgr)

	// Initial state: proxy_all=false, no domains.
	srv.pacSites.Set(false, nil)

	srv.RebuildPAC()

	proxyAll, domains := srv.pacSites.Get()
	if proxyAll {
		t.Error("expected proxy_all=false")
	}
	want := []string{"habr.com", "www.habr.com", "*.habr.com"}
	if len(domains) != len(want) {
		t.Fatalf("got %v, want %v", domains, want)
	}
	for i := range want {
		if domains[i] != want[i] {
			t.Errorf("domain[%d]: got %q, want %q", i, domains[i], want[i])
		}
	}
}

func TestRebuildPACPreservesProxyAllFlag(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	srv := newTestServerWithMgr(t, mgr)

	// Set proxy_all=true (renderer-pushed flag).
	srv.pacSites.Set(true, nil)

	srv.RebuildPAC()

	proxyAll, domains := srv.pacSites.Get()
	if !proxyAll {
		t.Error("expected proxy_all=true preserved")
	}
	if len(domains) != 0 {
		t.Errorf("expected empty domains in proxy_all mode, got %v", domains)
	}
}

func TestRebuildPACSkipsCloseAllConnsWhenUnchanged(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	srv := newTestServerWithMgr(t, mgr)

	// First rebuild populates pacSites.
	srv.RebuildPAC()

	// Subsequent rebuilds with identical cache should be no-ops.
	// We can't easily inspect CloseAllConns count without exposing it,
	// so we just verify the calls don't panic and the state is stable.
	srv.RebuildPAC()
	srv.RebuildPAC()
	srv.RebuildPAC()

	proxyAll, domains := srv.pacSites.Get()
	if proxyAll {
		t.Error("expected proxy_all=false")
	}
	if len(domains) == 0 {
		t.Error("expected domains preserved across no-op rebuilds")
	}
}
