package sites

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
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

func TestManagerSetOnCacheReplacedFiresAfterRefresh(t *testing.T) {
	// Use a fake server so Refresh() succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{
			MySites:    []MySite{{ID: 1, PrimaryDomain: "x.com", Enabled: true}},
			ServerTime: 1000,
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	var calls int32
	mgr.SetOnCacheReplaced(func() {
		atomic.AddInt32(&calls, 1)
	})

	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 callback fire, got %d", calls)
	}
}

func TestManagerCallbackNilSafe(t *testing.T) {
	// No callback set — Refresh must not panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{ServerTime: 1000})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)
	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestManagerEnabledDomains(t *testing.T) {
	dir := t.TempDir()
	keyStore := NewKeyStore(filepath.Join(dir, "key"))
	keyStore.Save("dummy")
	mgr := NewManager("https://example.invalid", keyStore)

	mgr.cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com", "habrcdn.io"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
		{ID: 3, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: true},
	})

	got := mgr.EnabledDomains()
	// Expected: enabled-only sites' domains, expanded with www./*. variants.
	// Order matches iteration order of cache.Snapshot.
	want := []string{
		"habr.com", "www.habr.com", "*.habr.com",
		"habrcdn.io", "www.habrcdn.io", "*.habrcdn.io",
		"vk.com", "www.vk.com", "*.vk.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
