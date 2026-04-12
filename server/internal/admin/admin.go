package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

// Handler holds the admin HTTP mux and its dependencies.
type Handler struct {
	db           *db.DB
	tracker      *stats.Tracker
	user         string
	password     string
	downloadsDir string
	deviceAuth   *DeviceAuth
	mux          *http.ServeMux
}

// NewHandler creates and wires up the admin HTTP handler.
func NewHandler(d *db.DB, tr *stats.Tracker, user, password, downloadsDir, configAddr string) *Handler {
	h := &Handler{db: d, tracker: tr, user: user, password: password, downloadsDir: downloadsDir}
	h.deviceAuth = NewDeviceAuth(d)
	mux := http.NewServeMux()

	mux.HandleFunc("GET /admin/api/users", h.auth(h.listUsers))
	mux.HandleFunc("POST /admin/api/users", h.auth(h.createUser))
	mux.HandleFunc("DELETE /admin/api/users/{id}", h.auth(h.deleteUser))
	mux.HandleFunc("GET /admin/api/users/{id}/devices", h.auth(h.listDevices))
	mux.HandleFunc("POST /admin/api/users/{id}/devices", h.auth(h.createDevice))
	mux.HandleFunc("PATCH /admin/api/devices/{id}", h.auth(h.toggleDevice))
	mux.HandleFunc("DELETE /admin/api/devices/{id}", h.auth(h.deleteDevice))
	mux.HandleFunc("GET /admin/api/stats/overview", h.auth(h.statsOverview))
	mux.HandleFunc("GET /admin/api/stats/active", h.auth(h.statsActive))
	mux.HandleFunc("GET /admin/api/stats/traffic", h.auth(h.statsTraffic))
	mux.HandleFunc("GET /admin/api/stats/traffic/{deviceId}/daily", h.auth(h.statsTrafficDaily))
	mux.HandleFunc("GET /admin/api/stats/rate", h.auth(h.statsRate))
	mux.HandleFunc("GET /admin/api/sites", h.auth(h.listSites))
	mux.HandleFunc("GET /admin/api/sites/{id}", h.auth(h.getSite))
	mux.HandleFunc("DELETE /admin/api/sites/{id}", h.auth(h.deleteSite))
	mux.HandleFunc("DELETE /admin/api/sites/{id}/domains/{domain}", h.auth(h.deleteSiteDomain))
	mux.HandleFunc("GET /admin/api/changelog", h.auth(h.listChangelog))
	mux.HandleFunc("GET /admin/api/changelog/unseen-count", h.auth(h.changelogUnseenCount))
	mux.HandleFunc("GET /admin/api/logs", h.auth(h.listLogs))

	// Public endpoints (no auth, device key for identification)
	mux.HandleFunc("POST /api/report-version", h.reportVersion)
	mux.HandleFunc("POST /api/lock-device", h.lockDevice)
	mux.HandleFunc("POST /api/unlock-device", h.unlockDevice)
	mux.HandleFunc("POST /api/sync", h.deviceAuth.Wrap(h.handleSync))
	mux.HandleFunc("GET /api/sites/search", h.deviceAuth.Wrap(h.searchCatalog))

	// Internal: config service validates device keys through this endpoint
	mux.HandleFunc("GET /api/validate-key", h.handleValidateKey)

	// Reverse proxy: forward config service endpoints to config container
	if configAddr == "" {
		configAddr = "http://127.0.0.1:8443"
	}
	configTarget, _ := url.Parse(configAddr)
	configProxy := httputil.NewSingleHostReverseProxy(configTarget)
	mux.Handle("GET /api/client-config", configProxy)
	mux.Handle("/api/admin/notifications", h.authHandler(configProxy))
	mux.Handle("/api/admin/notifications/", h.authHandler(configProxy))
	mux.Handle("/api/admin/services", h.authHandler(configProxy))

	// Download files
	mux.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir(downloadsDir))))

	// SPA static files (auth required)
	mux.Handle("/admin/", h.authHandler(SPAHandler()))

	// Landing page (redirect to standalone landing container on port 80)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://proxyness.smurov.com", http.StatusMovedPermanently)
	})

	h.mux = mux
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleValidateKey checks if a device key exists in the DB.
// Called internally by the config container over loopback.
func (h *Handler) handleValidateKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	_, err := h.db.GetDeviceByKey(key)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]bool{"valid": false})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"valid": true})
}

// auth wraps a HandlerFunc with HTTP Basic Auth.
func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != h.user || pass != h.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// authHandler wraps a Handler with HTTP Basic Auth.
func (h *Handler) authHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != h.user || pass != h.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// writeJSON writes a JSON response with Content-Type application/json.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// pathID parses a path value as int, writing 400 on error and returning ok=false.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	s := r.PathValue(name)
	id, err := strconv.Atoi(s)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

// ---- Changelog ----

func (h *Handler) listChangelog(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage < 1 || perPage > 50 {
		perPage = 10
	}

	entries, total, err := h.db.GetChangelog(page, perPage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
		"page":    page,
		"pages":   (total + perPage - 1) / perPage,
	})
}

func (h *Handler) changelogUnseenCount(w http.ResponseWriter, r *http.Request) {
	since := r.URL.Query().Get("since")
	count, err := h.db.GetChangelogUnseenCount(since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

// ---- Users ----

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListUsers()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []db.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	user, err := h.db.CreateUser(strings.TrimSpace(body.Name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteUser(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Devices ----

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	devs, err := h.db.ListDevices(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if devs == nil {
		devs = []db.Device{}
	}
	writeJSON(w, http.StatusOK, devs)
}

func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	dev, err := h.db.CreateDevice(userID, strings.TrimSpace(body.Name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, dev)
}

func (h *Handler) toggleDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body struct {
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := h.db.SetDeviceActive(id, body.Active); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteDevice(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Stats ----

func (h *Handler) statsOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := h.db.GetOverview()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ov.ActiveConnections = h.tracker.ActiveCount()
	writeJSON(w, http.StatusOK, ov)
}

func (h *Handler) statsActive(w http.ResponseWriter, r *http.Request) {
	conns := h.tracker.Active()
	if conns == nil {
		conns = []stats.ConnInfo{}
	}
	writeJSON(w, http.StatusOK, conns)
}

func (h *Handler) statsTraffic(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}
	traffic, err := h.db.GetTraffic(period)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if traffic == nil {
		traffic = []db.TrafficStat{}
	}
	writeJSON(w, http.StatusOK, traffic)
}

func (h *Handler) statsRate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.tracker.Rates())
}

func (h *Handler) statsTrafficDaily(w http.ResponseWriter, r *http.Request) {
	deviceID, ok := pathID(w, r, "deviceId")
	if !ok {
		return
	}
	days := 30
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			days = n
		}
	}
	data, err := h.db.GetTrafficByDay(deviceID, days)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		data = []map[string]interface{}{}
	}
	writeJSON(w, http.StatusOK, data)
}

// ---- Logs ----

func (h *Handler) listLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	level := r.URL.Query().Get("level")

	entries, total, err := h.db.GetLogs(limit, offset, level)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []db.LogEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"total":   total,
	})
}

// ---- Sites ----

// searchCatalog handles GET /api/sites/search?q=... (device auth).
func (h *Handler) searchCatalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusOK, []db.CatalogSite{})
		return
	}
	sites, err := h.db.SearchCatalog(q, 20)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []db.CatalogSite{}
	}
	writeJSON(w, http.StatusOK, sites)
}

func (h *Handler) listSites(w http.ResponseWriter, r *http.Request) {
	sites, err := h.db.ListSitesWithStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sites == nil {
		sites = []db.SiteWithStats{}
	}
	writeJSON(w, http.StatusOK, sites)
}

func (h *Handler) getSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	detail, err := h.db.GetSiteDetail(id)
	if err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *Handler) deleteSite(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.DeleteSite(id); err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteSiteDomain(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rawDomain := r.PathValue("domain")
	domain, err := url.PathUnescape(rawDomain)
	if err != nil || domain == "" {
		http.Error(w, "bad domain", http.StatusBadRequest)
		return
	}
	if err := h.db.DeleteSiteDomain(id, domain); err == sql.ErrNoRows {
		http.Error(w, "not found", http.StatusNotFound)
		return
	} else if err != nil {
		// Includes the "cannot delete primary domain" case.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reportVersion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key     string `json:"key"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Key == "" || req.Version == "" {
		http.Error(w, "key and version required", http.StatusBadRequest)
		return
	}
	h.db.UpdateDeviceVersion(req.Key, req.Version)
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) lockDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key       string `json:"key"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Key == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	_, err := h.db.GetDeviceByKey(req.Key)
	if err != nil {
		http.Error(w, "unknown device", http.StatusNotFound)
		return
	}
	// Machine binding is handled by hardware fingerprint in binary protocol.
	// This endpoint only validates the key exists.
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) unlockDevice(w http.ResponseWriter, r *http.Request) {
	// No-op: machine binding is permanent and managed by hardware fingerprint.
	w.WriteHeader(http.StatusOK)
}

