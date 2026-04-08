package api

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/sites"
	"smurov-proxy/daemon/internal/transport"
	"smurov-proxy/daemon/internal/tun"
	"smurov-proxy/daemon/internal/tunnel"
	"smurov-proxy/pkg/machineid"
)

type Server struct {
	mu              sync.Mutex
	tunnel          *tunnel.Tunnel
	tunEngine       *tun.Engine
	listenAddr      string
	meter           *dstats.RateMeter
	sessionID       string
	serverAddr      string              // remembered for unlock on disconnect
	key             string
	keyStore        *sites.KeyStore
	sitesManager    *sites.Manager
	tokenStore      *sites.TokenStore
	pacSites        *PacSites
	transportMode   string              // "auto", "udp", or "tls"
	activeTransport transport.Transport  // current transport instance
	sitesTestClient *http.Client        // dials through the local SOCKS5 tunnel
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
	return &Server{
		tunnel:        t,
		tunEngine:     te,
		listenAddr:    listenAddr,
		meter:         meter,
		sessionID:     hex.EncodeToString(b),
		pacSites:      NewPacSites(),
		transportMode: transport.ModeAuto,
	}
}

// SetKeyStore wires the KeyStore and loads the persisted device key, if any,
// so /sites/* endpoints (added in later tasks) can reach the server even
// before the user explicitly connects the tunnel.
func (s *Server) SetKeyStore(ks *sites.KeyStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyStore = ks
	// Restore key from disk on startup.
	if s.key == "" {
		s.key = ks.Load()
	}
}

// SetSites wires the sites manager and token store. Called once at startup
// from daemon main.
func (s *Server) SetSites(mgr *sites.Manager, tokenStore *sites.TokenStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sitesManager = mgr
	s.tokenStore = tokenStore
}

// SetSitesTestClient wires the http.Client used by /sites/test. Built in
// daemon main to dial through the local SOCKS5 tunnel.
func (s *Server) SetSitesTestClient(c *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sitesTestClient = c
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
	// Read-only sites endpoint for local desktop client renderer (no auth).
	mux.HandleFunc("GET /sites/my", s.handleSitesMy)
	mux.HandleFunc("GET /tunnel/active-hosts", s.handleActiveHosts)
	// TUN endpoints
	mux.HandleFunc("POST /tun/start", s.handleTUNStart)
	mux.HandleFunc("POST /tun/stop", s.handleTUNStop)
	mux.HandleFunc("GET /tun/status", s.handleTUNStatus)
	mux.HandleFunc("POST /tun/rules", s.handleTUNRulesUpdate)
	mux.HandleFunc("GET /tun/rules", s.handleTUNRulesGet)
	mux.HandleFunc("GET /stats", s.handleStats)
	// Transport endpoints
	mux.HandleFunc("GET /transport", s.handleTransportGet)
	mux.HandleFunc("POST /transport", s.handleTransportSet)
	// Sites API for browser extension. All require Authorization: Bearer <token>.
	if s.tokenStore != nil {
		mux.Handle("GET /sites/match", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesMatch)))
		mux.Handle("POST /sites/add", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesAdd)))
		mux.Handle("POST /sites/discover", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesDiscover)))
		mux.Handle("POST /sites/test", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesTest)))
		mux.Handle("POST /sites/set-enabled", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesSetEnabled)))
		mux.Handle("OPTIONS /sites/set-enabled", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesSetEnabled)))
		mux.Handle("POST /sites/remove", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesRemove)))
		mux.Handle("OPTIONS /sites/remove", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesRemove)))
		mux.Handle("OPTIONS /sites/match", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesMatch)))
		mux.Handle("OPTIONS /sites/add", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesAdd)))
		mux.Handle("OPTIONS /sites/discover", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesDiscover)))
		mux.Handle("OPTIONS /sites/test", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesTest)))
	}
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

	// Create and connect transport
	s.mu.Lock()
	tr := s.createTransport()
	s.mu.Unlock()

	fp := machineid.Fingerprint()
	if err := tr.Connect(req.ServerAddr, req.Key, fp); err != nil {
		go unlockDevice(req.ServerAddr, req.Key, s.sessionID)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.activeTransport = tr
	s.mu.Unlock()

	s.tunnel.SetTransport(tr)
	s.tunnel.SetTransportFactory(func() transport.Transport {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.createTransport()
	}, fp)
	log.Printf("[api] transport connected: mode=%s", tr.Mode())

	if err := s.tunnel.Start(s.listenAddr, req.ServerAddr, req.Key); err != nil {
		tr.Close()
		s.mu.Lock()
		s.activeTransport = nil
		s.mu.Unlock()
		s.tunnel.SetTransport(nil)
		go unlockDevice(req.ServerAddr, req.Key, s.sessionID)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.serverAddr = req.ServerAddr
	s.key = req.Key
	if s.keyStore != nil {
		if err := s.keyStore.Save(req.Key); err != nil {
			log.Printf("[sites] failed to persist device key: %v", err)
		}
	}
	if s.sitesManager != nil {
		s.sitesManager.SetKey(req.Key)
		// Best-effort initial refresh; OK to fail (offline first connect).
		go func() {
			if err := s.sitesManager.Refresh(); err != nil {
				log.Printf("[sites] initial refresh: %v", err)
			}
		}()
	}
	s.mu.Unlock()

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
	s.tunnel.Stop() // also closes tunnel's transport ref

	s.mu.Lock()
	s.activeTransport = nil
	addr, key := s.serverAddr, s.key
	s.mu.Unlock()

	if addr != "" && key != "" {
		go unlockDevice(addr, key, s.sessionID)
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

// handleActiveHosts returns a snapshot of the hosts with at least one
// in-flight SOCKS5 connection. Used by the UI to light up LIVE indicators
// on browser-site tiles.
func (s *Server) handleActiveHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	hosts := s.tunnel.GetActiveHosts()
	json.NewEncoder(w).Encode(map[string]any{
		"hosts": hosts,
	})
}

func (s *Server) handleTUNStart(w http.ResponseWriter, r *http.Request) {
	var req tun.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Create transport for the TUN engine if not already active
	s.mu.Lock()
	tr := s.createTransport()
	s.mu.Unlock()
	fp := machineid.Fingerprint()
	if err := tr.Connect(req.ServerAddr, req.Key, fp); err != nil {
		http.Error(w, fmt.Sprintf("transport connect: %v", err), http.StatusInternalServerError)
		return
	}
	s.tunEngine.SetTransport(tr)
	s.tunEngine.SetTransportFactory(func() transport.Transport {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.createTransport()
	}, fp)
	log.Printf("[api] TUN transport connected: mode=%s", tr.Mode())

	if err := s.tunEngine.Start(req); err != nil {
		tr.Close()
		s.tunEngine.SetTransport(nil)
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

func (s *Server) createTransport() transport.Transport {
	switch s.transportMode {
	case transport.ModeUDP:
		return transport.NewUDPTransport()
	case transport.ModeTLS:
		return transport.NewTLSTransport()
	default: // ModeAuto
		return transport.NewAutoTransport()
	}
}

func (s *Server) handleTransportGet(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	mode := s.transportMode
	active := mode
	if s.activeTransport != nil {
		active = s.activeTransport.Mode()
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"mode":   mode,
		"active": active,
	})
}

func (s *Server) handleTransportSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch req.Mode {
	case transport.ModeAuto, transport.ModeUDP, transport.ModeTLS:
		s.mu.Lock()
		s.transportMode = req.Mode
		s.mu.Unlock()
	default:
		http.Error(w, fmt.Sprintf("invalid mode: %q (expected auto, udp, or tls)", req.Mode), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// RebuildPAC refreshes pacSites from the sitesManager cache, preserving
// the current proxy_all flag (which is owned by the renderer's UI toggle
// and pushed via the existing /pac/sites endpoint).
//
// IMPORTANT — diff before CloseAllConns:
//
// This function gets called from background Refresh() every 5 minutes
// even when nothing changed. Without diffing, every tick would kill all
// in-flight SOCKS5 connections, giving users mysterious 5-minute
// connection resets. So we compare the new domain list against the
// previous one and only call CloseAllConns when something actually
// changed.
func (s *Server) RebuildPAC() {
	if s.sitesManager == nil {
		return
	}
	prevProxyAll, prevDomains := s.pacSites.Get()

	var newProxyAll bool
	var newDomains []string
	if prevProxyAll {
		newProxyAll = true
		newDomains = nil
	} else {
		newProxyAll = false
		newDomains = s.sitesManager.EnabledDomains()
	}

	changed := newProxyAll != prevProxyAll || !slicesEqual(prevDomains, newDomains)
	if !changed {
		return
	}

	s.pacSites.Set(newProxyAll, newDomains)
	s.tunnel.CloseAllConns()
}

// slicesEqual checks if two string slices have the same elements in the
// same order. Cheap because the lists are small (low hundreds even for
// power users).
func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
