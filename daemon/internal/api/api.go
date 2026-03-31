package api

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/tun"
	"smurov-proxy/daemon/internal/tunnel"
)

type Server struct {
	tunnel     *tunnel.Tunnel
	tunEngine  *tun.Engine
	listenAddr string
	meter      *dstats.RateMeter
	sessionID  string
	serverAddr string // remembered for unlock on disconnect
	key        string
	pacSites   *PacSites
}

type ConnectRequest struct {
	ServerAddr string `json:"server"`
	Key        string `json:"key"`
	Version    string `json:"version,omitempty"`
}

type StatusResponse struct {
	Status string `json:"status"`
	Uptime int64  `json:"uptime"`
	Error  string `json:"error,omitempty"`
}

func New(t *tunnel.Tunnel, te *tun.Engine, listenAddr string, meter *dstats.RateMeter) *Server {
	b := make([]byte, 16)
	rand.Read(b)
	return &Server{tunnel: t, tunEngine: te, listenAddr: listenAddr, meter: meter, sessionID: hex.EncodeToString(b), pacSites: NewPacSites()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /proxy.pac", s.handlePAC)
	mux.HandleFunc("POST /pac/sites", s.handlePacSitesUpdate)
	mux.HandleFunc("GET /pac/sites", s.handlePacSitesGet)
	// TUN endpoints
	mux.HandleFunc("POST /tun/start", s.handleTUNStart)
	mux.HandleFunc("POST /tun/stop", s.handleTUNStop)
	mux.HandleFunc("GET /tun/status", s.handleTUNStatus)
	mux.HandleFunc("POST /tun/rules", s.handleTUNRulesUpdate)
	mux.HandleFunc("GET /tun/rules", s.handleTUNRulesGet)
	mux.HandleFunc("GET /stats", s.handleStats)
	return mux
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Lock device on server before connecting
	if err := lockDevice(req.ServerAddr, req.Key, s.sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	if err := s.tunnel.Start(s.listenAddr, req.ServerAddr, req.Key); err != nil {
		go unlockDevice(req.ServerAddr, req.Key, s.sessionID)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.serverAddr = req.ServerAddr
	s.key = req.Key

	if req.Version != "" {
		go reportVersion(req.ServerAddr, req.Key, req.Version)
	}

	w.WriteHeader(http.StatusOK)
}

func serverHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
}

func lockDevice(serverAddr, key, sessionID string) error {
	data := fmt.Sprintf(`{"key":%q,"session_id":%q}`, key, sessionID)
	resp, err := serverHTTPClient().Post("https://"+serverAddr+"/api/lock-device", "application/json", strings.NewReader(data))
	if err != nil {
		return nil // server unreachable, allow connection anyway
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		var body struct{ Error string `json:"error"` }
		json.NewDecoder(resp.Body).Decode(&body)
		if body.Error != "" {
			return fmt.Errorf("%s", body.Error)
		}
		return fmt.Errorf("device already in use")
	}
	return nil
}

func unlockDevice(serverAddr, key, sessionID string) {
	data := fmt.Sprintf(`{"key":%q,"session_id":%q}`, key, sessionID)
	serverHTTPClient().Post("https://"+serverAddr+"/api/unlock-device", "application/json", strings.NewReader(data))
}

func reportVersion(serverAddr, key, version string) {
	data := fmt.Sprintf(`{"key":%q,"version":%q}`, key, version)
	serverHTTPClient().Post("https://"+serverAddr+"/api/report-version", "application/json", strings.NewReader(data))
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.tunnel.Stop()
	if s.serverAddr != "" && s.key != "" {
		go unlockDevice(s.serverAddr, s.key, s.sessionID)
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusResponse{
		Status: string(s.tunnel.GetStatus()),
		Uptime: s.tunnel.Uptime(),
		Error:  s.tunnel.LastError(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePAC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write([]byte(s.pacSites.GeneratePAC()))
}

func (s *Server) handlePacSitesUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyAll bool     `json:"proxy_all"`
		Sites    []string `json:"sites"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.pacSites.Set(req.ProxyAll, req.Sites)
	// Close existing SOCKS5 connections so browsers reconnect with updated PAC
	s.tunnel.CloseAllConns()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePacSitesGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	proxyAll, sites := s.pacSites.Get()
	json.NewEncoder(w).Encode(map[string]any{
		"proxy_all": proxyAll,
		"sites":     sites,
	})
}

func (s *Server) handleTUNStart(w http.ResponseWriter, r *http.Request) {
	var req tun.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.tunEngine.Start(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNStop(w http.ResponseWriter, r *http.Request) {
	if err := s.tunEngine.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{
		"status": string(s.tunEngine.GetStatus()),
		"uptime": s.tunEngine.GetUptime(),
	}
	if e := s.tunEngine.GetLastError(); e != "" {
		resp["error"] = e
	}
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleTUNRulesUpdate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.tunEngine.UpdateRules(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNRulesGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(s.tunEngine.GetRules().ToJSON())
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.meter.Snapshot())
}
