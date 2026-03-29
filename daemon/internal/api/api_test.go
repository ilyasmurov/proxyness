package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smurov-proxy/daemon/internal/tunnel"
)

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
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:0") // port 0 to avoid conflicts

	// Connect
	body := `{"server":"127.0.0.1:9999","key":"aabbccdd"}`
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
