package api

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"proxyness/config/internal/db"
)

type Server struct {
	db        *db.DB
	adminUser string
	adminPass string
	proxyAddr string
	keyClient *http.Client
}

func New(d *db.DB, adminUser, adminPass, proxyAddr string) *Server {
	return &Server{
		db:        d,
		adminUser: adminUser,
		adminPass: adminPass,
		proxyAddr: proxyAddr,
		keyClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

type ClientConfigResponse struct {
	ConfigURL   string `json:"config_url"`
	ProxyServer string `json:"proxy_server"`
	RelayURL    string `json:"relay_url,omitempty"`
	// Servers is the exit list the client should dial, in Auto priority
	// order (PRXNS-21). Empty = the client keeps its built-in list. Stored as
	// a JSON string under service_config key "servers", edited from the
	// admin "Servers" page. Lets an exit or bridge change without a release.
	Servers       []ServerEntry     `json:"servers,omitempty"`
	Notifications []db.Notification `json:"notifications"`
}

// ServerEntry mirrors the client's SERVERS shape: a stable id (persisted as
// the user's manual pick), a label for the picker, and host:port to dial.
type ServerEntry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Addr  string `json:"addr"`
	// Kind is "exit" (default) or "bridge" — a port-forwarding box in front
	// of an exit for ISPs that block the exit's subnet (PRXNS-22). Via names
	// the exit a bridge forwards to. Only the topology page reads these;
	// the client dials every entry the same way.
	Kind string `json:"kind,omitempty"`
	Via  string `json:"via,omitempty"`
}

const maxServers = 16

// parseServers validates the admin-supplied list. Empty input is a valid
// "no override". Every entry needs a non-blank id and label and an
// addr of the form host:port; ids and addrs must be unique. A bad list is
// rejected on write so a typo cannot strand every client.
func parseServers(raw string) ([]ServerEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "null" {
		return nil, nil
	}
	var list []ServerEntry
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("servers: not a JSON array: %w", err)
	}
	if len(list) > maxServers {
		return nil, fmt.Errorf("servers: at most %d entries", maxServers)
	}
	seenID, seenAddr := map[string]bool{}, map[string]bool{}
	out := make([]ServerEntry, 0, len(list))
	for i, e := range list {
		e.ID, e.Label, e.Addr = strings.TrimSpace(e.ID), strings.TrimSpace(e.Label), strings.TrimSpace(e.Addr)
		e.Kind, e.Via = strings.TrimSpace(e.Kind), strings.TrimSpace(e.Via)
		if e.ID == "" || e.Label == "" || e.Addr == "" {
			return nil, fmt.Errorf("servers[%d]: id, label and addr are required", i)
		}
		switch e.Kind {
		case "", "exit":
			e.Kind = "exit"
			if e.Via != "" {
				return nil, fmt.Errorf("servers[%d]: via is only for bridges", i)
			}
		case "bridge":
			if e.Via == "" {
				return nil, fmt.Errorf("servers[%d]: a bridge needs via = id of the exit it forwards to", i)
			}
		default:
			return nil, fmt.Errorf("servers[%d]: kind must be exit or bridge, got %q", i, e.Kind)
		}
		host, port, err := net.SplitHostPort(e.Addr)
		if err != nil || host == "" {
			return nil, fmt.Errorf("servers[%d]: addr must be host:port, got %q", i, e.Addr)
		}
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("servers[%d]: bad port in %q", i, e.Addr)
		}
		if seenID[e.ID] || seenAddr[e.Addr] {
			return nil, fmt.Errorf("servers[%d]: duplicate id or addr", i)
		}
		seenID[e.ID], seenAddr[e.Addr] = true, true
		out = append(out, e)
	}
	for i, e := range out {
		if e.Kind == "bridge" {
			target, ok := byID(out, e.Via)
			if !ok || target.Kind != "exit" {
				return nil, fmt.Errorf("servers[%d]: via %q is not an exit in this list", i, e.Via)
			}
		}
	}
	return out, nil
}

func byID(list []ServerEntry, id string) (ServerEntry, bool) {
	for _, e := range list {
		if e.ID == id {
			return e, true
		}
	}
	return ServerEntry{}, false
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public (device key auth)
	mux.HandleFunc("GET /api/client-config", s.handleClientConfig)

	// Admin (Basic Auth)
	mux.HandleFunc("GET /api/admin/notifications", s.requireAdmin(s.handleListNotifications))
	mux.HandleFunc("POST /api/admin/notifications", s.requireAdmin(s.handleCreateNotification))
	mux.HandleFunc("DELETE /api/admin/notifications/{id}", s.requireAdmin(s.handleDeleteNotification))
	mux.HandleFunc("PATCH /api/admin/notifications/{id}", s.requireAdmin(s.handleUpdateNotification))
	mux.HandleFunc("GET /api/admin/notifications/{id}/deliveries", s.requireAdmin(s.handleGetDeliveries))
	mux.HandleFunc("GET /api/admin/services", s.requireAdmin(s.handleGetServices))
	mux.HandleFunc("PUT /api/admin/services", s.requireAdmin(s.handleSetServices))

	// Admin UI
	mux.HandleFunc("GET /", s.handleAdminUI)

	return withCORS(mux)
}

func (s *Server) validateKey(key string) bool {
	if key == "" {
		return false
	}
	resp, err := s.keyClient.Get(fmt.Sprintf("%s/api/validate-key?key=%s", s.proxyAddr, key))
	if err != nil {
		log.Printf("[config] validate-key error: %v", err)
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if !s.validateKey(key) {
		http.Error(w, "invalid key", http.StatusForbidden)
		return
	}

	firstSeen, err := s.db.RecordDeviceSeen(key)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	cfg, err := s.db.GetServiceConfig()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	allNotifs, err := s.db.FilteredNotifications(firstSeen)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	clientVersion := r.URL.Query().Get("v")
	isBeta := strings.Contains(clientVersion, "beta")
	notifs := make([]db.Notification, 0, len(allNotifs))
	for _, n := range allNotifs {
		if n.BetaOnly && !isBeta {
			continue
		}
		notifs = append(notifs, n)
	}

	servers, err := parseServers(cfg["servers"])
	if err != nil {
		// Validated on write, so this only happens for a hand-edited DB;
		// serve the rest of the config rather than nothing.
		log.Printf("[config] stored servers list is invalid, omitting: %v", err)
		servers = nil
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClientConfigResponse{
		ConfigURL:     cfg["config_url"],
		ProxyServer:   cfg["proxy_server"],
		RelayURL:      cfg["relay_url"],
		Servers:       servers,
		Notifications: notifs,
	})

	if len(notifs) > 0 {
		ids := make([]string, len(notifs))
		for i, n := range notifs {
			ids[i] = n.ID
		}
		go s.db.RecordDeliveries(ids, key)
	}
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminUser == "" {
			next(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || u != s.adminUser || p != s.adminPass {
			w.Header().Set("WWW-Authenticate", `Basic realm="config admin"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	notifs, err := s.db.ListNotifications()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if notifs == nil {
		notifs = []db.Notification{}
	}

	type NotifWithCount struct {
		db.Notification
		DeliveryCount int `json:"delivery_count"`
	}
	out := make([]NotifWithCount, len(notifs))
	for i, n := range notifs {
		out[i] = NotifWithCount{Notification: n, DeliveryCount: s.db.DeliveryCount(n.ID)}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type      string          `json:"type"`
		Title     string          `json:"title"`
		Message   string          `json:"message"`
		Action    json.RawMessage `json:"action"`
		BetaOnly  bool            `json:"beta_only"`
		ExpiresAt string          `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ExpiresAt == "" {
		req.ExpiresAt = time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	n, err := s.db.CreateNotification(req.Type, req.Title, req.Message, req.Action, req.BetaOnly, req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(n)
}

func (s *Server) handleDeleteNotification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.db.DeleteNotification(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateNotification(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Active  *bool   `json:"active"`
		Title   *string `json:"title"`
		Message *string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.db.UpdateNotification(id, req.Active, req.Title, req.Message); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetDeliveries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deliveries, err := s.db.GetDeliveries(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if deliveries == nil {
		deliveries = []db.Delivery{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}

func (s *Server) handleGetServices(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.db.GetServiceConfig()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func (s *Server) handleSetServices(w http.ResponseWriter, r *http.Request) {
	var req map[string]string
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	for k, v := range req {
		if k == "egress_proxy" {
			v = strings.TrimSpace(v)
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					http.Error(w, "egress_proxy must be an http(s)://host:port URL or empty", http.StatusBadRequest)
					return
				}
			}
		}
		if k == "servers" {
			list, err := parseServers(v)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			// store the canonical form so the client sees exactly what was validated
			if list == nil {
				v = ""
			} else {
				b, _ := json.Marshal(list)
				v = string(b)
			}
		}
		if err := s.db.SetServiceConfig(k, v); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
