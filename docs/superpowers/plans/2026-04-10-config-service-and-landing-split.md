# Config Service + Landing Split — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract notifications + service discovery into a standalone `smurov-config` microservice, split the landing page into its own nginx container, and wire clients to poll the new config API instead of GitHub directly.

**Architecture:** Three containers (proxy, config, landing) on the same VPS. Config service has its own SQLite DB for notifications + service URLs. Client polls `/api/client-config` through the proxy (which reverse-proxies to config container). Landing is pure static nginx. Proxy keeps admin panel + relay logic.

**Tech Stack:** Go 1.26, SQLite, Docker, Electron/React (client), nginx (landing)

---

## File Structure

### New files

```
config/                           # New Go module
  cmd/main.go                     # Entry point
  go.mod
  go.sum
  internal/
    db/db.go                      # SQLite: notifications + service_config tables
    api/api.go                    # HTTP handlers (public + admin)
    api/admin.go                  # Admin UI (embedded HTML)
    poller/version.go             # Background GitHub version checker
  Dockerfile

landing/                          # Extracted from server/internal/admin/landing.go
  index.html                      # Landing page HTML/CSS/JS
  nginx.conf                      # nginx config
  Dockerfile

.github/workflows/
  deploy-config.yml               # CI/CD for config container
  deploy-landing.yml              # CI/CD for landing container
```

### Modified files

```
go.work                           # Add ./config module
server/internal/admin/admin.go    # Add /api/validate-key, add reverse proxy to config, remove landing route
server/internal/admin/landing.go  # Delete (moved to landing/)
server/Dockerfile                 # Remove landing-related build steps if any
docker-compose.yml                # Add config + landing containers

client/src/main/index.ts          # Replace GitHub polling with config polling
client/src/main/preload.ts        # Replace updater bridge with config bridge
client/src/renderer/components/
  UpdateBanner.tsx                 # Delete
  NotificationBanner.tsx           # New: server-driven notifications
client/src/renderer/App.tsx        # Swap UpdateBanner → NotificationBanner
```

---

## Phase 1: Config Service Backend

### Task 1: Go module skeleton + DB

**Files:**
- Create: `config/go.mod`, `config/cmd/main.go`, `config/internal/db/db.go`
- Modify: `go.work`

- [ ] **Step 1: Create Go module**

```bash
mkdir -p config/cmd config/internal/db config/internal/api config/internal/poller
cd config && go mod init smurov-proxy/config
```

Add to `go.work`:
```
use (
    ./daemon
    ./helper
    ./pkg
    ./server
    ./test
    ./config
)
```

- [ ] **Step 2: Write DB layer with schema**

`config/internal/db/db.go`:
```go
package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

type Notification struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Message   string          `json:"message,omitempty"`
	Action    json.RawMessage `json:"action,omitempty"`
	Active    bool            `json:"active"`
	CreatedAt string          `json:"created_at"`
}

type ServiceConfig struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite3", path+"?_journal=WAL&_fk=on")
	if err != nil {
		return nil, err
	}
	if err := migrate(d); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: d}, nil
}

func migrate(d *sql.DB) error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS notifications (
			id         TEXT PRIMARY KEY,
			type       TEXT NOT NULL CHECK(type IN ('update','migration','maintenance','info')),
			title      TEXT NOT NULL,
			message    TEXT,
			action     TEXT,
			active     INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS service_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		INSERT OR IGNORE INTO service_config (key, value) VALUES
			('proxy_server', '95.181.162.242:443'),
			('relay_url', ''),
			('config_url', '');
	`)
	return err
}

func (d *DB) Close() { d.db.Close() }

// --- Notifications ---

func (d *DB) ListNotifications() ([]Notification, error) {
	rows, err := d.db.Query(`SELECT id, type, title, message, action, active, created_at FROM notifications ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var msg, act sql.NullString
		var active int
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &msg, &act, &active, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Message = msg.String
		if act.Valid {
			n.Action = json.RawMessage(act.String)
		}
		n.Active = active == 1
		out = append(out, n)
	}
	return out, nil
}

func (d *DB) ActiveNotifications() ([]Notification, error) {
	all, err := d.ListNotifications()
	if err != nil {
		return nil, err
	}
	var out []Notification
	for _, n := range all {
		if n.Active {
			out = append(out, n)
		}
	}
	return out, nil
}

func (d *DB) CreateNotification(typ, title, message string, action json.RawMessage) (Notification, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	var actStr *string
	if len(action) > 0 {
		s := string(action)
		actStr = &s
	}
	_, err := d.db.Exec(`INSERT INTO notifications (id, type, title, message, action, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, typ, title, message, actStr, now)
	if err != nil {
		return Notification{}, err
	}
	return Notification{ID: id, Type: typ, Title: title, Message: message, Action: action, Active: true, CreatedAt: now}, nil
}

func (d *DB) DeleteNotification(id string) error {
	res, err := d.db.Exec(`DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

func (d *DB) UpdateNotification(id string, active *bool, title, message *string) error {
	if active != nil {
		v := 0
		if *active {
			v = 1
		}
		if _, err := d.db.Exec(`UPDATE notifications SET active = ? WHERE id = ?`, v, id); err != nil {
			return err
		}
	}
	if title != nil {
		if _, err := d.db.Exec(`UPDATE notifications SET title = ? WHERE id = ?`, *title, id); err != nil {
			return err
		}
	}
	if message != nil {
		if _, err := d.db.Exec(`UPDATE notifications SET message = ? WHERE id = ?`, *message, id); err != nil {
			return err
		}
	}
	return nil
}

// --- Service Config ---

func (d *DB) GetServiceConfig() (map[string]string, error) {
	rows, err := d.db.Query(`SELECT key, value FROM service_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}

func (d *DB) SetServiceConfig(key, value string) error {
	_, err := d.db.Exec(`INSERT INTO service_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
```

- [ ] **Step 3: Write entry point**

`config/cmd/main.go`:
```go
package main

import (
	"flag"
	"log"
	"net/http"

	"smurov-proxy/config/internal/api"
	"smurov-proxy/config/internal/db"
	"smurov-proxy/config/internal/poller"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	dbPath := flag.String("db", "config.db", "SQLite database path")
	adminUser := flag.String("admin-user", "", "admin username")
	adminPass := flag.String("admin-pass", "", "admin password")
	proxyAddr := flag.String("proxy", "http://smurov-proxy:443", "proxy server internal address for key validation")
	githubRepo := flag.String("github-repo", "ilyasmurov/smurov-proxy", "GitHub repo for version check")
	flag.Parse()

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer d.Close()

	srv := api.New(d, *adminUser, *adminPass, *proxyAddr)

	// Start background version poller
	go poller.Start(d, *githubRepo)

	log.Printf("[config] listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
```

- [ ] **Step 4: Add go.sum and verify build**

```bash
cd config && go mod tidy && go build ./cmd/
```

- [ ] **Step 5: Commit**

```bash
git add go.work config/
git commit -m "feat(config): Go module skeleton with DB schema and entry point [skip-deploy]"
```

---

### Task 2: Config public API + key validation

**Files:**
- Create: `config/internal/api/api.go`
- Modify: `server/internal/admin/admin.go` (add validate-key endpoint)

- [ ] **Step 1: Add `/api/validate-key` to proxy server**

In `server/internal/admin/admin.go`, add inside `NewHandler` route registration:

```go
// Internal endpoint for config service to validate device keys.
// Not behind admin auth — called by config container over Docker network.
mux.HandleFunc("GET /api/validate-key", h.handleValidateKey)
```

Add handler method:

```go
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
```

- [ ] **Step 2: Write config API handler**

`config/internal/api/api.go`:
```go
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
	ConfigURL     string             `json:"config_url"`
	ProxyServer   string             `json:"proxy_server"`
	RelayURL      string             `json:"relay_url,omitempty"`
	Notifications []db.Notification  `json:"notifications"`
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
```

- [ ] **Step 3: Build and verify**

```bash
cd config && go mod tidy && go build ./cmd/
cd ../server && go build ./cmd/
```

- [ ] **Step 4: Commit**

```bash
git add config/internal/api/ server/internal/admin/admin.go
git commit -m "feat(config): client-config API with key validation via proxy [skip-deploy]"
```

---

### Task 3: Admin UI + version poller

**Files:**
- Create: `config/internal/api/admin.go`, `config/internal/poller/version.go`

- [ ] **Step 1: Write admin UI handler**

`config/internal/api/admin.go` — embedded single-page HTML with two tabs (Notifications, Services). Minimal: table + forms, inline CSS, fetch-based JS. Pattern matches existing admin-ui embed approach but simpler (no React, just vanilla JS).

```go
package api

import "net/http"

func (s *Server) handleAdminUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	u, p, ok := r.BasicAuth()
	if s.adminUser != "" && (!ok || u != s.adminUser || p != s.adminPass) {
		w.Header().Set("WWW-Authenticate", `Basic realm="config admin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(adminHTML))
}

const adminHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>SmurovProxy Config</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,system-ui,sans-serif;background:#0b0f1a;color:#e2e8f0;padding:24px;max-width:800px;margin:0 auto}
h1{font-size:20px;margin-bottom:16px}
.tabs{display:flex;gap:4px;margin-bottom:20px}
.tab{padding:8px 16px;background:#1a2234;border:1px solid #2a3a5a;border-radius:6px;color:#94a3b8;cursor:pointer;font-size:14px}
.tab.active{background:#1e3a5f;color:#fff;border-color:#3b82f6}
.panel{display:none}.panel.active{display:block}
table{width:100%;border-collapse:collapse;margin-bottom:16px}
th,td{text-align:left;padding:8px 12px;border-bottom:1px solid #1e2533;font-size:13px}
th{color:#64748b;font-weight:600}
.badge{display:inline-block;padding:2px 8px;border-radius:10px;font-size:11px;font-weight:600}
.badge.update{background:#1e3a5f;color:#60a5fa}
.badge.migration{background:#3b1f1f;color:#f87171}
.badge.maintenance{background:#2d2006;color:#fbbf24}
.badge.info{background:#1a2234;color:#94a3b8}
.badge.active{background:#0f3d1a;color:#4ade80}
.badge.inactive{background:#1a2234;color:#64748b}
input,textarea,select{background:#0f1420;border:1px solid #2a3a5a;color:#e2e8f0;padding:6px 10px;border-radius:4px;font-size:13px;width:100%}
textarea{min-height:60px;resize:vertical}
button{padding:6px 14px;border:none;border-radius:4px;font-size:13px;cursor:pointer;font-weight:500}
.btn-primary{background:#3b82f6;color:#fff}
.btn-danger{background:#ef4444;color:#fff}
.btn-sm{padding:3px 8px;font-size:12px}
.form-row{display:flex;gap:8px;margin-bottom:8px;align-items:end}
.form-group{flex:1}
.form-group label{display:block;font-size:12px;color:#64748b;margin-bottom:4px}
.mt{margin-top:16px}
</style>
</head>
<body>
<h1>SmurovProxy Config</h1>
<div class="tabs">
  <div class="tab active" onclick="showTab('notifs')">Notifications</div>
  <div class="tab" onclick="showTab('services')">Services</div>
</div>
<div id="notifs" class="panel active"></div>
<div id="services" class="panel"></div>
<script>
const API = '';
function showTab(id) {
  document.querySelectorAll('.tab').forEach((t,i) => t.classList.toggle('active', t.textContent.trim() === (id==='notifs'?'Notifications':'Services')));
  document.querySelectorAll('.panel').forEach(p => p.classList.toggle('active', p.id === id));
}
async function api(method, path, body) {
  const opts = {method, headers: {'Content-Type':'application/json'}};
  if (body) opts.body = JSON.stringify(body);
  const r = await fetch(API + path, opts);
  if (r.status === 204) return null;
  return r.json();
}
async function loadNotifs() {
  const data = await api('GET', '/api/admin/notifications');
  let html = '<table><tr><th>Type</th><th>Title</th><th>Status</th><th></th></tr>';
  (data||[]).forEach(n => {
    html += '<tr><td><span class="badge '+n.type+'">'+n.type+'</span></td>';
    html += '<td>'+n.title+'</td>';
    html += '<td><span class="badge '+(n.active?'active':'inactive')+'">'+(n.active?'active':'off')+'</span></td>';
    html += '<td><button class="btn-sm btn-danger" onclick="delNotif(\''+n.id+'\')">Delete</button> ';
    html += '<button class="btn-sm btn-primary" onclick="toggleNotif(\''+n.id+'\','+!n.active+')">'+(n.active?'Disable':'Enable')+'</button></td></tr>';
  });
  html += '</table>';
  html += '<div class="mt"><h3 style="font-size:14px;margin-bottom:8px">Create Notification</h3>';
  html += '<div class="form-row"><div class="form-group"><label>Type</label><select id="n-type"><option>update</option><option>migration</option><option>maintenance</option><option>info</option></select></div>';
  html += '<div class="form-group"><label>Title</label><input id="n-title"></div></div>';
  html += '<div class="form-group" style="margin-bottom:8px"><label>Message</label><textarea id="n-msg"></textarea></div>';
  html += '<button class="btn-primary" onclick="createNotif()">Create</button></div>';
  document.getElementById('notifs').innerHTML = html;
}
async function loadServices() {
  const data = await api('GET', '/api/admin/services');
  let html = '<table><tr><th>Key</th><th>Value</th></tr>';
  Object.entries(data||{}).forEach(([k,v]) => {
    html += '<tr><td>'+k+'</td><td><input id="svc-'+k+'" value="'+v+'"></td></tr>';
  });
  html += '</table><button class="btn-primary" onclick="saveServices()">Save</button>';
  document.getElementById('services').innerHTML = html;
}
async function createNotif() {
  await api('POST', '/api/admin/notifications', {
    type: document.getElementById('n-type').value,
    title: document.getElementById('n-title').value,
    message: document.getElementById('n-msg').value
  });
  loadNotifs();
}
async function delNotif(id) { await api('DELETE', '/api/admin/notifications/'+id); loadNotifs(); }
async function toggleNotif(id, active) { await api('PATCH', '/api/admin/notifications/'+id, {active}); loadNotifs(); }
async function saveServices() {
  const inputs = document.querySelectorAll('[id^="svc-"]');
  const body = {};
  inputs.forEach(i => body[i.id.replace('svc-','')] = i.value);
  await api('PUT', '/api/admin/services', body);
  alert('Saved');
}
loadNotifs(); loadServices();
</script>
</body>
</html>`
```

- [ ] **Step 2: Write version poller**

`config/internal/poller/version.go`:
```go
package poller

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"

	"smurov-proxy/config/internal/db"
)

var versionRe = regexp.MustCompile(`^version:\s*(.+)$`)

func Start(d *db.DB, repo string) {
	var lastVersion string
	check := func() {
		ver, err := fetchLatestVersion(repo)
		if err != nil {
			log.Printf("[poller] version check: %v", err)
			return
		}
		if lastVersion == "" {
			lastVersion = ver
			log.Printf("[poller] current latest: %s", ver)
			return
		}
		if ver != lastVersion {
			log.Printf("[poller] new version: %s (was %s)", ver, lastVersion)
			lastVersion = ver
			_, err := d.CreateNotification("update",
				fmt.Sprintf("Version %s available", ver),
				"A new client version has been released.",
				json.RawMessage(`{"label":"Update","type":"update"}`))
			if err != nil {
				log.Printf("[poller] create notification: %v", err)
			}
		}
	}
	check()
	for range time.Tick(1 * time.Hour) {
		check()
	}
}

func fetchLatestVersion(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}
```

- [ ] **Step 3: Build and verify**

```bash
cd config && go mod tidy && go build ./cmd/
```

- [ ] **Step 4: Commit**

```bash
git add config/
git commit -m "feat(config): admin UI + background version poller [skip-deploy]"
```

---

### Task 4: Config Dockerfile + deploy workflow

**Files:**
- Create: `config/Dockerfile`, `.github/workflows/deploy-config.yml`

- [ ] **Step 1: Write Dockerfile**

`config/Dockerfile`:
```dockerfile
FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /app
COPY go.work go.work.sum ./
COPY pkg/ pkg/
COPY config/ config/
RUN cd config && go build -ldflags="-s -w" -o /smurov-config ./cmd

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /smurov-config /smurov-config
EXPOSE 8443
ENTRYPOINT ["/smurov-config"]
```

- [ ] **Step 2: Write deploy workflow**

`.github/workflows/deploy-config.yml`:
```yaml
name: Deploy Config Service

on:
  push:
    branches: [main]
    paths: [config/**, pkg/**]
  workflow_dispatch:

env:
  REGISTRY: ghcr.io
  IMAGE_NAME: ${{ github.repository }}-config

jobs:
  check:
    runs-on: ubuntu-latest
    outputs:
      should_deploy: ${{ steps.check.outputs.should_deploy }}
    steps:
      - id: check
        env:
          COMMIT_MSG: ${{ github.event.head_commit.message }}
        run: |
          SUBJECT=$(printf '%s' "$COMMIT_MSG" | head -n1)
          if [[ "$SUBJECT" == *"[skip-deploy]"* ]]; then
            echo "should_deploy=false" >> "$GITHUB_OUTPUT"
          else
            echo "should_deploy=true" >> "$GITHUB_OUTPUT"
          fi

  build-and-deploy:
    needs: check
    if: needs.check.outputs.should_deploy == 'true'
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - uses: docker/build-push-action@v6
        with:
          context: .
          file: config/Dockerfile
          push: true
          tags: |
            ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:latest
            ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:${{ github.sha }}

      - name: Deploy to VPS
        uses: appleboy/ssh-action@v1
        with:
          host: ${{ secrets.VPS_HOST }}
          username: ${{ secrets.VPS_USER }}
          password: ${{ secrets.VPS_PASSWORD }}
          script: |
            echo "${{ secrets.GHCR_TOKEN }}" | docker login ghcr.io -u ${{ secrets.GHCR_USER }} --password-stdin
            docker pull ghcr.io/${{ env.IMAGE_NAME }}:latest
            docker stop smurov-config 2>/dev/null || true
            docker rm smurov-config 2>/dev/null || true
            docker run -d \
              --name smurov-config \
              --restart unless-stopped \
              --network host \
              -v smurov-config-data:/data \
              -e ADMIN_USER="${{ secrets.ADMIN_USER }}" \
              -e ADMIN_PASSWORD="${{ secrets.ADMIN_PASSWORD }}" \
              ghcr.io/${{ env.IMAGE_NAME }}:latest \
              -addr :8443 \
              -db /data/config.db \
              -admin-user "${{ secrets.ADMIN_USER }}" \
              -admin-pass "${{ secrets.ADMIN_PASSWORD }}" \
              -proxy "https://127.0.0.1:443"
```

- [ ] **Step 3: Commit**

```bash
git add config/Dockerfile .github/workflows/deploy-config.yml
git commit -m "ci(config): Dockerfile + deploy workflow [skip-deploy]"
```

---

## Phase 2: Landing Extraction

### Task 5: Extract landing page into standalone container

**Files:**
- Create: `landing/index.html`, `landing/nginx.conf`, `landing/Dockerfile`
- Modify: `server/internal/admin/admin.go` (remove landing route)
- Delete: `server/internal/admin/landing.go`

- [ ] **Step 1: Generate static landing HTML**

The current landing page is a Go template that dynamically fetches GitHub release URLs. For the static version, we hardcode the download links (they use `releases/latest/download/` pattern which auto-resolves).

Extract the HTML from `server/internal/admin/landing.go` into `landing/index.html`. Replace dynamic Go template variables with static `releases/latest/download/` URLs.

```bash
mkdir -p landing
```

The HTML content from `landing.go` is ~600 lines. Copy it to `landing/index.html`, replacing:
- `{{.MacURL}}` → `https://github.com/ilyasmurov/smurov-proxy/releases/latest/download/SmurovProxy-1.29.5-arm64.pkg` (or a JS-based latest resolver)
- `{{.WinURL}}` → `https://github.com/ilyasmurov/smurov-proxy/releases/latest/download/SmurovProxy-Setup-1.29.5.exe`

Better approach: add a small JS snippet that fetches `https://api.github.com/repos/ilyasmurov/smurov-proxy/releases/latest` and populates download links dynamically (same as landing.go does server-side, but client-side).

- [ ] **Step 2: Write nginx config**

`landing/nginx.conf`:
```nginx
server {
    listen 80;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    # Cache static assets
    location ~* \.(css|js|png|svg|ico|woff2?)$ {
        expires 7d;
        add_header Cache-Control "public, immutable";
    }
}
```

- [ ] **Step 3: Write Dockerfile**

`landing/Dockerfile`:
```dockerfile
FROM nginx:alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY index.html /usr/share/nginx/html/
EXPOSE 80
```

- [ ] **Step 4: Remove landing from proxy**

In `server/internal/admin/admin.go`, remove the landing route:
```go
// Remove: mux.Handle("GET /", LandingHandler(h.downloadsDir))
// Replace with redirect or 404:
mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path == "/" {
        http.Redirect(w, r, "https://proxy.smurov.com", http.StatusMovedPermanently)
        return
    }
    http.NotFound(w, r)
})
```

Delete `server/internal/admin/landing.go`.

- [ ] **Step 5: Commit**

```bash
git add landing/ server/internal/admin/
git rm server/internal/admin/landing.go
git commit -m "feat: extract landing page into standalone nginx container [skip-deploy]"
```

---

## Phase 3: Proxy Integration

### Task 6: Reverse proxy `/api/client-config` to config container

**Files:**
- Modify: `server/internal/admin/admin.go`

- [ ] **Step 1: Add reverse proxy handler**

In `server/internal/admin/admin.go`, add a reverse proxy that forwards `/api/client-config` to the config container:

```go
import "net/http/httputil"
import "net/url"

// In NewHandler, add route:
configTarget, _ := url.Parse("http://127.0.0.1:8443")
configProxy := httputil.NewSingleHostReverseProxy(configTarget)
mux.Handle("GET /api/client-config", configProxy)
```

This way clients hit `https://95.181.162.242:443/api/client-config?key=X` and the proxy transparently forwards to the config container on port 8443.

- [ ] **Step 2: Build and verify**

```bash
cd server && go build ./cmd/
```

- [ ] **Step 3: Commit**

```bash
git add server/internal/admin/admin.go
git commit -m "feat(proxy): reverse proxy /api/client-config to config container [skip-deploy]"
```

---

## Phase 4: Client Integration

### Task 7: Config poller + cache in main process

**Files:**
- Modify: `client/src/main/index.ts`

- [ ] **Step 1: Add config cache read/write**

In `client/src/main/index.ts`, add config cache functions near the top (after imports):

```typescript
import * as fs from "fs";

interface CachedConfig {
  config_url: string;
  proxy_server: string;
  relay_url: string;
  notifications: ServerNotification[];
  fetched_at: number;
}

interface ServerNotification {
  id: string;
  type: "update" | "migration" | "maintenance" | "info";
  title: string;
  message?: string;
  action?: { label: string; type: string; url?: string; server?: string };
  created_at: string;
}

const DEFAULT_CONFIG_URL = "https://95.181.162.242/api/client-config";

function configCachePath(): string {
  const dir = app.getPath("userData");
  return path.join(dir, "config-cache.json");
}

function readConfigCache(): CachedConfig | null {
  try {
    const data = fs.readFileSync(configCachePath(), "utf-8");
    return JSON.parse(data);
  } catch {
    return null;
  }
}

function writeConfigCache(cfg: CachedConfig) {
  try {
    fs.writeFileSync(configCachePath(), JSON.stringify(cfg));
  } catch (err) {
    console.error("[config] cache write error:", err);
  }
}
```

- [ ] **Step 2: Replace GitHub polling with config polling**

Replace the existing `fetchYml`, `checkForUpdatesAndNotify`, `check-update-version` IPC handler, and setInterval with a single config poller:

```typescript
let cachedConfig: CachedConfig | null = null;

async function pollConfig() {
  const now = Date.now();
  if (cachedConfig && now - (cachedConfig.fetched_at || 0) < 30_000) return; // 30s throttle

  const configUrl = cachedConfig?.config_url || DEFAULT_CONFIG_URL;
  // Need the device key — read from keyStore or stored key
  const key = /* get current key from server state */;
  if (!key) return;

  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15000);
    const res = await net.fetch(`${configUrl}?key=${encodeURIComponent(key)}&v=${app.getVersion()}`, {
      signal: controller.signal,
      redirect: "follow",
    });
    clearTimeout(timeout);
    if (!res.ok) return;

    const data = await res.json() as CachedConfig;
    data.fetched_at = now;
    cachedConfig = data;
    writeConfigCache(data);

    // Push to renderer
    sendUpdate("config-updated", data);
  } catch (err) {
    console.error("[config] poll failed:", err);
  }
}
```

In `app.whenReady`, replace the old setInterval:
```typescript
// Remove: setInterval(checkForUpdatesAndNotify, 60 * 60 * 1000);
// Add:
cachedConfig = readConfigCache();
setInterval(pollConfig, 30 * 60 * 1000); // 30 min
```

Keep the window show/focus hooks but call `pollConfig` instead of `checkForUpdatesAndNotify`.

Remove: `fetchYml`, `checkForUpdatesAndNotify`, `isNewer`, `UPDATE_BASE`, the `check-update-version` IPC handler, `download-update` IPC handler (will be rewritten in Task 9), `update-available` IPC event.

- [ ] **Step 3: Commit**

```bash
git add client/src/main/index.ts
git commit -m "feat(client): config poller replaces GitHub polling [skip-deploy]"
```

---

### Task 8: Expose config bridge in preload

**Files:**
- Modify: `client/src/main/preload.ts`

- [ ] **Step 1: Replace updater bridge with config bridge**

In `client/src/main/preload.ts`, replace the `updater` block:

```typescript
contextBridge.exposeInMainWorld("updater", {
  // Keep download/install for the notification action handler
  downloadUpdate: () => ipcRenderer.send("download-update"),
  installUpdate: () => ipcRenderer.send("install-update"),
  onUpdateProgress: (cb: (percent: number) => void) =>
    ipcRenderer.on("update-progress", (_e, percent) => cb(percent)),
  onUpdateDownloaded: (cb: () => void) =>
    ipcRenderer.on("update-downloaded", () => cb()),
  onUpdateError: (cb: () => void) =>
    ipcRenderer.on("update-error", () => cb()),
  // New: config-driven
  onConfigUpdated: (cb: (config: any) => void) =>
    ipcRenderer.on("config-updated", (_e, config) => cb(config)),
  getConfig: () => ipcRenderer.invoke("get-config"),
});
```

Add `get-config` IPC handler in main:
```typescript
ipcMain.handle("get-config", () => cachedConfig);
```

- [ ] **Step 2: Commit**

```bash
git add client/src/main/preload.ts client/src/main/index.ts
git commit -m "feat(client): config IPC bridge in preload [skip-deploy]"
```

---

### Task 9: NotificationBanner component

**Files:**
- Create: `client/src/renderer/components/NotificationBanner.tsx`
- Modify: `client/src/renderer/App.tsx`
- Delete: `client/src/renderer/components/UpdateBanner.tsx`

- [ ] **Step 1: Write NotificationBanner**

`client/src/renderer/components/NotificationBanner.tsx`:
```tsx
import { useState, useEffect, useRef } from "react";

declare global {
  interface Window {
    updater?: {
      downloadUpdate: () => void;
      installUpdate: () => void;
      onUpdateProgress: (cb: (percent: number) => void) => void;
      onUpdateDownloaded: (cb: () => void) => void;
      onUpdateError: (cb: () => void) => void;
      onConfigUpdated: (cb: (config: any) => void) => void;
      getConfig: () => Promise<any>;
    };
  }
}

interface Notification {
  id: string;
  type: "update" | "migration" | "maintenance" | "info";
  title: string;
  message?: string;
  action?: { label: string; type: string; url?: string; server?: string };
}

type DownloadState = "idle" | "downloading" | "ready";

const TYPE_PRIORITY: Record<string, number> = { migration: 0, update: 1, maintenance: 2, info: 3 };
const TYPE_COLORS: Record<string, { bg: string; border: string }> = {
  migration: { bg: "#2d1b1b", border: "#7f1d1d" },
  update: { bg: "#1a2744", border: "#2a4a7a" },
  maintenance: { bg: "#2d2006", border: "#78520a" },
  info: { bg: "#1a2234", border: "#2a3a5a" },
};

export function NotificationBanner() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [dlState, setDlState] = useState<DownloadState>("idle");
  const [progress, setProgress] = useState(0);
  const dlStateRef = useRef(dlState);
  dlStateRef.current = dlState;

  useEffect(() => {
    if (!window.updater) return;

    // Load initial config
    window.updater.getConfig().then((cfg) => {
      if (cfg?.notifications) setNotifications(cfg.notifications);
    });

    // Listen for config updates from main process poller
    window.updater.onConfigUpdated((cfg) => {
      if (cfg?.notifications) {
        // Don't clobber download flow
        if (dlStateRef.current !== "idle") return;
        setNotifications(cfg.notifications);
      }
    });

    // Download progress handlers (same as before)
    window.updater.onUpdateProgress((p) => setProgress(p < 0 ? Math.round(-p / 1024 / 1024) : p));
    window.updater.onUpdateDownloaded(() => setDlState("ready"));
    window.updater.onUpdateError(() => {
      setDlState("idle");
    });
  }, []);

  // Pick highest priority notification
  const sorted = [...notifications].sort((a, b) => (TYPE_PRIORITY[a.type] ?? 9) - (TYPE_PRIORITY[b.type] ?? 9));
  const notif = sorted[0];

  if (!notif && dlState === "idle") return null;

  // Download in progress — show progress bar
  if (dlState === "downloading") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, background: "#1a2744", border: "1px solid #2a4a7a", borderRadius: 8, fontSize: 13 }}>
        <div style={{ marginBottom: 4 }}>Downloading... {progress >= 0 ? `${progress}%` : `${-progress} MB`}</div>
        <div style={{ height: 4, background: "#333", borderRadius: 2 }}>
          <div style={{ height: 4, width: `${Math.max(progress, 0)}%`, background: "#3b82f6", borderRadius: 2 }} />
        </div>
      </div>
    );
  }

  // Ready to install
  if (dlState === "ready") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, background: "#0f3d1a", border: "1px solid #166534", borderRadius: 8, fontSize: 13, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>Update ready</span>
        <button onClick={() => window.updater?.installUpdate()} style={{ padding: "4px 12px", background: "#22c55e", color: "#fff", border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer" }}>
          Restart & Update
        </button>
      </div>
    );
  }

  if (!notif) return null;

  const colors = TYPE_COLORS[notif.type] || TYPE_COLORS.info;

  const handleAction = () => {
    if (!notif.action) return;
    switch (notif.action.type) {
      case "update":
        setDlState("downloading");
        window.updater?.downloadUpdate();
        break;
      case "open_url":
        if (notif.action.url) {
          require("electron").shell.openExternal(notif.action.url);
        }
        break;
      case "reconnect":
        // TODO: implement reconnect to new server address
        break;
    }
  };

  return (
    <div style={{ padding: "10px 12px", marginBottom: 16, background: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 8, fontSize: 13 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div>
          <div style={{ fontWeight: 600, marginBottom: notif.message ? 4 : 0 }}>{notif.title}</div>
          {notif.message && <div style={{ color: "#94a3b8", fontSize: 12 }}>{notif.message}</div>}
        </div>
        {notif.action && (
          <button onClick={handleAction} style={{ padding: "4px 12px", background: "#3b82f6", color: "#fff", border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer", whiteSpace: "nowrap", marginLeft: 12 }}>
            {notif.action.label}
          </button>
        )}
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Swap in App.tsx**

Replace `import { UpdateBanner }` with `import { NotificationBanner }` and swap the component in JSX.

- [ ] **Step 3: Delete UpdateBanner.tsx**

```bash
rm client/src/renderer/components/UpdateBanner.tsx
```

- [ ] **Step 4: Verify TypeScript compiles**

```bash
cd client && npx tsc --noEmit && npx tsc -p tsconfig.electron.json --noEmit
```

- [ ] **Step 5: Commit**

```bash
git add client/src/renderer/ client/src/main/
git rm client/src/renderer/components/UpdateBanner.tsx
git commit -m "feat(client): NotificationBanner replaces UpdateBanner [skip-deploy]"
```

---

## Phase 5: Deployment

### Task 10: docker-compose + integration

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Update docker-compose**

```yaml
services:
  proxy:
    image: ghcr.io/ilyasmurov/smurov-proxy:latest
    container_name: smurov-proxy
    restart: unless-stopped
    ports:
      - "443:443"
      - "443:443/udp"
    ulimits:
      nofile:
        soft: 32768
        hard: 32768
    volumes:
      - proxy-data:/data
    environment:
      - ADMIN_USER=${ADMIN_USER}
      - ADMIN_PASSWORD=${ADMIN_PASSWORD}
    command: ["-addr", ":443", "-db", "/data/data.db", "-cert", "/data/cert.pem", "-keyfile", "/data/key.pem"]

  config:
    image: ghcr.io/ilyasmurov/smurov-proxy-config:latest
    container_name: smurov-config
    restart: unless-stopped
    network_mode: host
    volumes:
      - config-data:/data
    command: ["-addr", ":8443", "-db", "/data/config.db", "-admin-user", "${ADMIN_USER}", "-admin-pass", "${ADMIN_PASSWORD}", "-proxy", "https://127.0.0.1:443"]

  landing:
    image: ghcr.io/ilyasmurov/smurov-proxy-landing:latest
    container_name: smurov-landing
    restart: unless-stopped
    ports:
      - "80:80"

volumes:
  proxy-data:
  config-data:
```

- [ ] **Step 2: Commit**

```bash
git add docker-compose.yml
git commit -m "ci: docker-compose with proxy + config + landing containers [skip-deploy]"
```

---

### Task 11: Final integration test

- [ ] **Step 1: Manually verify config service**

```bash
# Start config locally
cd config && go run ./cmd/ -addr :8443 -db /tmp/test-config.db -proxy "https://95.181.162.242:443"
```

In another terminal:
```bash
# Get a valid device key from the proxy admin
KEY="<valid-device-key>"

# Test client-config endpoint
curl -s "http://localhost:8443/api/client-config?key=$KEY" | jq .

# Test admin notifications CRUD
curl -s -X POST http://localhost:8443/api/admin/notifications \
  -H 'Content-Type: application/json' \
  -d '{"type":"info","title":"Test notification","message":"Hello world"}'

curl -s http://localhost:8443/api/admin/notifications | jq .

# Test admin services
curl -s http://localhost:8443/api/admin/services | jq .
```

- [ ] **Step 2: Test proxy reverse proxy**

After deploying both containers, verify the forwarding:
```bash
curl -s "https://95.181.162.242/api/client-config?key=$KEY" -k | jq .
```

Should return the same response as hitting port 8443 directly.

- [ ] **Step 3: Final commit + push**

```bash
git push origin main
```
