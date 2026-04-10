package api

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"smurov-proxy/config/internal/db"
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
	ConfigURL     string            `json:"config_url"`
	ProxyServer   string            `json:"proxy_server"`
	RelayURL      string            `json:"relay_url,omitempty"`
	Notifications []db.Notification `json:"notifications"`
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

	cfg, err := s.db.GetServiceConfig()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	notifs, err := s.db.ActiveNotifications()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if notifs == nil {
		notifs = []db.Notification{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClientConfigResponse{
		ConfigURL:     cfg["config_url"],
		ProxyServer:   cfg["proxy_server"],
		RelayURL:      cfg["relay_url"],
		Notifications: notifs,
	})
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notifs)
}

func (s *Server) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type    string          `json:"type"`
		Title   string          `json:"title"`
		Message string          `json:"message"`
		Action  json.RawMessage `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	n, err := s.db.CreateNotification(req.Type, req.Title, req.Message, req.Action)
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
