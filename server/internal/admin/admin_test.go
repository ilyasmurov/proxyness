package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"smurov-proxy/server/internal/db"
	"smurov-proxy/server/internal/stats"
)

func setup(t *testing.T) *Handler {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return NewHandler(d, stats.New(), "admin", "secret", "")
}

func authed(req *http.Request) *http.Request {
	req.SetBasicAuth("admin", "secret")
	return req
}

// TestHealthNoAuth verifies that unauthenticated requests receive a 401.
func TestHealthNoAuth(t *testing.T) {
	h := setup(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/stats/overview", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestCreateAndListUsers creates a user and verifies it appears in the list.
func TestCreateAndListUsers(t *testing.T) {
	h := setup(t)

	// Create user
	body := `{"name":"alice"}`
	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(body)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create user: expected 201, got %d — %s", rr.Code, rr.Body.String())
	}

	var created db.User
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode created user: %v", err)
	}
	if created.Name != "alice" {
		t.Fatalf("expected name=alice, got %q", created.Name)
	}

	// List users
	req = authed(httptest.NewRequest(http.MethodGet, "/admin/api/users", nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list users: expected 200, got %d", rr.Code)
	}

	var users []db.User
	if err := json.NewDecoder(rr.Body).Decode(&users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Name != "alice" {
		t.Fatalf("expected alice, got %q", users[0].Name)
	}
}

// TestCreateDeviceReturnsKey creates a device and verifies the key is a 64-char hex string.
func TestCreateDeviceReturnsKey(t *testing.T) {
	h := setup(t)

	// Create user first
	userBody := `{"name":"bob"}`
	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(userBody)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create user: expected 201, got %d", rr.Code)
	}
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	// Create device
	devBody := `{"name":"laptop"}`
	url := "/admin/api/users/" + itoa(user.ID) + "/devices"
	req = authed(httptest.NewRequest(http.MethodPost, url, strings.NewReader(devBody)))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create device: expected 201, got %d — %s", rr.Code, rr.Body.String())
	}

	var dev db.Device
	if err := json.NewDecoder(rr.Body).Decode(&dev); err != nil {
		t.Fatalf("decode device: %v", err)
	}
	if len(dev.Key) != 64 {
		t.Fatalf("expected 64-char key, got len=%d key=%q", len(dev.Key), dev.Key)
	}
	for _, c := range dev.Key {
		if !isHex(byte(c)) {
			t.Fatalf("key contains non-hex char %q", c)
		}
	}
}

// TestToggleDevice patches a device to active=false and verifies 200.
func TestToggleDevice(t *testing.T) {
	h := setup(t)

	// Create user
	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", strings.NewReader(`{"name":"carol"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	// Create device
	url := "/admin/api/users/" + itoa(user.ID) + "/devices"
	req = authed(httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"name":"phone"}`)))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var dev db.Device
	json.NewDecoder(rr.Body).Decode(&dev)

	// Toggle device to inactive
	patchBody := `{"active":false}`
	patchURL := "/admin/api/devices/" + itoa(dev.ID)
	req = authed(httptest.NewRequest(http.MethodPatch, patchURL, bytes.NewBufferString(patchBody)))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("toggle device: expected 200, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestOverview verifies the overview endpoint returns the expected JSON structure.
func TestOverview(t *testing.T) {
	h := setup(t)

	req := authed(httptest.NewRequest(http.MethodGet, "/admin/api/stats/overview", nil))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("overview: expected 200, got %d — %s", rr.Code, rr.Body.String())
	}

	var ov db.Overview
	if err := json.NewDecoder(rr.Body).Decode(&ov); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	// Fresh DB: totals should be 0
	if ov.TotalBytesIn != 0 || ov.TotalBytesOut != 0 {
		t.Fatalf("expected zero bytes, got in=%d out=%d", ov.TotalBytesIn, ov.TotalBytesOut)
	}
	// ActiveConnections field must exist (zero on fresh tracker)
	if ov.ActiveConnections != 0 {
		t.Fatalf("expected 0 active connections, got %d", ov.ActiveConnections)
	}
}

// ---- helpers ----

func itoa(n int) string {
	return strconv.Itoa(n)
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
