package api

import (
	"crypto/tls"
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
	return &Server{tunnel: t, tunEngine: te, listenAddr: listenAddr, meter: meter}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /proxy.pac", s.handlePAC)
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

	if err := s.tunnel.Start(s.listenAddr, req.ServerAddr, req.Key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Version != "" {
		go reportVersion(req.ServerAddr, req.Key, req.Version)
	}

	w.WriteHeader(http.StatusOK)
}

func reportVersion(serverAddr, key, version string) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}
	data := fmt.Sprintf(`{"key":%q,"version":%q}`, key, version)
	client.Post("https://"+serverAddr+"/api/report-version", "application/json", strings.NewReader(data))
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.tunnel.Stop()
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
	w.Write([]byte(`function FindProxyForURL(url, host) {
  if (host === "127.0.0.1" || host === "localhost") return "DIRECT";
  return "SOCKS5 127.0.0.1:1080; SOCKS 127.0.0.1:1080; DIRECT";
}`))
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
	json.NewEncoder(w).Encode(map[string]string{
		"status": string(s.tunEngine.GetStatus()),
	})
}

func (s *Server) handleTUNRulesUpdate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.tunEngine.GetRules().FromJSON(body); err != nil {
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
