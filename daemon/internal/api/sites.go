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

func (s *Server) handleSitesSetEnabled(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	var req struct {
		SiteID  int  `json:"site_id"`
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	if req.SiteID == 0 {
		http.Error(w, "missing site_id", 400)
		return
	}
	if err := s.sitesManager.SetEnabled(req.SiteID, req.Enabled); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"ok":       true,
		"my_sites": s.sitesManager.Cache().Snapshot(),
	})
}

func (s *Server) handleSitesRemove(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	var req struct {
		SiteID int `json:"site_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	if req.SiteID == 0 {
		http.Error(w, "missing site_id", 400)
		return
	}
	if err := s.sitesManager.RemoveSite(req.SiteID); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"ok":       true,
		"my_sites": s.sitesManager.Cache().Snapshot(),
	})
}

// handleSitesMy returns the cached my_sites snapshot. No auth — this is
// localhost-only and read-only, used by the desktop client renderer to
// pull the authoritative sites list from the daemon (which is the single
// source of truth after the popup-control-panel refactor).
func (s *Server) handleSitesMy(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"my_sites": s.sitesManager.Cache().Snapshot(),
	})
}

func (s *Server) handleSitesSearch(w http.ResponseWriter, r *http.Request) {
	if s.sitesManager == nil {
		http.Error(w, "daemon not ready", 503)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, []interface{}{})
		return
	}
	ql := strings.ToLower(q)

	type result struct {
		ID            int    `json:"id"`
		Label         string `json:"label"`
		PrimaryDomain string `json:"primary_domain"`
	}

	// 1. Search local cache (instant, always available).
	seen := map[int]bool{}
	var out []result
	for _, site := range s.sitesManager.Cache().Snapshot() {
		if strings.Contains(strings.ToLower(site.Label), ql) ||
			strings.Contains(strings.ToLower(site.PrimaryDomain), ql) {
			out = append(out, result{site.ID, site.Label, site.PrimaryDomain})
			seen[site.ID] = true
		}
	}

	// 2. Try server catalog (may fail if server not deployed yet).
	if remote, err := s.sitesManager.SearchCatalog(q); err == nil {
		for _, r := range remote {
			if !seen[r.ID] {
				out = append(out, result{r.ID, r.Label, r.PrimaryDomain})
				seen[r.ID] = true
			}
		}
	}

	if out == nil {
		out = []result{}
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleSitesTest(w http.ResponseWriter, r *http.Request) {
	if s.sitesTestClient == nil {
		http.Error(w, "test client not configured", 503)
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", 400)
		return
	}
	if req.URL == "" {
		http.Error(w, "missing url", 400)
		return
	}

	httpReq, err := http.NewRequest("HEAD", req.URL, nil)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"likely_blocked": false})
		return
	}
	httpReq.Header.Set("User-Agent", "Proxyness-Discovery/1.0")

	resp, err := s.sitesTestClient.Do(httpReq)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"likely_blocked": false})
		return
	}
	defer resp.Body.Close()

	// 2xx/3xx via the tunnel = block confirmed (since the direct request
	// failed before the extension called us).
	likely := resp.StatusCode < 400
	writeJSON(w, 200, map[string]interface{}{
		"likely_blocked": likely,
		"status_code":    resp.StatusCode,
	})
}
