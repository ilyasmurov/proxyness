package api

import (
	"encoding/json"
	"net/http"

	"smurov-proxy/daemon/internal/tunnel"
)

type Server struct {
	tunnel     *tunnel.Tunnel
	listenAddr string
}

type ConnectRequest struct {
	ServerAddr string `json:"server"`
	Key        string `json:"key"`
}

type StatusResponse struct {
	Status string `json:"status"`
	Uptime int64  `json:"uptime"`
}

func New(t *tunnel.Tunnel, listenAddr string) *Server {
	return &Server{tunnel: t, listenAddr: listenAddr}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /proxy.pac", s.handlePAC)
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

	w.WriteHeader(http.StatusOK)
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
