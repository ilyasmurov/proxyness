package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// writeJSON writes v as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleSitesMatch(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		writeJSON(w, 503, map[string]interface{}{"daemon_running": false})
		return
	}
	host := strings.TrimSpace(r.URL.Query().Get("host"))
	m := s.sitesManager.Cache().Match(host)
	resp := map[string]interface{}{
		"daemon_running": true,
		"in_catalog":     m != nil,
	}
	if m != nil {
		resp["site_id"] = m.ID
		resp["proxy_enabled"] = m.Enabled
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleSitesAdd(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	var req struct {
		PrimaryDomain string `json:"primary_domain"`
		Label         string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	siteID, deduped, err := s.sitesManager.AddSite(req.PrimaryDomain, req.Label)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"site_id": siteID,
		"deduped": deduped,
	})
}

func (s *Server) handleSitesDiscover(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	var req struct {
		SiteID  int      `json:"site_id"`
		Domains []string `json:"domains"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	if req.SiteID == 0 || len(req.Domains) == 0 {
		http.Error(w, "missing site_id or domains", 400)
		return
	}
	added, deduped, err := s.sitesManager.AddDomains(req.SiteID, req.Domains)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"added":   added,
		"deduped": deduped,
	})
}

// handleSitesTest is implemented in Task 8 (needs the SOCKS5 client wrapper).
// Placeholder so the route mounts compile.
func (s *Server) handleSitesTest(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented yet", 501)
}
