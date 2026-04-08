package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"smurov-proxy/daemon/internal/sites"
)

func TestRequireExtensionTokenAllowsValid(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()

	called := false
	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 || !called {
		t.Fatalf("expected handler to run, code=%d called=%v", w.Code, called)
	}
}

func TestRequireExtensionTokenRejectsMissing(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()

	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequireExtensionTokenRejectsWrong(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()

	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	r.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
