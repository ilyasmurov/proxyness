package api

import (
	"net/http"
	"strings"

	"proxyness/daemon/internal/sites"
)

// requireExtensionToken wraps a handler with a constant-time bearer-token
// check against the provided TokenStore. Used only on /sites/* routes.
func requireExtensionToken(store *sites.TokenStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight from the extension origin: allow without token.
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(auth[len(prefix):])
		if !store.Check(token) {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}

		// Allow the actual handler to add CORS headers on the response too.
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		next.ServeHTTP(w, r)
	})
}
