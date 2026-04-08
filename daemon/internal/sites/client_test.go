package sites

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncClientSendsAuthAndOps(t *testing.T) {
	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dev-key-123" {
			t.Errorf("expected Bearer dev-key-123, got %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"op_results":[{"site_id":42,"status":"ok"}],"my_sites":[],"server_time":1000}`))
	}))
	defer srv.Close()

	client := NewSyncClient(srv.URL, "dev-key-123")
	resp, err := client.SyncOps([]map[string]interface{}{
		{"op": "add", "local_id": -1, "site": map[string]string{"primary_domain": "habr.com", "label": "Habr"}, "at": 1000},
	})
	if err != nil {
		t.Fatalf("SyncOps: %v", err)
	}

	if len(resp.OpResults) != 1 || resp.OpResults[0].SiteID != 42 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if captured["last_sync_at"].(float64) != 0 {
		t.Errorf("expected last_sync_at=0, got %v", captured["last_sync_at"])
	}
	ops := captured["ops"].([]interface{})
	if len(ops) != 1 {
		t.Errorf("expected 1 op, got %d", len(ops))
	}
}

func TestSyncClientReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 503)
	}))
	defer srv.Close()

	client := NewSyncClient(srv.URL, "dev-key-123")
	_, err := client.SyncOps(nil)
	if err == nil {
		t.Fatalf("expected error on 503")
	}
}

func TestSyncClientHandlesNoKey(t *testing.T) {
	client := NewSyncClient("http://example.com", "")
	_, err := client.SyncOps(nil)
	if err == nil {
		t.Fatalf("expected error when key is empty")
	}
}
