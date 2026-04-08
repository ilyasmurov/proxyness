package sites

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestManagerRefreshLoadsCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{
			MySites: []MySite{
				{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
			},
			ServerTime: time.Now().Unix(),
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := NewManager(srv.URL, keyStore)

	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if m := mgr.Cache().Match("habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected habr.com matched after refresh")
	}
}

func TestManagerAddSiteRefreshesCache(t *testing.T) {
	var sitesMu sync.Mutex
	currentSites := []MySite{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		sitesMu.Lock()
		ops, _ := req["ops"].([]interface{})
		for _, op := range ops {
			m := op.(map[string]interface{})
			if m["op"] == "add" {
				site := m["site"].(map[string]interface{})
				newID := len(currentSites) + 1
				currentSites = append(currentSites, MySite{
					ID:            newID,
					PrimaryDomain: site["primary_domain"].(string),
					Domains:       []string{site["primary_domain"].(string)},
					Enabled:       true,
				})
			}
		}
		sites := append([]MySite{}, currentSites...)
		sitesMu.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"op_results":  []map[string]interface{}{{"site_id": len(sites), "status": "ok", "deduped": false}},
			"my_sites":    sites,
			"server_time": time.Now().Unix(),
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := NewManager(srv.URL, keyStore)

	siteID, deduped, err := mgr.AddSite("habr.com", "Habr")
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	if siteID == 0 || deduped {
		t.Fatalf("unexpected: id=%d deduped=%v", siteID, deduped)
	}

	// Cache should now contain habr.com.
	if m := mgr.Cache().Match("habr.com"); m == nil {
		t.Fatalf("expected cache to contain habr.com after AddSite")
	}
}
