package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"smurov-proxy/daemon/internal/sites"
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
