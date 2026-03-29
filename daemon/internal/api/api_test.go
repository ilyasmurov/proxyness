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
	"strings"
	"testing"
	"time"

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

func TestHealthEndpoint(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStatusEndpoint_Disconnected(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

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
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("POST", "/connect", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDisconnectEndpoint(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("POST", "/disconnect", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestConnectDisconnectFlow(t *testing.T) {
	mockAddr := startMockServer(t)
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:0")

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
