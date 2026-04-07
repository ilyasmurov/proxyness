# Browser Extension Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Manifest V3 Chrome extension that complements the desktop client with per-tab proxy state UI, automatic domain discovery via `chrome.webRequest`, and ISP-block detection with silent verification through the tunnel.

**Architecture:** Extension talks ONLY to the local daemon at `127.0.0.1:9090`, never to `proxy.smurov.com`. Daemon persists the device key on first Connect, maintains an in-memory `my_sites` cache, exposes a new `/sites/*` API guarded by a per-extension bearer token, and forwards add/discover ops to the existing server `/api/sync` endpoint via a new HTTP client.

**Tech Stack:**
- Server: Go + SQLite (extend existing `server/internal/admin/sync.go` and `server/internal/db/sites.go`)
- Daemon: Go + `net/http` (extend `daemon/internal/api/`, new `daemon/internal/sites/` package)
- Desktop client: Electron/React (small additions to `client/src/main/` and `client/src/renderer/`)
- Extension: vanilla JS + Manifest V3 + Shadow DOM (no framework)

---

## Architectural Assumptions

These were not exhaustively discussed in the spec. If any are wrong, fix the assumption and the affected tasks before starting.

1. **Daemon online-only sync (no offline buffer in daemon).** The daemon's `/sites/add` and `/sites/discover` endpoints fail with 503 if the daemon cannot reach the server right now. The renderer keeps its own offline buffer (existing `client/src/renderer/sites/sync.ts`). Rationale: extension is for active browsing which already needs internet, and adding an offline queue to the daemon duplicates renderer logic.
2. **Daemon persists device key after first Connect.** Currently `s.key = req.Key` is in-memory only. This plan adds a tiny on-disk persist so the daemon can call `/api/sync` between client restarts and BEFORE the user explicitly connects the tunnel. Key file lives at `~/.config/smurov-proxy/device-key` (Unix) / `%APPDATA%\SmurovProxy\device-key` (Windows), mode `0600`. Same threat model as the existing localStorage key in the renderer.
3. **In-memory `my_sites` cache, refreshed every 5 minutes + on writes.** The daemon stores its current view of `my_sites` (the user's enabled sites with their domain lists) in RAM. `/sites/match` queries this cache (no I/O). Refresh on: daemon start (load from server), every 5 min, immediately after a successful add/discover op.
4. **Extension is not formally tested.** Browser extension test infrastructure (Puppeteer + extension loading) is overkill for v1. Each extension task has a "Manual verification" step instead of automated tests. Server and daemon code IS tested via Go test suite.
5. **CHANGELOG.new.md needs `git add -f`.** The pre-commit hook deletes the file after processing and `.gitignore` blocks it. Always force-add.

---

## File Structure

### New files

```
server/internal/db/sites.go             # extend with ApplyAddDomainOp
server/internal/admin/sync.go           # extend syncOp + applyOp for "add_domain"

daemon/internal/sites/                  # NEW package
├── cache.go                            # in-memory my_sites cache + match logic
├── cache_test.go
├── client.go                           # HTTP client to server /api/sync
├── client_test.go
├── key.go                              # device key file persistence
├── key_test.go
├── token.go                            # extension auth token file persistence
└── token_test.go

daemon/internal/api/sites.go            # NEW: 4 HTTP endpoints
daemon/internal/api/sites_test.go
daemon/internal/api/auth_token.go       # NEW: bearer token middleware
daemon/internal/api/auth_token_test.go

client/src/main/extension.ts            # NEW: IPC handler for daemon token
client/src/renderer/components/BrowserExtension.tsx  # NEW: settings tab content

extension/                              # NEW root for the browser extension
├── manifest.json
├── service-worker.js
├── content-script.js
├── lib/
│   ├── daemon-client.js                # token-aware fetch wrapper
│   ├── tldts.min.js                    # bundled public suffix list (~30KB)
│   └── state.js                        # tab state cache, persistence
├── panel/
│   ├── panel.html
│   ├── panel.css
│   └── panel.js
├── popup/
│   ├── popup.html
│   ├── popup.css
│   └── popup.js
├── icons/
│   ├── icon-16.png
│   ├── icon-32.png
│   ├── icon-48.png
│   └── icon-128.png
└── README.md                           # sideload instructions for dev
```

### Modified files

```
server/internal/admin/sync.go           # add "add_domain" case
server/internal/db/sites.go             # add ApplyAddDomainOp
daemon/internal/api/api.go              # mount /sites/* routes, persist key
daemon/cmd/main.go                      # construct sites cache + key loader
client/src/main/preload.ts              # expose getDaemonToken IPC
client/src/main/index.ts                # register IPC handler
client/src/renderer/App.tsx             # add Browser Extension tab
.gitignore                              # ignore extension/build/, daemon-token, device-key
```

---

## Task 1: Server — add_domain sync op type

**Files:**
- Modify: `server/internal/db/sites.go`
- Modify: `server/internal/db/sites_test.go`
- Modify: `server/internal/admin/sync.go`
- Modify: `server/internal/admin/sync_test.go`

- [ ] **Step 1: Write the failing test for `ApplyAddDomainOp`**

Add to `server/internal/db/sites_test.go`:

```go
func TestApplyAddDomainOpAddsAndDedupes(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	user, _ := d.CreateUser("alice")
	tx, _ := d.SQL().Begin()
	addRes, err := d.ApplyAddOp(tx, user.ID, "habr.com", "Habr", 1000)
	if err != nil {
		t.Fatal(err)
	}
	siteID := addRes.SiteID

	// First insert: added.
	r, err := d.ApplyAddDomainOp(tx, user.ID, siteID, "habrcdn.io", 2000)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if r.Deduped {
		t.Fatalf("expected added, got deduped")
	}

	// Second insert of same domain: dedup.
	r2, err := d.ApplyAddDomainOp(tx, user.ID, siteID, "habrcdn.io", 3000)
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if !r2.Deduped {
		t.Fatalf("expected deduped, got added")
	}

	tx.Commit()

	// Verify row exists.
	var count int
	d.SQL().QueryRow(
		`SELECT COUNT(*) FROM site_domains WHERE site_id=? AND domain=?`,
		siteID, "habrcdn.io",
	).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestApplyAddDomainOpRejectsNonLinkedUser(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	owner, _ := d.CreateUser("owner")
	stranger, _ := d.CreateUser("stranger")

	tx, _ := d.SQL().Begin()
	addRes, _ := d.ApplyAddOp(tx, owner.ID, "habr.com", "Habr", 1000)

	// Stranger should NOT be allowed to add domains to owner's site.
	_, err := d.ApplyAddDomainOp(tx, stranger.ID, addRes.SiteID, "habrcdn.io", 2000)
	if err == nil {
		t.Fatalf("expected error for non-linked user, got nil")
	}
	tx.Rollback()
}

func TestApplyAddDomainOpRejectsInvalidDomain(t *testing.T) {
	d, cleanup := openTestDB(t)
	defer cleanup()

	user, _ := d.CreateUser("alice")
	tx, _ := d.SQL().Begin()
	addRes, _ := d.ApplyAddOp(tx, user.ID, "habr.com", "Habr", 1000)

	_, err := d.ApplyAddDomainOp(tx, user.ID, addRes.SiteID, "NOT A DOMAIN", 2000)
	if err == nil {
		t.Fatalf("expected error for invalid domain")
	}
	tx.Rollback()
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd server && go test ./internal/db/ -run TestApplyAddDomainOp -v`
Expected: FAIL with "ApplyAddDomainOp undefined"

- [ ] **Step 3: Implement `ApplyAddDomainOp`**

Add to `server/internal/db/sites.go`:

```go
type AddDomainOpResult struct {
	Deduped bool
}

// ApplyAddDomainOp adds a domain to an existing site, only if the user is
// linked to that site via user_sites. Used by the discovery flow to enrich
// the global catalog with subdomains/CDN hosts a user encounters while
// browsing a site they have enabled.
func (d *DB) ApplyAddDomainOp(tx *sql.Tx, userID, siteID int, domain string, at int64) (AddDomainOpResult, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return AddDomainOpResult{}, fmt.Errorf("invalid domain: %q", domain)
	}

	// Auth: only users who have the site enabled can enrich its domains.
	var dummy int
	err := tx.QueryRow(
		`SELECT 1 FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, siteID,
	).Scan(&dummy)
	if err == sql.ErrNoRows {
		return AddDomainOpResult{}, fmt.Errorf("user %d not linked to site %d", userID, siteID)
	}
	if err != nil {
		return AddDomainOpResult{}, err
	}

	res, err := tx.Exec(
		`INSERT OR IGNORE INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`,
		siteID, domain,
	)
	if err != nil {
		return AddDomainOpResult{}, err
	}
	rows, _ := res.RowsAffected()
	return AddDomainOpResult{Deduped: rows == 0}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./internal/db/ -run TestApplyAddDomainOp -v`
Expected: PASS for all three tests.

- [ ] **Step 5: Wire op into the sync HTTP handler**

Modify `server/internal/admin/sync.go`. Update `syncOp` struct to include the domain field, and add a new case to `applyOp`.

```go
type syncOp struct {
	Op      string   `json:"op"` // "add" | "remove" | "enable" | "disable" | "add_domain"
	LocalID *int     `json:"local_id,omitempty"`
	SiteID  int      `json:"site_id,omitempty"`
	Site    *siteDTO `json:"site,omitempty"`
	Domain  string   `json:"domain,omitempty"`
	At      int64    `json:"at"`
}
```

Add case in `applyOp` switch (alphabetical order doesn't matter, put it after `enable`/`disable`):

```go
	case "add_domain":
		if op.SiteID == 0 || op.Domain == "" {
			res.Status = "invalid"
			res.Message = "missing site_id or domain"
			return res
		}
		r, err := h.db.ApplyAddDomainOp(tx, userID, op.SiteID, op.Domain, op.At)
		if err != nil {
			res.Status = "error"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"
		res.SiteID = op.SiteID
		res.Deduped = r.Deduped
```

- [ ] **Step 6: Add a sync handler integration test for the new op**

Add to `server/internal/admin/sync_integration_test.go` (after the existing `TestSyncIntegrationFullFlow`):

```go
func TestSyncIntegrationAddDomainOp(t *testing.T) {
	d, _ := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	defer d.Close()

	user, _ := d.CreateUser("alice")
	dev, _ := d.CreateDevice(user.ID, "mac")
	h := NewHandler(d, nil, "admin", "pw", t.TempDir())

	// Create habr.com site first (it's not in the seed).
	w := postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "add", "local_id": -1, "site": map[string]string{"primary_domain": "habr.com", "label": "Habr"}, "at": 1000},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("add habr code=%d", w.Code)
	}
	var r1 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r1)
	siteID := int(r1["op_results"].([]interface{})[0].(map[string]interface{})["site_id"].(float64))

	// Add a discovered domain.
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "add_domain", "site_id": siteID, "domain": "habrcdn.io", "at": 2000},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("add_domain code=%d: %s", w.Code, w.Body.String())
	}
	var r2 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r2)
	op := r2["op_results"].([]interface{})[0].(map[string]interface{})
	if op["status"] != "ok" {
		t.Fatalf("expected ok, got %+v", op)
	}
	if op["deduped"] == true {
		t.Fatalf("first add should not be deduped")
	}

	// Same domain again should dedupe.
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "add_domain", "site_id": siteID, "domain": "habrcdn.io", "at": 3000},
		},
	})
	json.NewDecoder(w.Body).Decode(&r2)
	op = r2["op_results"].([]interface{})[0].(map[string]interface{})
	if op["deduped"] != true {
		t.Fatalf("expected dedup, got %+v", op)
	}
}
```

- [ ] **Step 7: Run all server tests**

Run: `cd server && go test ./internal/db/ ./internal/admin/ -v -run "TestApplyAddDomainOp|TestSyncIntegrationAddDomainOp"`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
add_domain sync op для обогащения site_domains через discovery
EOF
git add -f CHANGELOG.new.md server/internal/db/sites.go server/internal/db/sites_test.go server/internal/admin/sync.go server/internal/admin/sync_integration_test.go
git commit -m "feat(server): add_domain sync op with auth check [skip-deploy]"
```

---

## Task 2: Daemon — device key persistence

**Files:**
- Create: `daemon/internal/sites/key.go`
- Create: `daemon/internal/sites/key_test.go`
- Modify: `daemon/internal/api/api.go` (handleConnect)

- [ ] **Step 1: Write the failing test for key persistence**

Create `daemon/internal/sites/key_test.go`:

```go
package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-key")

	store := NewKeyStore(path)

	if got := store.Load(); got != "" {
		t.Fatalf("expected empty on first load, got %q", got)
	}

	if err := store.Save("abc123"); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file mode is 0600
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}

	// Load via a fresh store
	store2 := NewKeyStore(path)
	if got := store2.Load(); got != "abc123" {
		t.Fatalf("expected 'abc123', got %q", got)
	}
}

func TestKeyStoreOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-key")
	store := NewKeyStore(path)

	store.Save("first")
	store.Save("second")

	if got := NewKeyStore(path).Load(); got != "second" {
		t.Fatalf("expected 'second', got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd daemon && go test ./internal/sites/ -run TestKeyStore -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement KeyStore**

Create `daemon/internal/sites/key.go`:

```go
// Package sites holds the daemon-side state for the browser extension's
// /sites/* HTTP API: persisted device key, persisted extension auth token,
// in-memory my_sites cache, and an HTTP client to the server's /api/sync.
package sites

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// KeyStore is a tiny on-disk persisted device key. The file is mode 0600.
// The key is loaded once on Save and on the first Load call; subsequent
// Loads return the cached value.
type KeyStore struct {
	path string
	mu   sync.RWMutex
	key  string
}

func NewKeyStore(path string) *KeyStore {
	return &KeyStore{path: path}
}

// Load returns the persisted key or "" if no file exists. Reads from disk
// only on first call; subsequent calls hit the cache.
func (s *KeyStore) Load() string {
	s.mu.RLock()
	if s.key != "" {
		k := s.key
		s.mu.RUnlock()
		return k
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != "" {
		return s.key
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return ""
	}
	s.key = strings.TrimSpace(string(data))
	return s.key
}

// Save writes the key to disk with mode 0600 and updates the cache.
func (s *KeyStore) Save(key string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, []byte(key), 0600); err != nil {
		return err
	}
	s.mu.Lock()
	s.key = key
	s.mu.Unlock()
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/sites/ -run TestKeyStore -v`
Expected: PASS.

- [ ] **Step 5: Wire KeyStore into daemon api Server**

Modify `daemon/internal/api/api.go`. Add to the `Server` struct:

```go
import (
	// ... existing imports
	"smurov-proxy/daemon/internal/sites"
)

type Server struct {
	// ... existing fields
	keyStore *sites.KeyStore
}
```

Add a setter:

```go
func (s *Server) SetKeyStore(ks *sites.KeyStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyStore = ks
	// Restore key from disk on startup so /sites/* endpoints work even
	// before the user explicitly connects the tunnel.
	if s.key == "" {
		s.key = ks.Load()
	}
}
```

In `handleConnect` at the existing `s.key = req.Key` line, also persist:

```go
s.key = req.Key
if s.keyStore != nil {
	if err := s.keyStore.Save(req.Key); err != nil {
		log.Printf("[sites] failed to persist device key: %v", err)
	}
}
```

- [ ] **Step 6: Initialize KeyStore in daemon main**

Modify `daemon/cmd/main.go` to construct the KeyStore and call `SetKeyStore`. Find where the api server is constructed (search for `api.New(`) and add right after:

```go
ks := sites.NewKeyStore(sites.DefaultKeyPath())
apiServer.SetKeyStore(ks)
```

Add `DefaultKeyPath()` to `daemon/internal/sites/key.go`:

```go
// DefaultKeyPath returns the OS-appropriate path for the persisted device key.
func DefaultKeyPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "SmurovProxy", "device-key")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "smurov-proxy", "device-key")
}
```

Add `"runtime"` to the imports.

- [ ] **Step 7: Run daemon tests**

Run: `cd daemon && go test ./internal/sites/ ./internal/api/ -v`
Expected: PASS, no regressions.

- [ ] **Step 8: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## improvement
Демон персистит device key на диск для использования /sites/* API без активного туннеля
EOF
git add -f CHANGELOG.new.md daemon/internal/sites/key.go daemon/internal/sites/key_test.go daemon/internal/api/api.go daemon/cmd/main.go
git commit -m "feat(daemon): persist device key for sites API [skip-deploy]"
```

---

## Task 3: Daemon — extension auth token

**Files:**
- Create: `daemon/internal/sites/token.go`
- Create: `daemon/internal/sites/token_test.go`
- Create: `daemon/internal/api/auth_token.go`
- Create: `daemon/internal/api/auth_token_test.go`
- Modify: `daemon/cmd/main.go`

- [ ] **Step 1: Write the failing test for TokenStore**

Create `daemon/internal/sites/token_test.go`:

```go
package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStoreGenerateOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-token")
	store := NewTokenStore(path)

	tok1, err := store.GetOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok1) != 64 {
		t.Fatalf("expected 64-char hex token, got %d chars: %q", len(tok1), tok1)
	}

	// Second call returns the same token.
	tok2, _ := store.GetOrCreate()
	if tok1 != tok2 {
		t.Fatalf("expected stable token, got %q vs %q", tok1, tok2)
	}

	// Fresh store loads the same token.
	store2 := NewTokenStore(path)
	tok3, _ := store2.GetOrCreate()
	if tok1 != tok3 {
		t.Fatalf("expected loaded token to match, got %q vs %q", tok1, tok3)
	}

	// File mode 0600.
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}
}

func TestTokenStoreCheck(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(filepath.Join(dir, "daemon-token"))
	tok, _ := store.GetOrCreate()

	if !store.Check(tok) {
		t.Fatalf("expected Check(valid) = true")
	}
	if store.Check("wrong") {
		t.Fatalf("expected Check(wrong) = false")
	}
	if store.Check("") {
		t.Fatalf("expected Check(empty) = false")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestTokenStore -v`
Expected: FAIL — TokenStore undefined.

- [ ] **Step 3: Implement TokenStore**

Add to `daemon/internal/sites/token.go`:

```go
package sites

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// TokenStore is a per-extension auth token persisted to disk. Generated
// on first GetOrCreate; the same value is returned on subsequent calls.
// File mode 0600.
type TokenStore struct {
	path  string
	mu    sync.RWMutex
	token string
}

func NewTokenStore(path string) *TokenStore {
	return &TokenStore{path: path}
}

// GetOrCreate returns the persisted token, generating + saving a new one
// if no file exists.
func (s *TokenStore) GetOrCreate() (string, error) {
	s.mu.RLock()
	if s.token != "" {
		t := s.token
		s.mu.RUnlock()
		return t, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}

	// Try to load.
	if data, err := os.ReadFile(s.path); err == nil {
		s.token = strings.TrimSpace(string(data))
		if len(s.token) == 64 {
			return s.token, nil
		}
		// Corrupted; fall through to regenerate.
	}

	// Generate new.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s.token = hex.EncodeToString(b)

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.path, []byte(s.token), 0600); err != nil {
		return "", err
	}
	return s.token, nil
}

// Check is a constant-time comparison of the provided token against the
// stored one. Returns false if no token has been generated yet.
func (s *TokenStore) Check(provided string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.token == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.token), []byte(provided)) == 1
}

// DefaultTokenPath returns the OS-appropriate path for the daemon token.
func DefaultTokenPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "SmurovProxy", "daemon-token")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "smurov-proxy", "daemon-token")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/sites/ -run TestTokenStore -v`
Expected: PASS.

- [ ] **Step 5: Write the auth middleware test**

Create `daemon/internal/api/auth_token_test.go`:

```go
package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"smurov-proxy/daemon/internal/sites"
)

func TestRequireExtensionTokenAllowsValid(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()

	called := false
	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 || !called {
		t.Fatalf("expected handler to run, code=%d called=%v", w.Code, called)
	}
}

func TestRequireExtensionTokenRejectsMissing(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()

	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestRequireExtensionTokenRejectsWrong(t *testing.T) {
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()

	h := requireExtensionToken(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	r := httptest.NewRequest("GET", "/sites/match?host=x.com", nil)
	r.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 401 {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 6: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestRequireExtensionToken -v`
Expected: FAIL — `requireExtensionToken` undefined.

- [ ] **Step 7: Implement the middleware**

Create `daemon/internal/api/auth_token.go`:

```go
package api

import (
	"net/http"
	"strings"

	"smurov-proxy/daemon/internal/sites"
)

// requireExtensionToken wraps a handler with a constant-time bearer-token
// check against the provided TokenStore. Used only on /sites/* routes.
func requireExtensionToken(store *sites.TokenStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight from the extension origin: allow without token.
		if r.Method == "OPTIONS" {
			w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(auth[len(prefix):])
		if !store.Check(token) {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}

		// Allow the actual handler to add CORS headers on the response too.
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/api/ -run TestRequireExtensionToken -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Per-extension auth token + middleware для /sites/* эндпоинтов демона
EOF
git add -f CHANGELOG.new.md daemon/internal/sites/token.go daemon/internal/sites/token_test.go daemon/internal/api/auth_token.go daemon/internal/api/auth_token_test.go
git commit -m "feat(daemon): extension token auth for sites API [skip-deploy]"
```

---

## Task 4: Daemon — server HTTP client

**Files:**
- Create: `daemon/internal/sites/client.go`
- Create: `daemon/internal/sites/client_test.go`

- [ ] **Step 1: Write the failing test for SyncClient**

Create `daemon/internal/sites/client_test.go`:

```go
package sites

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncClientSendsAuthAndOps(t *testing.T) {
	var captured map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dev-key-123" {
			t.Errorf("expected Bearer dev-key-123, got %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &captured)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"op_results":[{"site_id":42,"status":"ok"}],"my_sites":[],"server_time":1000}`))
	}))
	defer srv.Close()

	client := NewSyncClient(srv.URL, "dev-key-123")
	resp, err := client.SyncOps([]map[string]interface{}{
		{"op": "add", "local_id": -1, "site": map[string]string{"primary_domain": "habr.com", "label": "Habr"}, "at": 1000},
	})
	if err != nil {
		t.Fatalf("SyncOps: %v", err)
	}

	if len(resp.OpResults) != 1 || resp.OpResults[0].SiteID != 42 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	if captured["last_sync_at"].(float64) != 0 {
		t.Errorf("expected last_sync_at=0, got %v", captured["last_sync_at"])
	}
	ops := captured["ops"].([]interface{})
	if len(ops) != 1 {
		t.Errorf("expected 1 op, got %d", len(ops))
	}
}

func TestSyncClientReturnsErrorOnNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", 503)
	}))
	defer srv.Close()

	client := NewSyncClient(srv.URL, "dev-key-123")
	_, err := client.SyncOps(nil)
	if err == nil {
		t.Fatalf("expected error on 503")
	}
}

func TestSyncClientHandlesNoKey(t *testing.T) {
	client := NewSyncClient("http://example.com", "")
	_, err := client.SyncOps(nil)
	if err == nil {
		t.Fatalf("expected error when key is empty")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestSyncClient -v`
Expected: FAIL — SyncClient undefined.

- [ ] **Step 3: Implement SyncClient**

Create `daemon/internal/sites/client.go`:

```go
package sites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// SyncResponse mirrors the server's syncResponse type from
// server/internal/admin/sync.go. Kept minimal — only fields the daemon
// actually uses.
type SyncResponse struct {
	OpResults  []OpResult `json:"op_results"`
	MySites    []MySite   `json:"my_sites"`
	ServerTime int64      `json:"server_time"`
}

type OpResult struct {
	LocalID *int   `json:"local_id,omitempty"`
	SiteID  int    `json:"site_id,omitempty"`
	Status  string `json:"status"`
	Deduped bool   `json:"deduped,omitempty"`
	Message string `json:"message,omitempty"`
}

// MySite mirrors db.UserSite, exposing the fields the daemon needs for
// its in-memory cache. Names match the server's JSON.
type MySite struct {
	ID            int      `json:"id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	PrimaryDomain string   `json:"primary_domain"`
	Domains       []string `json:"domains"`
	IPs           []string `json:"ips"`
	Enabled       bool     `json:"enabled"`
	UpdatedAt     int64    `json:"updated_at"`
}

// SyncClient is the daemon's HTTP client to the server's POST /api/sync.
// It is online-only — it does NOT buffer offline.
type SyncClient struct {
	baseURL string
	mu      sync.RWMutex
	key     string
	http    *http.Client
}

func NewSyncClient(baseURL, key string) *SyncClient {
	return &SyncClient{
		baseURL: baseURL,
		key:     key,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// SetKey updates the cached device key. Called when the desktop client
// connects (and thus persists the key).
func (c *SyncClient) SetKey(key string) {
	c.mu.Lock()
	c.key = key
	c.mu.Unlock()
}

func (c *SyncClient) hasKey() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.key != ""
}

func (c *SyncClient) authHeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return "Bearer " + c.key
}

// SyncOps posts the given ops batch to /api/sync and returns the server
// response. last_sync_at is always 0 — the daemon does not track partial
// state, it always asks for the full snapshot.
func (c *SyncClient) SyncOps(ops []map[string]interface{}) (*SyncResponse, error) {
	if !c.hasKey() {
		return nil, fmt.Errorf("no device key set")
	}

	body, _ := json.Marshal(map[string]interface{}{
		"last_sync_at": 0,
		"ops":          ops,
	})

	req, err := http.NewRequest("POST", c.baseURL+"/api/sync", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sync %d: %s", resp.StatusCode, string(buf))
	}

	var sr SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &sr, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/sites/ -run TestSyncClient -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Daemon SyncClient — HTTP клиент к серверному /api/sync для extension API
EOF
git add -f CHANGELOG.new.md daemon/internal/sites/client.go daemon/internal/sites/client_test.go
git commit -m "feat(daemon): server sync client [skip-deploy]"
```

---

## Task 5: Daemon — in-memory my_sites cache

**Files:**
- Create: `daemon/internal/sites/cache.go`
- Create: `daemon/internal/sites/cache_test.go`

- [ ] **Step 1: Write the failing test for Cache**

Create `daemon/internal/sites/cache_test.go`:

```go
package sites

import (
	"testing"
)

func TestCacheMatch(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com", "habrcdn.io"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com", "ytimg.com"}, Enabled: false},
	})

	// Direct match by primary domain.
	if m := cache.Match("habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected match site 1, got %+v", m)
	}

	// Direct match by secondary domain.
	if m := cache.Match("habrcdn.io"); m == nil || m.ID != 1 {
		t.Fatalf("expected match site 1 by habrcdn.io, got %+v", m)
	}

	// Subdomain match (news.habr.com → habr.com).
	if m := cache.Match("news.habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected subdomain match, got %+v", m)
	}

	// No match.
	if m := cache.Match("wikipedia.org"); m != nil {
		t.Fatalf("expected no match, got %+v", m)
	}
}

func TestCacheEnabledFlag(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: false},
	})

	if m := cache.Match("habr.com"); !m.Enabled {
		t.Fatalf("habr should be enabled")
	}
	if m := cache.Match("vk.com"); m.Enabled {
		t.Fatalf("vk should be disabled")
	}
}

func TestCacheConcurrency(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true}})

	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			cache.Match("habr.com")
		}
		done <- true
	}()
	for i := 0; i < 100; i++ {
		cache.Replace([]MySite{{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true}})
	}
	<-done
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestCache -v`
Expected: FAIL — Cache undefined.

- [ ] **Step 3: Implement Cache**

Create `daemon/internal/sites/cache.go`:

```go
package sites

import (
	"strings"
	"sync"
)

// Cache holds the user's full my_sites snapshot in memory. Refreshed on
// daemon start, every 5 minutes, and immediately after a write op.
//
// Match() does a substring + suffix match across all known domains for
// every cached site. The total domain count is small (low hundreds even
// for power users), so a linear scan is fine and avoids needing a trie.
type Cache struct {
	mu    sync.RWMutex
	sites []MySite
}

func NewCache() *Cache {
	return &Cache{}
}

// Replace swaps the entire cache contents.
func (c *Cache) Replace(sites []MySite) {
	c.mu.Lock()
	c.sites = sites
	c.mu.Unlock()
}

// Snapshot returns a shallow copy of the current site list.
func (c *Cache) Snapshot() []MySite {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]MySite, len(c.sites))
	copy(out, c.sites)
	return out
}

// Match returns the first site whose domain list contains the given host
// (or any parent of the host). Returns nil if no match.
func (c *Cache) Match(host string) *MySite {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	for i := range c.sites {
		s := &c.sites[i]
		for _, d := range s.Domains {
			if hostMatches(host, d) {
				return s
			}
		}
	}
	return nil
}

// hostMatches reports whether host equals pattern or is a sub-domain of it.
// "news.habr.com" matches pattern "habr.com"; "habr.com" matches "habr.com";
// "evilhabr.com" does NOT match "habr.com".
func hostMatches(host, pattern string) bool {
	if host == pattern {
		return true
	}
	return strings.HasSuffix(host, "."+pattern)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/sites/ -run TestCache -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
In-memory my_sites cache в демоне с subdomain matching
EOF
git add -f CHANGELOG.new.md daemon/internal/sites/cache.go daemon/internal/sites/cache_test.go
git commit -m "feat(daemon): in-memory sites cache [skip-deploy]"
```

---

## Task 6: Daemon — Sites manager (cache + client + refresh)

**Files:**
- Create: `daemon/internal/sites/manager.go`
- Create: `daemon/internal/sites/manager_test.go`

- [ ] **Step 1: Write the failing test**

Create `daemon/internal/sites/manager_test.go`:

```go
package sites

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newServerWithSites(initial []MySite) (*httptest.Server, *int) {
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		json.NewEncoder(w).Encode(SyncResponse{
			MySites:    initial,
			ServerTime: time.Now().Unix(),
		})
	}))
	return srv, &calls
}

func TestManagerRefreshLoadsCache(t *testing.T) {
	srv, _ := newServerWithSites([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := NewManager(srv.URL, keyStore)

	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if m := mgr.Cache().Match("habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected habr.com matched after refresh")
	}
}

func TestManagerAddSiteRefreshesCache(t *testing.T) {
	var sitesMu sync.Mutex
	currentSites := []MySite{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)

		sitesMu.Lock()
		ops, _ := req["ops"].([]interface{})
		for _, op := range ops {
			m := op.(map[string]interface{})
			if m["op"] == "add" {
				site := m["site"].(map[string]interface{})
				newID := len(currentSites) + 1
				currentSites = append(currentSites, MySite{
					ID:            newID,
					PrimaryDomain: site["primary_domain"].(string),
					Domains:       []string{site["primary_domain"].(string)},
					Enabled:       true,
				})
			}
		}
		sites := append([]MySite{}, currentSites...)
		sitesMu.Unlock()

		json.NewEncoder(w).Encode(map[string]interface{}{
			"op_results":  []map[string]interface{}{{"site_id": len(sites), "status": "ok", "deduped": false}},
			"my_sites":    sites,
			"server_time": time.Now().Unix(),
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := NewManager(srv.URL, keyStore)

	siteID, deduped, err := mgr.AddSite("habr.com", "Habr")
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	if siteID == 0 || deduped {
		t.Fatalf("unexpected: id=%d deduped=%v", siteID, deduped)
	}

	// Cache should now contain habr.com.
	if m := mgr.Cache().Match("habr.com"); m == nil {
		t.Fatalf("expected cache to contain habr.com after AddSite")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestManager -v`
Expected: FAIL — Manager undefined.

- [ ] **Step 3: Implement Manager**

Create `daemon/internal/sites/manager.go`:

```go
package sites

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Manager is the daemon-side composite that wires KeyStore + SyncClient +
// Cache and exposes the operations the HTTP API needs. It is the only
// type the api package interacts with for sites work.
type Manager struct {
	keyStore *KeyStore
	client   *SyncClient
	cache    *Cache

	mu          sync.Mutex
	stopRefresh chan struct{}
}

func NewManager(serverURL string, keyStore *KeyStore) *Manager {
	key := keyStore.Load()
	return &Manager{
		keyStore: keyStore,
		client:   NewSyncClient(serverURL, key),
		cache:    NewCache(),
	}
}

// Cache exposes the read-only cache for /sites/match.
func (m *Manager) Cache() *Cache { return m.cache }

// SetKey is called from the api server when the user (re)connects via
// the desktop client. Updates both the persisted key and the sync client.
func (m *Manager) SetKey(key string) {
	if err := m.keyStore.Save(key); err != nil {
		log.Printf("[sites] persist key: %v", err)
	}
	m.client.SetKey(key)
}

// Refresh fetches the user's full my_sites snapshot and replaces the cache.
// Returns an error if the device key is missing or the server is unreachable.
func (m *Manager) Refresh() error {
	resp, err := m.client.SyncOps(nil)
	if err != nil {
		return err
	}
	m.cache.Replace(resp.MySites)
	return nil
}

// AddSite enqueues an "add" op to the server, refreshes the cache on
// success, and returns the assigned site_id.
func (m *Manager) AddSite(primaryDomain, label string) (int, bool, error) {
	resp, err := m.client.SyncOps([]map[string]interface{}{
		{
			"op":       "add",
			"local_id": -1,
			"site":     map[string]string{"primary_domain": primaryDomain, "label": label},
			"at":       time.Now().Unix(),
		},
	})
	if err != nil {
		return 0, false, err
	}
	if len(resp.OpResults) == 0 {
		return 0, false, fmt.Errorf("no op_results in response")
	}
	r := resp.OpResults[0]
	if r.Status != "ok" {
		return 0, false, fmt.Errorf("server: %s", r.Message)
	}
	m.cache.Replace(resp.MySites)
	return r.SiteID, r.Deduped, nil
}

// AddDomains enqueues add_domain ops for the given domains.
func (m *Manager) AddDomains(siteID int, domains []string) (int, int, error) {
	now := time.Now().Unix()
	ops := make([]map[string]interface{}, 0, len(domains))
	for _, d := range domains {
		ops = append(ops, map[string]interface{}{
			"op":      "add_domain",
			"site_id": siteID,
			"domain":  d,
			"at":      now,
		})
	}
	resp, err := m.client.SyncOps(ops)
	if err != nil {
		return 0, 0, err
	}
	added, deduped := 0, 0
	for _, r := range resp.OpResults {
		if r.Status == "ok" {
			if r.Deduped {
				deduped++
			} else {
				added++
			}
		}
	}
	if added > 0 {
		m.cache.Replace(resp.MySites)
	}
	return added, deduped, nil
}

// StartBackgroundRefresh starts a goroutine that calls Refresh every
// `interval`. Stops when StopBackgroundRefresh is called or the manager
// is garbage collected.
func (m *Manager) StartBackgroundRefresh(interval time.Duration) {
	m.mu.Lock()
	if m.stopRefresh != nil {
		m.mu.Unlock()
		return
	}
	m.stopRefresh = make(chan struct{})
	stop := m.stopRefresh
	m.mu.Unlock()

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := m.Refresh(); err != nil {
					log.Printf("[sites] background refresh: %v", err)
				}
			}
		}
	}()
}

func (m *Manager) StopBackgroundRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopRefresh != nil {
		close(m.stopRefresh)
		m.stopRefresh = nil
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/sites/ -run TestManager -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Daemon Sites Manager — координирует key store, sync client, in-memory cache
EOF
git add -f CHANGELOG.new.md daemon/internal/sites/manager.go daemon/internal/sites/manager_test.go
git commit -m "feat(daemon): sites manager (cache + client orchestration) [skip-deploy]"
```

---

## Task 7: Daemon — /sites/match and /sites/add HTTP endpoints

**Files:**
- Create: `daemon/internal/api/sites.go`
- Create: `daemon/internal/api/sites_test.go`
- Modify: `daemon/internal/api/api.go`
- Modify: `daemon/cmd/main.go`

- [ ] **Step 1: Write the failing test for handlers**

Create `daemon/internal/api/sites_test.go`:

```go
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"smurov-proxy/daemon/internal/sites"
)

func newTestServerWithSitesAPI(t *testing.T, mgr *sites.Manager, tokenStore *sites.TokenStore) *Server {
	s := &Server{
		sitesManager: mgr,
		tokenStore:   tokenStore,
	}
	return s
}

func TestHandleSitesMatch(t *testing.T) {
	mgr := sites.NewManager("http://unused", sites.NewKeyStore(filepath.Join(t.TempDir(), "k")))
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	r := httptest.NewRequest("GET", "/sites/match?host=habr.com", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesMatch)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["in_catalog"] != true {
		t.Fatalf("expected in_catalog=true, got %+v", resp)
	}
	if resp["site_id"].(float64) != 1 {
		t.Fatalf("expected site_id=1, got %+v", resp["site_id"])
	}
	if resp["proxy_enabled"] != true {
		t.Fatalf("expected proxy_enabled=true")
	}
}

func TestHandleSitesMatchNotInCatalog(t *testing.T) {
	mgr := sites.NewManager("http://unused", sites.NewKeyStore(filepath.Join(t.TempDir(), "k")))
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	r := httptest.NewRequest("GET", "/sites/match?host=wikipedia.org", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesMatch)).ServeHTTP(w, r)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["in_catalog"] != false {
		t.Fatalf("expected in_catalog=false")
	}
}

func TestHandleSitesAdd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"op_results": []map[string]interface{}{{"site_id": 99, "status": "ok", "deduped": false}},
			"my_sites": []map[string]interface{}{
				{"id": 99, "primary_domain": "habr.com", "domains": []string{"habr.com"}, "enabled": true},
			},
			"server_time": 1000,
		})
	}))
	defer srv.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "k"))
	keyStore.Save("test-key")
	mgr := sites.NewManager(srv.URL, keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := newTestServerWithSitesAPI(t, mgr, store)

	body, _ := json.Marshal(map[string]string{"primary_domain": "habr.com", "label": "Habr"})
	r := httptest.NewRequest("POST", "/sites/add", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesAdd)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["site_id"].(float64) != 99 {
		t.Fatalf("expected site_id=99, got %+v", resp)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestHandleSites -v`
Expected: FAIL.

- [ ] **Step 3: Add the new fields to Server struct**

Modify `daemon/internal/api/api.go`. Add to imports:

```go
"smurov-proxy/daemon/internal/sites"
```

Add to `Server` struct:

```go
type Server struct {
	// ... existing fields
	sitesManager *sites.Manager
	tokenStore   *sites.TokenStore
}
```

Add a setter:

```go
// SetSites wires the sites manager and token store. Called once at startup
// from daemon main.
func (s *Server) SetSites(mgr *sites.Manager, tokenStore *sites.TokenStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sitesManager = mgr
	s.tokenStore = tokenStore
}
```

In `handleConnect`, replace the existing `s.key = req.Key` block with:

```go
s.key = req.Key
if s.sitesManager != nil {
	s.sitesManager.SetKey(req.Key)
	// Best-effort initial refresh; OK to fail (offline first connect).
	go func() {
		if err := s.sitesManager.Refresh(); err != nil {
			log.Printf("[sites] initial refresh: %v", err)
		}
	}()
}
```

Mount the new routes in `Handler()` (add at the end of the existing route list, before `return mux`):

```go
// Sites API for browser extension. All require Authorization: Bearer <token>.
if s.tokenStore != nil {
	mux.Handle("GET /sites/match", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesMatch)))
	mux.Handle("POST /sites/add", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesAdd)))
	mux.Handle("POST /sites/discover", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesDiscover)))
	mux.Handle("POST /sites/test", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesTest)))
	mux.Handle("OPTIONS /sites/match", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesMatch)))
	mux.Handle("OPTIONS /sites/add", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesAdd)))
	mux.Handle("OPTIONS /sites/discover", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesDiscover)))
	mux.Handle("OPTIONS /sites/test", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesTest)))
}
```

- [ ] **Step 4: Implement the four handlers**

Create `daemon/internal/api/sites.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

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
```

- [ ] **Step 5: Wire daemon main**

Modify `daemon/cmd/main.go`. After `apiServer := api.New(...)`, add:

```go
keyStore := sites.NewKeyStore(sites.DefaultKeyPath())
tokenStore := sites.NewTokenStore(sites.DefaultTokenPath())
if _, err := tokenStore.GetOrCreate(); err != nil {
	log.Fatalf("daemon token: %v", err)
}
sitesManager := sites.NewManager("https://proxy.smurov.com", keyStore)
sitesManager.StartBackgroundRefresh(5 * time.Minute)
apiServer.SetKeyStore(keyStore)
apiServer.SetSites(sitesManager, tokenStore)

// Best-effort first refresh — fine to fail if offline.
go func() {
	if err := sitesManager.Refresh(); err != nil {
		log.Printf("[sites] initial refresh: %v", err)
	}
}()
```

Add imports if missing: `"smurov-proxy/daemon/internal/sites"`, `"time"`.

- [ ] **Step 6: Run tests**

Run: `cd daemon && go test ./internal/api/ ./internal/sites/ -v`
Expected: PASS for everything except `handleSitesTest` (still 501).

- [ ] **Step 7: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Daemon HTTP API: GET /sites/match, POST /sites/add, POST /sites/discover
EOF
git add -f CHANGELOG.new.md daemon/internal/api/sites.go daemon/internal/api/sites_test.go daemon/internal/api/api.go daemon/cmd/main.go
git commit -m "feat(daemon): sites match/add/discover HTTP endpoints [skip-deploy]"
```

---

## Task 8: Daemon — /sites/test (HEAD via SOCKS5)

**Files:**
- Modify: `daemon/internal/api/sites.go`
- Modify: `daemon/internal/api/sites_test.go`

- [ ] **Step 1: Write the failing test**

The test uses an `httptest.Server` and verifies the daemon makes a request through it as a SOCKS5 proxy. Mocking SOCKS5 is awkward, so the test instead injects a custom http client into the Server struct. Add to `daemon/internal/api/sites_test.go`:

```go
import (
	"net/url"
)

func TestHandleSitesTestConfirmsBlock(t *testing.T) {
	// Stub the "real" upstream that responds 200 (simulating a successful
	// proxied fetch).
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	s := &Server{
		tokenStore: store,
		sitesTestClient: &http.Client{
			// Bypass tunnel: just hit the upstream directly. The handler
			// only cares whether the response was 2xx/3xx.
			Transport: &http.Transport{
				DialContext: nil,
			},
		},
	}

	body, _ := json.Marshal(map[string]string{"url": upstream.URL})
	r := httptest.NewRequest("POST", "/sites/test", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	requireExtensionToken(store, http.HandlerFunc(s.handleSitesTest)).ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["likely_blocked"] != true {
		t.Fatalf("expected likely_blocked=true, got %+v", resp)
	}
	_ = url.Parse // keep import
}
```

- [ ] **Step 2: Add the SOCKS5 http.Client field to Server**

In `daemon/internal/api/api.go`:

```go
type Server struct {
	// ... existing fields
	sitesTestClient *http.Client  // dials through the local SOCKS5 tunnel
}
```

Add a setter:

```go
func (s *Server) SetSitesTestClient(c *http.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sitesTestClient = c
}
```

- [ ] **Step 3: Implement handleSitesTest**

In `daemon/internal/api/sites.go`, replace the placeholder:

```go
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
	httpReq.Header.Set("User-Agent", "SmurovProxy-Discovery/1.0")

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
```

- [ ] **Step 4: Construct the SOCKS5 client in daemon main**

In `daemon/cmd/main.go`, after the existing `sitesManager` setup:

```go
import (
	"context"
	"net"
	"net/http"
	"golang.org/x/net/proxy"
)

// ...

socksDialer, err := proxy.SOCKS5("tcp", "127.0.0.1:1080", nil, proxy.Direct)
if err != nil {
	log.Fatalf("socks5 dialer: %v", err)
}
testClient := &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return socksDialer.Dial(network, addr)
		},
	},
}
apiServer.SetSitesTestClient(testClient)
```

If `golang.org/x/net/proxy` is not yet a dependency, add it: `cd daemon && go get golang.org/x/net/proxy && go mod tidy`.

- [ ] **Step 5: Run tests**

Run: `cd daemon && go test ./internal/api/ -run TestHandleSitesTest -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
POST /sites/test — HEAD-запрос через SOCKS5 тоннель для подтверждения блокировки
EOF
git add -f CHANGELOG.new.md daemon/internal/api/sites.go daemon/internal/api/sites_test.go daemon/internal/api/api.go daemon/cmd/main.go daemon/go.mod daemon/go.sum
git commit -m "feat(daemon): sites/test endpoint via SOCKS5 [skip-deploy]"
```

---

## Task 9: Desktop client — Browser Extension settings tab

**Files:**
- Create: `client/src/main/extension.ts`
- Create: `client/src/renderer/components/BrowserExtension.tsx`
- Modify: `client/src/main/index.ts`
- Modify: `client/src/main/preload.ts`
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Create the IPC handler in main process**

Create `client/src/main/extension.ts`:

```ts
import fs from "fs";
import os from "os";
import path from "path";

function tokenPath(): string {
  if (process.platform === "win32") {
    return path.join(process.env.APPDATA || os.homedir(), "SmurovProxy", "daemon-token");
  }
  return path.join(os.homedir(), ".config", "smurov-proxy", "daemon-token");
}

export function getDaemonToken(): string {
  try {
    return fs.readFileSync(tokenPath(), "utf-8").trim();
  } catch {
    return "";
  }
}
```

- [ ] **Step 2: Register IPC handler in main**

Modify `client/src/main/index.ts`. Add import:

```ts
import { getDaemonToken } from "./extension";
```

In `setupIpc()`, add (near `get-installed-apps`):

```ts
ipcMain.handle("get-daemon-token", () => getDaemonToken());
```

- [ ] **Step 3: Expose via preload**

Modify `client/src/main/preload.ts`. Add to `appInfo`:

```ts
getDaemonToken: () => ipcRenderer.invoke("get-daemon-token"),
```

- [ ] **Step 4: Create the React component**

Create `client/src/renderer/components/BrowserExtension.tsx`:

```tsx
import { useEffect, useState } from "react";

export function BrowserExtension() {
  const [token, setToken] = useState<string>("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    (window as any).appInfo?.getDaemonToken?.().then((t: string) => setToken(t));
  }, []);

  const copy = async () => {
    if (!token) return;
    await navigator.clipboard.writeText(token);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div style={{ padding: 24, maxWidth: 600 }}>
      <h2 style={{ marginTop: 0 }}>Browser Extension</h2>
      <p style={{ color: "#888", marginBottom: 16 }}>
        Установи расширение в Chrome / Edge / Brave (см. <code>extension/README.md</code>),
        затем открой расширение и вставь токен ниже:
      </p>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <input
          readOnly
          value={token || "(daemon not running)"}
          style={{
            flex: 1,
            fontFamily: "monospace",
            fontSize: 12,
            padding: "8px 12px",
            background: "#1a1f2e",
            border: "1px solid #2a3042",
            color: "#aab3c5",
            borderRadius: 4,
          }}
          onClick={(e) => (e.target as HTMLInputElement).select()}
        />
        <button
          onClick={copy}
          disabled={!token}
          style={{
            padding: "8px 16px",
            background: copied ? "#22c55e" : "#3b82f6",
            color: "#fff",
            border: "none",
            borderRadius: 4,
            cursor: token ? "pointer" : "not-allowed",
          }}
        >
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
      <p style={{ color: "#666", fontSize: 12, marginTop: 16 }}>
        Токен генерируется автоматически при первом старте демона. Если хочешь
        отозвать доступ — удали файл <code>~/.config/smurov-proxy/daemon-token</code>
        и перезапусти клиент.
      </p>
    </div>
  );
}
```

- [ ] **Step 5: Add a tab to App.tsx**

Modify `client/src/renderer/App.tsx`. Find the tab rendering area (search for where AppRules is used) and add a new tab option. Exact insertion depends on the current tab structure — read the file first and add a "Browser Extension" tab next to "Apps" or similar.

Sample addition (adapt to existing tab pattern):

```tsx
import { BrowserExtension } from "./components/BrowserExtension";

// In the tabs render:
<button onClick={() => setActiveTab("extension")}>Browser Extension</button>

// In the content render:
{activeTab === "extension" && <BrowserExtension />}
```

- [ ] **Step 6: Type-check**

Run: `cd client && npx tsc --noEmit -p tsconfig.json`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Вкладка "Browser Extension" в десктоп-клиенте с токеном для пейринга
EOF
git add -f CHANGELOG.new.md client/src/main/extension.ts client/src/renderer/components/BrowserExtension.tsx client/src/main/index.ts client/src/main/preload.ts client/src/renderer/App.tsx
git commit -m "feat(client): browser extension settings tab [skip-deploy]"
```

---

## Task 10: Extension — scaffold (manifest + folders)

**Files:**
- Create: `extension/manifest.json`
- Create: `extension/README.md`
- Create: `extension/icons/` (placeholder files for now)
- Modify: `.gitignore`

- [ ] **Step 1: Write manifest.json**

Create `extension/manifest.json`:

```json
{
  "manifest_version": 3,
  "name": "Smurov Proxy",
  "version": "0.1.0",
  "description": "Per-tab proxy controls and automatic domain discovery for Smurov Proxy.",
  "permissions": [
    "webRequest",
    "webNavigation",
    "storage",
    "tabs"
  ],
  "host_permissions": [
    "<all_urls>",
    "http://127.0.0.1:9090/*"
  ],
  "background": {
    "service_worker": "service-worker.js",
    "type": "module"
  },
  "content_scripts": [
    {
      "matches": ["<all_urls>"],
      "js": ["content-script.js"],
      "run_at": "document_idle"
    }
  ],
  "action": {
    "default_popup": "popup/popup.html",
    "default_icon": {
      "16": "icons/icon-16.png",
      "32": "icons/icon-32.png",
      "48": "icons/icon-48.png",
      "128": "icons/icon-128.png"
    }
  },
  "icons": {
    "16": "icons/icon-16.png",
    "32": "icons/icon-32.png",
    "48": "icons/icon-48.png",
    "128": "icons/icon-128.png"
  }
}
```

- [ ] **Step 2: Create placeholder icons**

Run from project root:

```bash
mkdir -p extension/icons
# Use any 128x128 PNG. For now, copy the existing client tray icon.
cp client/build/iconTemplate.png extension/icons/icon-128.png 2>/dev/null || \
  printf '\x89PNG\r\n\x1a\n' > extension/icons/icon-128.png  # placeholder
# Sips on macOS to resize:
sips -z 16 16 extension/icons/icon-128.png --out extension/icons/icon-16.png 2>/dev/null || cp extension/icons/icon-128.png extension/icons/icon-16.png
sips -z 32 32 extension/icons/icon-128.png --out extension/icons/icon-32.png 2>/dev/null || cp extension/icons/icon-128.png extension/icons/icon-32.png
sips -z 48 48 extension/icons/icon-128.png --out extension/icons/icon-48.png 2>/dev/null || cp extension/icons/icon-128.png extension/icons/icon-48.png
```

Real icons can be replaced before publishing to the Web Store.

- [ ] **Step 3: Create empty stub files for the entry points**

Create empty files so subsequent tasks can append to them and the manifest validates when loaded:

```bash
mkdir -p extension/popup extension/panel extension/lib
echo "// Service worker — populated in Task 11" > extension/service-worker.js
echo "// Content script — populated in Task 14" > extension/content-script.js
cat > extension/popup/popup.html <<'EOF'
<!doctype html>
<html><head><meta charset="utf-8"></head>
<body><script src="popup.js"></script></body></html>
EOF
echo "// Popup — populated in Task 12" > extension/popup/popup.js
```

- [ ] **Step 4: Write README**

Create `extension/README.md`:

```markdown
# Smurov Proxy — Browser Extension

Companion to the Smurov Proxy desktop client. Provides per-tab proxy controls,
automatic domain discovery, and ISP-block detection.

**Requires the Smurov Proxy desktop client** running on the same machine.

## Install (development)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Toggle on "Developer mode" (top right).
3. Click "Load unpacked" and select this `extension/` folder.
4. Click the extension icon → paste the token shown in the desktop client's
   "Browser Extension" tab.

The extension will be published to the Chrome Web Store when feature-complete
and tested.

## Architecture

See [`docs/superpowers/specs/2026-04-08-browser-extension-design.md`](../docs/superpowers/specs/2026-04-08-browser-extension-design.md).
```

- [ ] **Step 5: Update .gitignore**

Add to `.gitignore`:

```
extension/build/
```

- [ ] **Step 6: Verify the extension loads in Chrome (manual)**

Open `chrome://extensions`, enable Developer mode, click "Load unpacked",
select the `extension/` folder. The extension should load with no errors
in the console (check the service worker logs by clicking "Inspect views:
service-worker"). The toolbar icon should appear; clicking it should open
an empty popup window.

- [ ] **Step 7: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Browser extension scaffold: manifest v3 + папочная структура + sideload README
EOF
git add -f CHANGELOG.new.md extension/manifest.json extension/README.md extension/service-worker.js extension/content-script.js extension/popup/popup.html extension/popup/popup.js extension/icons/ .gitignore
git commit -m "feat(extension): manifest v3 scaffold [skip-deploy]"
```

---

## Task 11: Extension — service worker daemon client

**Files:**
- Create: `extension/lib/daemon-client.js`
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Implement the daemon client wrapper**

Create `extension/lib/daemon-client.js`:

```js
// Daemon client: token-aware fetch wrapper for the local Smurov daemon API.
// All extension → daemon HTTP traffic flows through this module.

const DAEMON_BASE = "http://127.0.0.1:9090";

let cachedToken = null;

async function getToken() {
  if (cachedToken) return cachedToken;
  const stored = await chrome.storage.local.get("daemon_token");
  cachedToken = stored.daemon_token || null;
  return cachedToken;
}

export async function setToken(token) {
  cachedToken = token;
  await chrome.storage.local.set({ daemon_token: token });
}

export async function clearToken() {
  cachedToken = null;
  await chrome.storage.local.remove("daemon_token");
}

async function call(method, path, body) {
  const token = await getToken();
  if (!token) {
    return { ok: false, error: "no_token" };
  }

  let resp;
  try {
    resp = await fetch(DAEMON_BASE + path, {
      method,
      headers: {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch (e) {
    return { ok: false, error: "daemon_down" };
  }

  if (resp.status === 401) {
    await clearToken();
    return { ok: false, error: "unauthorized" };
  }
  if (!resp.ok) {
    const text = await resp.text();
    return { ok: false, error: `http_${resp.status}`, message: text };
  }
  const data = await resp.json();
  return { ok: true, data };
}

export const daemonClient = {
  match: (host) => call("GET", `/sites/match?host=${encodeURIComponent(host)}`),
  add: (primaryDomain, label) => call("POST", "/sites/add", { primary_domain: primaryDomain, label }),
  discover: (siteId, domains) => call("POST", "/sites/discover", { site_id: siteId, domains }),
  test: (url) => call("POST", "/sites/test", { url }),
  ping: async () => {
    // Used for daemon-up detection without needing a real query.
    const r = await call("GET", "/sites/match?host=ping.local");
    return r.ok || r.error === "unauthorized";
  },
};
```

- [ ] **Step 2: Initialize service worker**

Replace `extension/service-worker.js`:

```js
import { daemonClient, setToken, clearToken } from "./lib/daemon-client.js";

// Per-tab state cache.
// Map<tabId, {host, state, siteId, discovering}>
const tabState = new Map();

// Listen for messages from popup (pairing) and content script (state queries).
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "set_token") {
    setToken(msg.token).then(async () => {
      const ok = await daemonClient.ping();
      sendResponse({ ok });
    });
    return true;  // async
  }

  if (msg.type === "clear_token") {
    clearToken().then(() => sendResponse({ ok: true }));
    return true;
  }

  if (msg.type === "get_state") {
    const tabId = sender.tab?.id;
    const state = tabState.get(tabId) || { state: "idle" };
    sendResponse(state);
    return false;
  }

  if (msg.type === "ping_daemon") {
    daemonClient.ping().then((up) => sendResponse({ up }));
    return true;
  }
});

// Refresh tab state on activation/navigation. Real implementation in Task 14.
chrome.tabs.onActivated.addListener(async (info) => {
  // Stub: full implementation in Task 14.
});

console.log("[smurov-proxy] service worker loaded");
```

- [ ] **Step 3: Manual verification**

1. Reload the extension at `chrome://extensions`.
2. Click "Inspect views: service-worker" — DevTools opens.
3. Console should show `[smurov-proxy] service worker loaded`.
4. In the DevTools console, run:
   ```js
   chrome.runtime.sendMessage({type: "ping_daemon"}, console.log)
   ```
   With the desktop daemon running and no token set, should print
   `{up: false}` (because the unauthorized 401 is treated as up; but with
   no token, the call returns `error: "no_token"`, ping returns false).

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension service worker + daemon client wrapper с token storage
EOF
git add -f CHANGELOG.new.md extension/lib/daemon-client.js extension/service-worker.js
git commit -m "feat(extension): daemon client and service worker [skip-deploy]"
```

---

## Task 12: Extension — popup pairing screen

**Files:**
- Modify: `extension/popup/popup.html`
- Create: `extension/popup/popup.css`
- Modify: `extension/popup/popup.js`

- [ ] **Step 1: Write popup HTML**

Replace `extension/popup/popup.html`:

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <link rel="stylesheet" href="popup.css">
</head>
<body>
  <div id="root">
    <div class="loading">…</div>
  </div>
  <script src="popup.js"></script>
</body>
</html>
```

- [ ] **Step 2: Write popup CSS**

Create `extension/popup/popup.css`:

```css
* { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, system-ui, sans-serif; }
body { width: 320px; background: #0b0f1a; color: #e8eaf0; font-size: 13px; }
#root { padding: 16px; }
.title { font-size: 14px; font-weight: 600; margin-bottom: 12px; }
.subtitle { color: #888; font-size: 12px; margin-bottom: 12px; line-height: 1.5; }
input[type="text"] {
  width: 100%; padding: 10px; margin-bottom: 8px;
  background: #1a1f2e; border: 1px solid #2a3042; border-radius: 4px;
  color: #e8eaf0; font-family: monospace; font-size: 11px;
}
button {
  width: 100%; padding: 10px; background: #3b82f6; color: #fff;
  border: none; border-radius: 4px; cursor: pointer; font-weight: 500;
}
button:disabled { opacity: 0.5; cursor: not-allowed; }
button.danger { background: #dc2626; }
.error { color: #f87171; font-size: 12px; margin-top: 8px; }
.ok { color: #4ade80; font-size: 12px; margin-top: 8px; }
.row { display: flex; align-items: center; gap: 8px; margin-bottom: 8px; }
.label { color: #9ca3af; font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; }
.value { color: #e8eaf0; font-weight: 500; }
```

- [ ] **Step 3: Implement popup logic**

Replace `extension/popup/popup.js`:

```js
const root = document.getElementById("root");

async function getStoredToken() {
  const r = await chrome.storage.local.get("daemon_token");
  return r.daemon_token || null;
}

async function tryPing(token) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage({ type: "set_token", token }, (resp) => resolve(resp?.ok === true));
  });
}

async function clearAndRender() {
  await new Promise((resolve) => chrome.runtime.sendMessage({ type: "clear_token" }, resolve));
  render();
}

function renderPairing(initialError) {
  root.innerHTML = `
    <div class="title">Pair with Smurov Proxy</div>
    <div class="subtitle">
      Open the Smurov Proxy desktop client → Browser Extension tab,
      copy the token, paste it below.
    </div>
    <input type="text" id="token" placeholder="abc123..." autofocus>
    <button id="pair">Pair</button>
    <div id="msg" class="${initialError ? 'error' : ''}">${initialError || ''}</div>
  `;
  document.getElementById("pair").addEventListener("click", async () => {
    const token = document.getElementById("token").value.trim();
    const msg = document.getElementById("msg");
    if (token.length !== 64) {
      msg.textContent = "Token should be 64 hex characters.";
      msg.className = "error";
      return;
    }
    msg.textContent = "Pairing…";
    msg.className = "";
    const ok = await tryPing(token);
    if (ok) {
      render();
    } else {
      msg.textContent = "Pairing failed. Is the daemon running? Token correct?";
      msg.className = "error";
    }
  });
}

function renderPaired() {
  root.innerHTML = `
    <div class="title">✓ Paired</div>
    <div class="row"><span class="label">Status:</span><span class="value">Connected to local daemon</span></div>
    <div class="subtitle" style="margin-top: 12px;">
      The extension is monitoring your tabs and will offer to add new sites
      to the proxy when needed.
    </div>
    <button id="unpair" class="danger">Unpair</button>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

async function render() {
  const token = await getStoredToken();
  if (!token) {
    renderPairing();
    return;
  }
  // Verify token still works.
  const ok = await tryPing(token);
  if (ok) {
    renderPaired();
  } else {
    renderPairing("Saved token is invalid. Please re-pair.");
  }
}

render();
```

- [ ] **Step 4: Manual verification**

1. Reload the extension.
2. Click the toolbar icon → popup opens with pairing screen.
3. Open desktop client → Browser Extension tab → copy the token.
4. Paste into the popup → click Pair → should switch to "✓ Paired".
5. Close popup, reopen → should show "✓ Paired" directly (no re-paste).
6. Click Unpair → returns to pairing screen.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension popup: pairing screen + paired state с unpair кнопкой
EOF
git add -f CHANGELOG.new.md extension/popup/popup.html extension/popup/popup.css extension/popup/popup.js
git commit -m "feat(extension): popup pairing UI [skip-deploy]"
```

---

## Task 13: Extension — content script + Shadow DOM panel skeleton

**Files:**
- Modify: `extension/content-script.js`
- Create: `extension/panel/panel.css`
- Create: `extension/panel/panel.html` (template fragment loaded inline in JS)

- [ ] **Step 1: Implement the content script with Shadow DOM**

Replace `extension/content-script.js`:

```js
// Content script: injected into every page. Creates a Shadow DOM root,
// renders a small floating panel, listens for state updates from the
// service worker.

(function () {
  if (window.__smurovProxyInjected) return;
  window.__smurovProxyInjected = true;

  // Create the host element and shadow root.
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;bottom:16px;right:16px;z-index:2147483647;width:0;height:0;";
  document.documentElement.appendChild(host);
  const shadow = host.attachShadow({ mode: "open" });

  // Inject the panel HTML + styles.
  shadow.innerHTML = `
    <style>
      :host { all: initial; }
      .panel {
        position: fixed; bottom: 16px; right: 16px;
        background: #0b0f1a; color: #e8eaf0;
        border: 1px solid #2a3042; border-radius: 8px;
        padding: 10px 14px;
        font-family: -apple-system, system-ui, sans-serif;
        font-size: 13px; line-height: 1.4;
        box-shadow: 0 4px 16px rgba(0,0,0,0.4);
        max-width: 280px;
        opacity: 0;
        transition: opacity 0.2s;
      }
      .panel.visible { opacity: 1; }
      .panel.collapsed { padding: 8px 12px; }
      .row { display: flex; align-items: center; gap: 8px; }
      .icon { width: 14px; height: 14px; border-radius: 50%; flex-shrink: 0; }
      .icon.green { background: #22c55e; }
      .icon.gray  { background: #6b7280; }
      .icon.red   { background: #ef4444; }
      .icon.yellow { background: #eab308; }
      .label { font-weight: 500; }
      .actions { display: flex; gap: 6px; margin-top: 8px; }
      button {
        background: #3b82f6; color: #fff; border: none;
        padding: 6px 10px; border-radius: 4px; cursor: pointer;
        font-size: 12px; font-weight: 500;
      }
      button.dismiss { background: #374151; }
      .hint { color: #9ca3af; font-size: 11px; margin-top: 4px; }
    </style>
    <div class="panel collapsed" id="panel">
      <div class="row">
        <div class="icon gray" id="icon"></div>
        <div class="label" id="label">…</div>
      </div>
      <div id="actions" class="actions" style="display:none;"></div>
      <div id="hint" class="hint" style="display:none;"></div>
    </div>
  `;

  const panel = shadow.getElementById("panel");
  const iconEl = shadow.getElementById("icon");
  const labelEl = shadow.getElementById("label");
  const actionsEl = shadow.getElementById("actions");
  const hintEl = shadow.getElementById("hint");

  // Render a state object: { state, host, ... }
  function render(s) {
    panel.classList.add("visible");
    actionsEl.innerHTML = "";
    actionsEl.style.display = "none";
    hintEl.style.display = "none";
    panel.classList.add("collapsed");

    switch (s.state) {
      case "down":
        iconEl.className = "icon red";
        labelEl.textContent = "Daemon not running";
        hintEl.textContent = "Open the Smurov Proxy desktop app";
        hintEl.style.display = "block";
        panel.classList.remove("collapsed");
        break;
      case "proxied":
        iconEl.className = "icon green";
        labelEl.textContent = `✓ ${s.host} proxied`;
        break;
      case "discovering":
        iconEl.className = "icon yellow";
        labelEl.textContent = `${s.host} · discovering…`;
        break;
      case "add":
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} not in proxy`;
        const addBtn = document.createElement("button");
        addBtn.textContent = "Add to proxy";
        addBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "add_current_site" });
        });
        actionsEl.appendChild(addBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
      case "blocked":
        iconEl.className = "icon red";
        labelEl.textContent = `${s.host} blocked`;
        const fixBtn = document.createElement("button");
        fixBtn.textContent = "Add to proxy";
        fixBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "add_current_site_and_reload" });
        });
        actionsEl.appendChild(fixBtn);
        const dismissBtn = document.createElement("button");
        dismissBtn.className = "dismiss";
        dismissBtn.textContent = "Dismiss";
        dismissBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "dismiss_block", host: s.host });
          panel.classList.remove("visible");
        });
        actionsEl.appendChild(dismissBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
      default:
        panel.classList.remove("visible");
    }
  }

  // Initial state query.
  chrome.runtime.sendMessage({ type: "get_state" }, (state) => {
    if (state) render(state);
  });

  // Listen for push updates from service worker.
  chrome.runtime.onMessage.addListener((msg) => {
    if (msg.type === "state_update") {
      render(msg.state);
    }
  });
})();
```

- [ ] **Step 2: Manual verification**

1. Reload the extension.
2. Open any website (e.g., `https://example.com`).
3. Open DevTools console on the page. The panel won't render anything yet
   because the service worker doesn't push state updates yet (Task 14).
4. In the page's DevTools console:
   ```js
   document.querySelector('div').shadowRoot
   ```
   should return a Shadow Root. (Note: the host div is the LAST child of
   `<html>`, so `document.documentElement.lastChild.shadowRoot` is more reliable.)

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension content script + Shadow DOM panel skeleton
EOF
git add -f CHANGELOG.new.md extension/content-script.js
git commit -m "feat(extension): content script with Shadow DOM panel [skip-deploy]"
```

---

## Task 14: Extension — tab state machine in service worker

**Files:**
- Modify: `extension/service-worker.js`
- Create: `extension/lib/tldts.min.js` (download)

- [ ] **Step 1: Bundle tldts**

Download `tldts.min.js` (a small library for extracting eTLD+1 from a host)
and place it at `extension/lib/tldts.min.js`. Use the IIFE/UMD build, not
ES module:

```bash
curl -L -o extension/lib/tldts.min.js https://cdn.jsdelivr.net/npm/tldts@6.1.16/dist/index.umd.min.js
```

Verify the file exists and is a few dozen KB.

- [ ] **Step 2: Implement state machine in service worker**

Replace `extension/service-worker.js`:

```js
import { daemonClient, setToken, clearToken } from "./lib/daemon-client.js";

// Bundled tldts loaded via importScripts (Manifest V3 service workers
// support importScripts only at top level).
importScripts("lib/tldts.min.js");

// tldts is exposed as `tldts` global by the UMD build.
const getDomain = (host) => self.tldts.getDomain(host) || host;

// Per-tab state.
// Map<tabId, {host, state, siteId}>
const tabState = new Map();

// ---------- helpers ----------

function pushStateToTab(tabId) {
  const state = tabState.get(tabId);
  if (!state) return;
  chrome.tabs.sendMessage(tabId, { type: "state_update", state }).catch(() => {});
}

async function refreshTabState(tabId, url) {
  if (!url) return;
  let urlObj;
  try { urlObj = new URL(url); } catch { return; }
  if (urlObj.protocol !== "http:" && urlObj.protocol !== "https:") {
    tabState.delete(tabId);
    return;
  }
  const fullHost = urlObj.hostname;
  const host = getDomain(fullHost);

  const r = await daemonClient.match(host);
  if (!r.ok) {
    tabState.set(tabId, { state: "down", host });
    pushStateToTab(tabId);
    return;
  }
  const data = r.data;
  if (!data.in_catalog) {
    tabState.set(tabId, { state: "add", host });
  } else if (data.proxy_enabled) {
    tabState.set(tabId, { state: "proxied", host, siteId: data.site_id });
  } else {
    tabState.set(tabId, { state: "add", host, siteId: data.site_id });
  }
  pushStateToTab(tabId);
}

// ---------- tab events ----------

chrome.tabs.onActivated.addListener(async (info) => {
  try {
    const tab = await chrome.tabs.get(info.tabId);
    refreshTabState(tab.id, tab.url);
  } catch {}
});

chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (change.url || change.status === "complete") {
    refreshTabState(tabId, tab.url);
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  tabState.delete(tabId);
});

// ---------- messages ----------

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "set_token") {
    setToken(msg.token).then(async () => {
      const ok = await daemonClient.ping();
      sendResponse({ ok });
      // Refresh all tabs after pairing.
      chrome.tabs.query({}, (tabs) => tabs.forEach((t) => refreshTabState(t.id, t.url)));
    });
    return true;
  }

  if (msg.type === "clear_token") {
    clearToken().then(() => sendResponse({ ok: true }));
    return true;
  }

  if (msg.type === "get_state") {
    const tabId = sender.tab?.id;
    sendResponse(tabState.get(tabId) || { state: "idle" });
    return false;
  }

  if (msg.type === "ping_daemon") {
    daemonClient.ping().then((up) => sendResponse({ up }));
    return true;
  }

  // Handlers for "add_current_site", "add_current_site_and_reload",
  // "dismiss_block" arrive in Task 15+.
});

console.log("[smurov-proxy] service worker loaded");
```

- [ ] **Step 3: Manual verification**

1. Reload the extension.
2. Visit a non-catalog site like `https://example.com`. Within ~1s the
   panel should appear with "example.com not in proxy" + "Add to proxy" button
   (button click does nothing yet, that's Task 15).
3. Visit `https://habr.com` (assuming you've added it via the desktop client
   already). Panel should show "✓ habr.com proxied".
4. Stop the daemon (kill the desktop client). Visit any new page. Panel
   should show "Daemon not running".
5. Restart daemon, visit a new page. Panel should reflect catalog state.

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension tab state machine — отслеживает текущий site и пушит state в content script
EOF
git add -f CHANGELOG.new.md extension/service-worker.js extension/lib/tldts.min.js
git commit -m "feat(extension): tab state machine [skip-deploy]"
```

---

## Task 15: Extension — Flow 1 (Add site click)

**Files:**
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Add the message handlers**

Find the `chrome.runtime.onMessage.addListener` block in `service-worker.js`
and add new cases:

```js
  if (msg.type === "add_current_site") {
    handleAddCurrentSite(sender.tab);
    return false;
  }
```

Add the implementation function before the `console.log` line at the bottom:

```js
async function handleAddCurrentSite(tab) {
  if (!tab || !tab.url) return;
  let host;
  try {
    host = getDomain(new URL(tab.url).hostname);
  } catch {
    return;
  }

  // Show "discovering" immediately.
  tabState.set(tab.id, { state: "discovering", host });
  pushStateToTab(tab.id);

  const r = await daemonClient.add(host, host);  // label = host as fallback
  if (!r.ok) {
    tabState.set(tab.id, { state: "add", host });
    pushStateToTab(tab.id);
    return;
  }
  const siteId = r.data.site_id;
  tabState.set(tab.id, { state: "discovering", host, siteId });
  pushStateToTab(tab.id);

  // Discovery hook is enabled in Task 16. For now, just transition to
  // "proxied" after a short delay.
  setTimeout(() => {
    tabState.set(tab.id, { state: "proxied", host, siteId });
    pushStateToTab(tab.id);
  }, 1500);
}
```

- [ ] **Step 2: Manual verification**

1. Reload the extension.
2. Visit `https://example.com` (not in catalog).
3. Panel shows "example.com not in proxy" + Add button.
4. Click Add. Panel briefly shows "discovering…" (yellow).
5. After ~1.5s, panel shows "✓ example.com proxied" (green).
6. Confirm in the desktop client → Browser Extension tab → that the daemon
   actually called the server. Or check the server DB:
   ```bash
   ssh root@95.181.162.242 'sqlite3 /var/lib/docker/volumes/smurov-proxy-data/_data/data.db "SELECT * FROM sites WHERE primary_domain=\"example.com\";"'
   ```

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension Flow 1: клик "Add to proxy" → /sites/add → state→discovering
EOF
git add -f CHANGELOG.new.md extension/service-worker.js
git commit -m "feat(extension): add site click handler [skip-deploy]"
```

---

## Task 16: Extension — Flow 1 discovery via webRequest

**Files:**
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Add discovery state and webRequest hook**

In `service-worker.js`, add a discovery state map at the top:

```js
// Map<tabId, { siteId, host, queue: Set<string>, flushTimer }>
const discoveryState = new Map();
```

Add helper functions:

```js
function startDiscovery(tabId, host, siteId) {
  // Clean up any prior discovery for this tab.
  stopDiscovery(tabId);
  const state = {
    siteId,
    host,
    queue: new Set(),
    flushTimer: null,
  };
  discoveryState.set(tabId, state);
  state.flushTimer = setInterval(() => flushDiscovery(tabId), 5000);
}

function stopDiscovery(tabId) {
  const s = discoveryState.get(tabId);
  if (!s) return;
  if (s.flushTimer) clearInterval(s.flushTimer);
  flushDiscovery(tabId);  // final flush
  discoveryState.delete(tabId);
}

async function flushDiscovery(tabId) {
  const s = discoveryState.get(tabId);
  if (!s || s.queue.size === 0) return;
  const domains = Array.from(s.queue);
  s.queue.clear();
  await daemonClient.discover(s.siteId, domains);
}
```

Modify `handleAddCurrentSite` to start discovery instead of the fake setTimeout:

```js
async function handleAddCurrentSite(tab) {
  if (!tab || !tab.url) return;
  let host;
  try {
    host = getDomain(new URL(tab.url).hostname);
  } catch {
    return;
  }

  tabState.set(tab.id, { state: "discovering", host });
  pushStateToTab(tab.id);

  const r = await daemonClient.add(host, host);
  if (!r.ok) {
    tabState.set(tab.id, { state: "add", host });
    pushStateToTab(tab.id);
    return;
  }
  const siteId = r.data.site_id;
  tabState.set(tab.id, { state: "discovering", host, siteId });
  pushStateToTab(tab.id);
  startDiscovery(tab.id, host, siteId);
}
```

Add the webRequest hook (top-level, near the other listeners):

```js
chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    if (details.tabId < 0) return;  // background fetch
    const disc = discoveryState.get(details.tabId);
    if (!disc) return;
    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    if (urlObj.protocol !== "https:" && urlObj.protocol !== "http:") return;
    const host = urlObj.hostname;
    // Skip localhost / IP literals / our own daemon.
    if (host === "127.0.0.1" || host === "localhost") return;
    disc.queue.add(host);
  },
  { urls: ["<all_urls>"] }
);
```

Hook tab navigation to STOP discovery when user leaves the site:

```js
chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (change.url) {
    const disc = discoveryState.get(tabId);
    if (disc) {
      let newHost;
      try { newHost = getDomain(new URL(tab.url).hostname); } catch { newHost = ""; }
      if (newHost !== disc.host) {
        stopDiscovery(tabId);
        // Now that discovery is done, transition to plain "proxied" state.
        tabState.set(tabId, { state: "proxied", host: disc.host, siteId: disc.siteId });
      }
    }
    refreshTabState(tabId, tab.url);
  }
});
```

(Note: this REPLACES the existing `chrome.tabs.onUpdated.addListener` from
Task 14. Make sure you replace, not add a second listener.)

Hook tab close to stop discovery:

```js
chrome.tabs.onRemoved.addListener((tabId) => {
  stopDiscovery(tabId);
  tabState.delete(tabId);
});
```

(Same: replace the existing `onRemoved` from Task 14.)

- [ ] **Step 2: Manual verification**

1. Reload the extension.
2. Visit `https://habr.com` if not already added — click Add.
3. Browse the site, scroll, click links, open articles.
4. After 5+ seconds, check the server DB:
   ```bash
   ssh root@95.181.162.242 'sqlite3 /var/lib/docker/volumes/smurov-proxy-data/_data/data.db "SELECT domain FROM site_domains WHERE site_id=(SELECT id FROM sites WHERE primary_domain=\"habr.com\");"'
   ```
   You should see `habr.com` plus several discovered subdomains/CDN hosts
   like `habrastorage.org`, `habr-image.s3.eu-central-1.amazonaws.com`,
   etc. (the exact list depends on what loaded).
5. Navigate to a different site (e.g., `https://wikipedia.org`). Discovery
   for habr should stop automatically (final flush) and the panel should
   re-render for wikipedia.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension discovery: webRequest hook → накопление доменов → батч flush в /sites/discover
EOF
git add -f CHANGELOG.new.md extension/service-worker.js
git commit -m "feat(extension): webRequest discovery and batch flush [skip-deploy]"
```

---

## Task 17: Extension — Flow 2 (sub-resource fail = strong signal)

**Files:**
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Add the error hook for sub-resources on proxied sites**

In `service-worker.js`, add the suspicious error allowlist near the top:

```js
const SUSPICIOUS_ERRORS = new Set([
  "net::ERR_CONNECTION_RESET",
  "net::ERR_CONNECTION_TIMED_OUT",
  "net::ERR_NAME_NOT_RESOLVED",
  "net::ERR_CONNECTION_CLOSED",
  "net::ERR_SSL_PROTOCOL_ERROR",
  "net::ERR_CONNECTION_REFUSED",
]);
```

Add the hook for sub-resource failures (NOT main_frame — that's Flow 3):

```js
chrome.webRequest.onErrorOccurred.addListener(
  (details) => {
    if (details.tabId < 0) return;
    if (details.type === "main_frame") return;  // handled by Flow 3
    if (!SUSPICIOUS_ERRORS.has(details.error)) return;

    const tab = tabState.get(details.tabId);
    if (!tab || tab.state !== "proxied") return;
    if (!tab.siteId) return;

    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    const failedHost = urlObj.hostname;
    const failedSld = getDomain(failedHost);
    if (failedSld === tab.host) return;  // same SLD = should already be covered

    // Strong signal: a request from a proxied page failed at a non-covered
    // host with a block-like error. Push it to discover IMMEDIATELY.
    daemonClient.discover(tab.siteId, [failedHost]);
  },
  { urls: ["<all_urls>"] }
);
```

- [ ] **Step 2: Manual verification**

This is hard to test deterministically. The setup:

1. Add a site to the catalog whose primary domain works but whose CDN is
   blocked (rare in practice). Or temporarily set a `/etc/hosts` entry
   like `127.0.0.1 some-cdn.example.com` so requests fail with
   `ERR_CONNECTION_REFUSED`.
2. Load the proxied page, observe the failed sub-resource.
3. Check daemon logs (`tail -f ~/Library/Logs/SmurovProxy/daemon.log` or
   wherever logs go) for the `add_domain` op.
4. Check server DB:
   ```bash
   ssh root@95.181.162.242 'sqlite3 ... "SELECT * FROM site_domains WHERE site_id=...;"'
   ```
   The failing host should appear.

If you can't reproduce naturally, manually trigger by running this in the
service worker DevTools:

```js
chrome.tabs.query({active: true, currentWindow: true}, (tabs) => {
  tabState.set(tabs[0].id, { state: "proxied", host: "habr.com", siteId: 47 });
  daemonClient.discover(47, ["habr-test-domain.invalid"]);
});
```

Then check the DB.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension Flow 2: failed sub-resource на проксируемом сайте → immediate discover
EOF
git add -f CHANGELOG.new.md extension/service-worker.js
git commit -m "feat(extension): sub-resource error strong-signal flush [skip-deploy]"
```

---

## Task 18: Extension — Flow 3 (top-level block detection)

**Files:**
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Add the top-level error hook with silent verification**

In `service-worker.js`, add a handler for main_frame errors. Place it
right after the existing `onErrorOccurred` listener from Task 17 (but
make a separate listener to keep the logic clear):

```js
// In-memory dismissals: { host: dismissedAtMs }
// Persisted to chrome.storage.local under "block_dismissals" in Task 19.
const blockDismissals = new Map();
const DISMISS_TTL_MS = 24 * 60 * 60 * 1000;

async function loadDismissals() {
  const stored = await chrome.storage.local.get("block_dismissals");
  const data = stored.block_dismissals || {};
  const now = Date.now();
  for (const [host, at] of Object.entries(data)) {
    if (now - at < DISMISS_TTL_MS) {
      blockDismissals.set(host, at);
    }
  }
}
loadDismissals();

async function persistDismissal(host) {
  blockDismissals.set(host, Date.now());
  const out = {};
  for (const [h, at] of blockDismissals) out[h] = at;
  await chrome.storage.local.set({ block_dismissals: out });
}

chrome.webRequest.onErrorOccurred.addListener(
  async (details) => {
    if (details.tabId < 0) return;
    if (details.type !== "main_frame") return;
    if (!SUSPICIOUS_ERRORS.has(details.error)) return;

    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    const failedHost = getDomain(urlObj.hostname);
    if (blockDismissals.has(failedHost)) return;

    // Verify: ask daemon to test the URL through the tunnel.
    const r = await daemonClient.test(details.url);
    if (!r.ok || !r.data.likely_blocked) return;

    // Push the "blocked" state to the failed tab. The content script
    // will render the banner.
    tabState.set(details.tabId, { state: "blocked", host: failedHost });
    pushStateToTab(details.tabId);
  },
  { urls: ["<all_urls>"] }
);
```

Add message handlers for the banner buttons:

```js
  if (msg.type === "add_current_site_and_reload") {
    handleAddSiteAndReload(sender.tab);
    return false;
  }

  if (msg.type === "dismiss_block") {
    persistDismissal(msg.host);
    return false;
  }
```

Implementation:

```js
async function handleAddSiteAndReload(tab) {
  if (!tab) return;
  const state = tabState.get(tab.id);
  if (!state || state.state !== "blocked") return;

  const r = await daemonClient.add(state.host, state.host);
  if (!r.ok) {
    console.warn("[smurov-proxy] add failed:", r);
    return;
  }
  const siteId = r.data.site_id;

  // Brief pause to let daemon's PAC update.
  await new Promise((res) => setTimeout(res, 500));

  tabState.set(tab.id, { state: "proxied", host: state.host, siteId });
  pushStateToTab(tab.id);

  // Reload the tab so the request goes through the proxy this time.
  chrome.tabs.reload(tab.id);
}
```

- [ ] **Step 2: Manual verification**

1. With the daemon running, reload the extension.
2. Try opening a known-blocked-in-Russia site that you have NOT yet added
   to the proxy (e.g., temporarily remove `youtube.com` from the catalog).
3. The page should fail to load. After ~1-2 seconds (silent verify time),
   the panel should show "youtube.com blocked" with "Add to proxy" + "Dismiss" buttons.
4. Click "Add to proxy". Tab reloads and now loads YouTube via the proxy.
5. Repeat with another blocked site, click "Dismiss" instead. Refresh that
   site within 24h — banner should NOT reappear.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Extension Flow 3: detect top-level block → verify через /sites/test → banner с одним кликом
EOF
git add -f CHANGELOG.new.md extension/service-worker.js
git commit -m "feat(extension): block detection with silent verification [skip-deploy]"
```

---

## Task 19: Extension — README install instructions

**Files:**
- Modify: `extension/README.md`
- Modify: `README.md` (root)

- [ ] **Step 1: Update extension README with full install + usage**

Replace `extension/README.md`:

```markdown
# Smurov Proxy — Browser Extension

Companion to the Smurov Proxy desktop client. Provides per-tab proxy controls,
automatic domain discovery, and ISP-block detection.

**Requires:** the Smurov Proxy desktop client running on the same machine.

## Install (development)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Toggle on "Developer mode" (top right).
3. Click "Load unpacked" and select this `extension/` folder.
4. Click the extension icon in the toolbar.
5. Open the Smurov Proxy desktop client → "Browser Extension" tab.
6. Copy the token shown there.
7. Paste it into the extension popup → click "Pair".

The extension is now connected to your local daemon and will start
working on every tab.

## What it does

- **In-page panel** (bottom-right corner of every page) shows the proxy state
  for the current site:
  - ✓ green: site is being proxied
  - gray: site is not in your catalog — click "Add to proxy" to add it
  - red: ISP block detected — click "Add to proxy" to fix
  - red ("daemon not running"): start the desktop client
- **Automatic discovery:** when you add a new site, the extension watches
  what your browser loads on that site for the duration of your visit and
  pushes any new domains to the catalog. Subsequent visits (and other
  users) get the enriched domain list automatically.
- **Block detection:** if a top-level navigation fails with a suspicious
  error code, the extension silently verifies via the proxy whether the
  site loads through the tunnel. If yes, it offers to add the site with
  one click.

## Privacy

The extension reads request URLs (network metadata, not page content) and
forwards summaries only to your local Smurov Proxy daemon at
`127.0.0.1:9090`. Nothing is sent directly to any remote server.

## Architecture

See [`docs/superpowers/specs/2026-04-08-browser-extension-design.md`](../docs/superpowers/specs/2026-04-08-browser-extension-design.md).

## Publishing

Future Chrome Web Store submission. For now, sideload only.
```

- [ ] **Step 2: Add a pointer in the root README**

If `README.md` exists in the project root, add a section near the bottom:

```markdown
## Browser extension (optional)

A companion Chrome/Edge/Brave extension provides per-tab proxy controls and
automatic domain discovery. See [`extension/README.md`](extension/README.md)
for sideload instructions. The extension is currently sideload-only;
Chrome Web Store publication is planned after testing.
```

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## improvement
Extension README с install + usage инструкциями для sideload
EOF
git add -f CHANGELOG.new.md extension/README.md README.md
git commit -m "docs(extension): install and usage instructions [skip-deploy]"
```

---

## Self-Review

The plan covers all spec sections:

- ✅ **add_domain server op + auth check** → Task 1
- ✅ **Daemon device key persistence** → Task 2
- ✅ **Daemon token store + middleware** → Task 3
- ✅ **Daemon SyncClient → server** → Task 4
- ✅ **Daemon in-memory my_sites cache** → Task 5
- ✅ **Daemon Manager (orchestration)** → Task 6
- ✅ **Daemon /sites/match, /sites/add, /sites/discover** → Task 7
- ✅ **Daemon /sites/test (SOCKS5 HEAD)** → Task 8
- ✅ **Desktop client Browser Extension tab + IPC** → Task 9
- ✅ **Extension scaffold (manifest + folders)** → Task 10
- ✅ **Extension service worker + daemon client** → Task 11
- ✅ **Extension popup pairing** → Task 12
- ✅ **Extension content script + Shadow DOM panel** → Task 13
- ✅ **Extension tab state machine** → Task 14
- ✅ **Extension Flow 1 (Add site click)** → Task 15
- ✅ **Extension Flow 1 (discovery via webRequest)** → Task 16
- ✅ **Extension Flow 2 (sub-resource fail)** → Task 17
- ✅ **Extension Flow 3 (top-level block + verify + banner)** → Task 18
- ✅ **README + sideload instructions** → Task 19

## Out of Scope (per spec)

Not implemented in this plan, deferred per spec's "Non-Goals" / "Out of Scope" sections:

- Firefox / Safari extension builds
- Chrome Web Store publication (manual when ready)
- Admin UI for moderating user-submitted sites or discovered domains
- Token auth on existing daemon `/tun/*`, `/pac/*`, `/transport` endpoints
- Per-user discovery preferences (always-on for new sites)
- Native messaging (paste-pairing chosen for v1)
- Disabling proxy for one specific subdomain on a proxied site
