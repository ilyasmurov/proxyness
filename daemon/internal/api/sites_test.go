package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/sites"
	"smurov-proxy/daemon/internal/tun"
	"smurov-proxy/daemon/internal/tunnel"
)

func newTestServerWithSitesAPI(t *testing.T, mgr *sites.Manager, tokenStore *sites.TokenStore) *Server {
	s := &Server{
		sitesManager: mgr,
		tokenStore:   tokenStore,
	}
	return s
}

func TestHandleSitesMatch(t *testing.T) {
	mgr := sites.NewManager("http://unused", sites.NewKeyStore(filepath.Join(t.TempDir(), "k")))
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	r := httptest.NewRequest("GET", "/sites/match?host=habr.com", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesMatch)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["in_catalog"] != true {
		t.Fatalf("expected in_catalog=true, got %+v", resp)
	}
	if resp["site_id"].(float64) != 1 {
		t.Fatalf("expected site_id=1, got %+v", resp["site_id"])
	}
	if resp["proxy_enabled"] != true {
		t.Fatalf("expected proxy_enabled=true")
	}
}

func TestHandleSitesMatchNotInCatalog(t *testing.T) {
	mgr := sites.NewManager("http://unused", sites.NewKeyStore(filepath.Join(t.TempDir(), "k")))
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	r := httptest.NewRequest("GET", "/sites/match?host=wikipedia.org", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesMatch)).ServeHTTP(w, r)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["in_catalog"] != false {
		t.Fatalf("expected in_catalog=false")
	}
}

func TestHandleSitesTestConfirmsBlock(t *testing.T) {
	// Stub the "real" upstream that responds 200 (simulating a successful
	// proxied fetch).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()

	// Use the default http.Client which dials directly — simulates the
	// behavior of a working proxy without actually going through SOCKS5.
	// This is sufficient to exercise the handler logic.
	s := &Server{
		tokenStore:      store,
		sitesTestClient: &http.Client{},
	}

	body, _ := json.Marshal(map[string]string{"url": upstream.URL})
	r := httptest.NewRequest("POST", "/sites/test", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesTest)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["likely_blocked"] != true {
		t.Fatalf("expected likely_blocked=true, got %+v", resp)
	}
}

func TestSitesSetEnabledHappyPath(t *testing.T) {
	// Fake server that flips a site to disabled.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sites.SyncResponse{
			MySites: []sites.MySite{
				{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
			},
			OpResults:  []sites.OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer upstream.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager(upstream.URL, keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":47,"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	mySites, ok := resp["my_sites"].([]interface{})
	if !ok || len(mySites) != 1 {
		t.Fatalf("expected my_sites with 1 entry, got %v", resp["my_sites"])
	}
}

func TestSitesSetEnabledMissingSiteID(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSitesSetEnabledRequiresAuth(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":1,"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	// no Authorization header
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestSitesRemoveHappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sites.SyncResponse{
			MySites:    []sites.MySite{}, // site removed
			OpResults:  []sites.OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer upstream.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager(upstream.URL, keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":47}`)
	req := httptest.NewRequest("POST", "/sites/remove", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.Cache().Match("youtube.com") != nil {
		t.Error("expected cache to be empty after remove")
	}
}

func TestSitesMyReturnsCacheSnapshot(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: false},
	})
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	req := httptest.NewRequest("GET", "/sites/my", nil)
	// NO Authorization header — endpoint is not auth-gated.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	mySites, ok := resp["my_sites"].([]interface{})
	if !ok || len(mySites) != 2 {
		t.Fatalf("expected 2 sites, got %v", resp["my_sites"])
	}
}

func TestSitesMyReturns503WhenManagerNil(t *testing.T) {
	// Build a server with no sites manager — must return 503.
	// Use the same pattern as newTestServerWithMgr but without SetSites.
	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	srv := New(tnl, tun.NewEngine(meter), "127.0.0.1:0", meter)
	// Intentionally no SetSites call — sitesManager stays nil.

	req := httptest.NewRequest("GET", "/sites/my", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandleSitesAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"op_results": []map[string]interface{}{{"site_id": 99, "status": "ok", "deduped": false}},
			"my_sites": []map[string]interface{}{
				{"id": 99, "primary_domain": "habr.com", "domains": []string{"habr.com"}, "enabled": true},
			},
			"server_time": 1000,
		})
	}))
	defer srv.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := sites.NewManager(srv.URL, keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	body, _ := json.Marshal(map[string]string{"primary_domain": "habr.com", "label": "Habr"})
	r := httptest.NewRequest("POST", "/sites/add", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesAdd)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["site_id"].(float64) != 99 {
		t.Fatalf("expected site_id=99, got %+v", resp)
	}
}
