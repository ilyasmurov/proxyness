package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"proxyness/server/internal/db"
)

func newTestSyncHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	u, err := d.CreateUser("alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	dev, err := d.CreateDevice(u.ID, "mac")
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	h := NewHandler(d, nil, "admin", "pw", t.TempDir())
	return h, dev.Key
}

func postSync(t *testing.T, h *Handler, key string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/sync", bytes.NewReader(buf))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSyncRejectsMissingAuth(t *testing.T) {
	h, _ := newTestSyncHandler(t)
	r := httptest.NewRequest("POST", "/api/sync", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

func TestSyncEmptyOpsReturnsSnapshot(t *testing.T) {
	h, key := newTestSyncHandler(t)

	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops":          []interface{}{},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["my_sites"].([]interface{}); !ok {
		t.Fatalf("my_sites missing or wrong type: %v", resp)
	}
}

func TestSyncAddOpRoundTrip(t *testing.T) {
	h, key := newTestSyncHandler(t)

	localID := -1
	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{
				"op":       "add",
				"local_id": localID,
				"site":     map[string]string{"primary_domain": "example.com", "label": "Example"},
				"at":       1000,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OpResults []struct {
			LocalID *int   `json:"local_id"`
			Status  string `json:"status"`
			SiteID  int    `json:"site_id"`
		} `json:"op_results"`
		MySites []struct {
			ID      int      `json:"id"`
			Slug    string   `json:"slug"`
			Label   string   `json:"label"`
			Domains []string `json:"domains"`
			Enabled bool     `json:"enabled"`
		} `json:"my_sites"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.OpResults) != 1 || resp.OpResults[0].Status != "ok" {
		t.Fatalf("op_results = %+v", resp.OpResults)
	}
	found := false
	for _, s := range resp.MySites {
		if s.Slug == "example" && s.Enabled {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("example.com not in my_sites: %+v", resp.MySites)
	}
}

func TestSyncInvalidDomainReturnsInvalidStatus(t *testing.T) {
	h, key := newTestSyncHandler(t)

	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{
				"op":       "add",
				"local_id": -1,
				"site":     map[string]string{"primary_domain": "NOT A DOMAIN", "label": "X"},
				"at":       1000,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		OpResults []struct{ Status string } `json:"op_results"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.OpResults[0].Status != "invalid" {
		t.Fatalf("status = %q, want invalid", resp.OpResults[0].Status)
	}
}
