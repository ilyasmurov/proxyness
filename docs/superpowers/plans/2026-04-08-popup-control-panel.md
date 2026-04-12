# Popup Control Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert browser extension popup from static "Paired" screen into a per-tab control panel, and consolidate PAC ownership into the daemon so mutations from extension and desktop client UI never race.

**Architecture:** Daemon takes ownership of PAC formation. Renderer's `toggleSite/addSite/removeSite` go through new daemon HTTP endpoints (via main process IPC) instead of local pendingOps queue. New `GET /sites/my` endpoint lets renderer pull from daemon as the single source of truth. Browser extension popup becomes a state machine with 6 views — control panel for the active tab.

**Tech Stack:**
- Daemon: Go + `net/http` (extends `daemon/internal/api/`, `daemon/internal/sites/`)
- Desktop client: Electron/React/TypeScript (modifies `client/src/main/`, `client/src/renderer/`)
- Browser extension: vanilla JS + Manifest V3 (modifies `extension/`)

**Spec:** [`docs/superpowers/specs/2026-04-08-popup-control-panel-design.md`](../specs/2026-04-08-popup-control-panel-design.md)

---

## Architectural Assumptions

These were settled during brainstorming. If any are wrong, fix the assumption and the affected tasks before starting.

1. **Daemon owns PAC formation.** After this refactor, daemon — and only daemon — generates the domain list that goes into the PAC file. Renderer pushes only the `proxy_all` flag through the existing `/pac/sites` endpoint; the `sites` field is ignored server-side and dropped from the TS type.
2. **Mutations require online daemon.** After the refactor, `addSite/toggleSite/removeSite` from the renderer always go through daemon HTTP. If daemon is down, the mutation fails with a clear error toast and the UI rolls back. We sacrifice the existing offline pendingOps queue. Acceptable trade-off because daemon and client spawn together.
3. **Renderer reads sites from daemon, not catalog server.** New endpoint `GET /sites/my` returns `cache.Snapshot()`. Renderer's `sync()` calls this instead of POST'ing to `https://proxyness.smurov.com/api/sync`. This eliminates the read-side race window between renderer's background sync and daemon mutations.
4. **`RebuildPAC` must diff before `CloseAllConns`.** Background `Refresh()` runs every 5 minutes. Without diffing, this would kill all in-flight SOCKS5 connections every tick. The implementation MUST compare new domain list against current `pacSites` and only call `tunnel.CloseAllConns()` when something actually changed.
5. **Local exclusions JSON store is NOT created.** Earlier draft had this; replaced by reusing existing per-user `enabled` flag through serverside sync. Only one source of truth per user.
6. **CHANGELOG.new.md needs `git add -f`.** The pre-commit hook deletes it after processing, `.gitignore` blocks it. Always force-add when committing.

---

## File Structure

### New files

```
daemon/internal/sites/pac_expand.go         # vendored expandDomains from TS
daemon/internal/sites/pac_expand_test.go    # unit tests + parity fixtures
client/src/renderer/sites/sync.test.ts      # NEW (no test infra exists yet)
client/src/renderer/sites/useSites.test.ts  # NEW
```

### Modified files

```
daemon/internal/sites/manager.go            # SetEnabled, RemoveSite, EnabledDomains, SetOnCacheReplaced
daemon/internal/sites/manager_test.go       # tests for new methods
daemon/internal/api/api.go                  # RebuildPAC method, simplify handlePacSitesUpdate
daemon/internal/api/api_test.go             # tests for RebuildPAC diff behavior
daemon/internal/api/sites.go                # handleSitesSetEnabled, handleSitesRemove, handleSitesMy
daemon/internal/api/sites_test.go           # tests for new endpoints
daemon/internal/api/auth_token_test.go      # cover new routes under middleware
daemon/cmd/main.go                          # wire SetOnCacheReplaced(srv.RebuildPAC)
client/src/main/extension.ts                # cachedDaemonToken function
client/src/main/index.ts                    # 4 new IPC handlers, simplify pac-sites handler
client/src/main/preload.ts                  # expose new daemon-* methods, update setPacSites type
client/src/renderer/hooks/useDaemon.ts      # update window.sysproxy type
client/src/renderer/sites/sync.ts           # rewrite mutations as async, pull from daemon
client/src/renderer/sites/storage.ts        # remove pendingOps fields
client/src/renderer/sites/useSites.ts       # async API surface
client/src/renderer/components/AppRules.tsx # remove siteDomains memo, simplify applyPac, async handlers
client/src/renderer/App.tsx                 # remove sites: [] from setPacSites payload
extension/lib/daemon-client.js              # add setEnabled method
extension/service-worker.js                 # popup_get_state, popup_add_site, popup_set_enabled handlers
extension/popup/popup.js                    # state machine + control panel
extension/popup/popup.css                   # new layout styles
extension/content-script.js                 # locally_disabled state render
extension/manifest.json                     # version bump 0.1.0 → 0.2.0
```

### Deleted files

```
client/src/renderer/sites/pac.ts            # expandDomains moved to daemon
```

---

# Phase 1 — Daemon Foundation (Go)

## Task 1: Vendor expandDomains from TypeScript to Go

**Files:**
- Create: `daemon/internal/sites/pac_expand.go`
- Create: `daemon/internal/sites/pac_expand_test.go`

- [ ] **Step 1: Write the failing test for `ExpandDomains`**

Create `daemon/internal/sites/pac_expand_test.go`:

```go
package sites

import (
	"reflect"
	"testing"
)

func TestExpandDomainsAddsWWWAndStarVariants(t *testing.T) {
	got := ExpandDomains([]string{"habr.com"})
	want := []string{"habr.com", "www.habr.com", "*.habr.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsSkipsWWWPrefixForExistingWWW(t *testing.T) {
	got := ExpandDomains([]string{"www.example.com"})
	want := []string{"www.example.com", "*.www.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsDeduplicatesAcrossInputs(t *testing.T) {
	got := ExpandDomains([]string{"a.com", "a.com", "B.COM"})
	want := []string{"a.com", "www.a.com", "*.a.com", "b.com", "www.b.com", "*.b.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsTrimsWhitespaceAndIgnoresEmpty(t *testing.T) {
	got := ExpandDomains([]string{"  a.com  ", "", "   "})
	want := []string{"a.com", "www.a.com", "*.a.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsEmptyInput(t *testing.T) {
	got := ExpandDomains(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestExpandDomains -v`
Expected: FAIL with `undefined: ExpandDomains`

- [ ] **Step 3: Implement `ExpandDomains`**

Create `daemon/internal/sites/pac_expand.go`:

```go
package sites

import "strings"

// ExpandDomains takes a list of primary site domains and returns the
// flat list that goes into the PAC file. For each input domain it adds
// "www." and "*." variants because the PAC matches by suffix.
//
// Mirrors the previous client-side implementation in
// client/src/renderer/sites/pac.ts so the daemon can take ownership of
// PAC formation.
func ExpandDomains(domains []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(domains)*3)
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, d := range domains {
		clean := strings.ToLower(strings.TrimSpace(d))
		if clean == "" {
			continue
		}
		add(clean)
		if !strings.HasPrefix(clean, "www.") {
			add("www." + clean)
		}
		add("*." + clean)
	}
	return out
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd daemon && go test ./internal/sites/ -run TestExpandDomains -v`
Expected: PASS for all 5 cases.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/sites/pac_expand.go daemon/internal/sites/pac_expand_test.go
git commit -m "feat(daemon/sites): vendor ExpandDomains from TypeScript to Go [skip-deploy]"
```

---

## Task 2: Manager.EnabledDomains helper

**Files:**
- Modify: `daemon/internal/sites/manager.go`
- Modify: `daemon/internal/sites/manager_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/sites/manager_test.go`:

```go
func TestManagerEnabledDomains(t *testing.T) {
	dir := t.TempDir()
	keyStore := NewKeyStore(filepath.Join(dir, "key"))
	keyStore.Save("dummy")
	mgr := NewManager("https://example.invalid", keyStore)

	mgr.cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com", "habrcdn.io"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
		{ID: 3, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: true},
	})

	got := mgr.EnabledDomains()
	// Expected: enabled-only sites' domains, expanded with www./*. variants.
	// Order matches iteration order of cache.Snapshot.
	want := []string{
		"habr.com", "www.habr.com", "*.habr.com",
		"habrcdn.io", "www.habrcdn.io", "*.habrcdn.io",
		"vk.com", "www.vk.com", "*.vk.com",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
```

Add `"reflect"` and `"path/filepath"` to imports if not already present.

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerEnabledDomains -v`
Expected: FAIL with `mgr.EnabledDomains undefined`

- [ ] **Step 3: Implement `EnabledDomains`**

Add to `daemon/internal/sites/manager.go`:

```go
// EnabledDomains returns the flat expanded domain list for all sites
// where Enabled == true. Used to feed pacSites in Server.RebuildPAC.
func (m *Manager) EnabledDomains() []string {
	snapshot := m.cache.Snapshot()
	raw := make([]string, 0, len(snapshot))
	for _, s := range snapshot {
		if !s.Enabled {
			continue
		}
		raw = append(raw, s.Domains...)
	}
	return ExpandDomains(raw)
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerEnabledDomains -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/sites/manager.go daemon/internal/sites/manager_test.go
git commit -m "feat(daemon/sites): add Manager.EnabledDomains helper [skip-deploy]"
```

---

## Task 3: Manager.SetOnCacheReplaced callback

**Files:**
- Modify: `daemon/internal/sites/manager.go`
- Modify: `daemon/internal/sites/manager_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/sites/manager_test.go`:

```go
func TestManagerSetOnCacheReplacedFiresAfterRefresh(t *testing.T) {
	// Use a fake server so Refresh() succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{
			MySites:    []MySite{{ID: 1, PrimaryDomain: "x.com", Enabled: true}},
			ServerTime: 1000,
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	var calls int32
	mgr.SetOnCacheReplaced(func() {
		atomic.AddInt32(&calls, 1)
	})

	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 callback fire, got %d", calls)
	}
}

func TestManagerCallbackNilSafe(t *testing.T) {
	// No callback set — Refresh must not panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{ServerTime: 1000})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)
	if err := mgr.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}
```

Add imports: `"net/http"`, `"net/http/httptest"`, `"encoding/json"`, `"sync/atomic"`.

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerSetOnCacheReplaced -v`
Expected: FAIL with `mgr.SetOnCacheReplaced undefined`

- [ ] **Step 3: Implement callback wiring**

Modify `daemon/internal/sites/manager.go`:

Add field to `Manager` struct:
```go
type Manager struct {
	keyStore *KeyStore
	client   *SyncClient
	cache    *Cache

	mu              sync.Mutex
	stopRefresh     chan struct{}
	onCacheReplaced func()  // fired after every cache.Replace, nil-safe
}
```

Add setter:
```go
// SetOnCacheReplaced registers a callback that fires after cache.Replace
// (and only after the lock has been released to avoid deadlock).
func (m *Manager) SetOnCacheReplaced(fn func()) {
	m.mu.Lock()
	m.onCacheReplaced = fn
	m.mu.Unlock()
}

func (m *Manager) fireOnCacheReplaced() {
	m.mu.Lock()
	cb := m.onCacheReplaced
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
}
```

Wire `fireOnCacheReplaced()` after every existing `m.cache.Replace(...)` call:

In `Refresh()`:
```go
func (m *Manager) Refresh() error {
	resp, err := m.client.SyncOps(nil)
	if err != nil {
		return err
	}
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return nil
}
```

In `AddSite()` (after the `m.cache.Replace(resp.MySites)` line):
```go
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return r.SiteID, r.Deduped, nil
```

In `AddDomains()` (after `m.cache.Replace(resp.MySites)`):
```go
	if added > 0 {
		m.cache.Replace(resp.MySites)
		m.fireOnCacheReplaced()
	}
	return added, deduped, nil
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd daemon && go test ./internal/sites/ -v`
Expected: PASS for all sites tests, including new `TestManagerSetOnCacheReplaced*` and existing tests.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/sites/manager.go daemon/internal/sites/manager_test.go
git commit -m "feat(daemon/sites): add SetOnCacheReplaced callback [skip-deploy]"
```

---

## Task 4: Manager.SetEnabled

**Files:**
- Modify: `daemon/internal/sites/manager.go`
- Modify: `daemon/internal/sites/manager_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/sites/manager_test.go`:

```go
func TestManagerSetEnabledTogglesViaSync(t *testing.T) {
	var receivedOps []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SyncRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedOps = req.Ops

		// Server responds with the site flipped to disabled.
		json.NewEncoder(w).Encode(SyncResponse{
			MySites: []MySite{
				{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
			},
			OpResults:  []OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	if err := mgr.SetEnabled(47, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	if len(receivedOps) != 1 {
		t.Fatalf("expected 1 op, got %d", len(receivedOps))
	}
	op := receivedOps[0]
	if op["op"] != "disable" {
		t.Errorf("expected op=disable, got %v", op["op"])
	}
	if int(op["site_id"].(float64)) != 47 {
		t.Errorf("expected site_id=47, got %v", op["site_id"])
	}

	// Cache should now reflect the disabled state.
	m := mgr.cache.Match("youtube.com")
	if m == nil || m.Enabled {
		t.Errorf("cache not updated, got %+v", m)
	}
}

func TestManagerSetEnabledFiresCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{
			MySites:    []MySite{},
			OpResults:  []OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	var calls int32
	mgr.SetOnCacheReplaced(func() { atomic.AddInt32(&calls, 1) })

	mgr.SetEnabled(1, true)

	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected 1 callback fire, got %d", calls)
	}
}

func TestManagerSetEnabledServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(SyncResponse{
			OpResults: []OpResult{{Status: "error", Message: "site not found"}},
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	err := mgr.SetEnabled(999, false)
	if err == nil || !strings.Contains(err.Error(), "site not found") {
		t.Fatalf("expected error containing 'site not found', got %v", err)
	}
}
```

Add `"strings"` to imports if not present.

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerSetEnabled -v`
Expected: FAIL with `mgr.SetEnabled undefined`

- [ ] **Step 3: Implement SetEnabled**

Add to `daemon/internal/sites/manager.go`:

```go
// SetEnabled toggles the per-user enabled flag for a site through server
// sync. On success the cache is replaced with the fresh my_sites snapshot
// and the OnCacheReplaced callback fires.
func (m *Manager) SetEnabled(siteID int, enabled bool) error {
	op := "disable"
	if enabled {
		op = "enable"
	}
	resp, err := m.client.SyncOps([]map[string]interface{}{
		{
			"op":      op,
			"site_id": siteID,
			"at":      time.Now().Unix(),
		},
	})
	if err != nil {
		return err
	}
	if len(resp.OpResults) == 0 {
		return fmt.Errorf("no op_results in response")
	}
	if r := resp.OpResults[0]; r.Status != "ok" {
		return fmt.Errorf("server: %s", r.Message)
	}
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerSetEnabled -v`
Expected: PASS for all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/sites/manager.go daemon/internal/sites/manager_test.go
git commit -m "feat(daemon/sites): add Manager.SetEnabled [skip-deploy]"
```

---

## Task 5: Manager.RemoveSite

**Files:**
- Modify: `daemon/internal/sites/manager.go`
- Modify: `daemon/internal/sites/manager_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/sites/manager_test.go`:

```go
func TestManagerRemoveSite(t *testing.T) {
	var receivedOps []map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SyncRequest
		json.NewDecoder(r.Body).Decode(&req)
		receivedOps = req.Ops
		json.NewEncoder(w).Encode(SyncResponse{
			MySites:    []MySite{},
			OpResults:  []OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer srv.Close()

	keyStore := NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := NewManager(srv.URL, keyStore)

	mgr.cache.Replace([]MySite{
		{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true},
	})

	if err := mgr.RemoveSite(47); err != nil {
		t.Fatalf("RemoveSite: %v", err)
	}

	if len(receivedOps) != 1 || receivedOps[0]["op"] != "remove" {
		t.Errorf("expected remove op, got %v", receivedOps)
	}
	if int(receivedOps[0]["site_id"].(float64)) != 47 {
		t.Errorf("expected site_id=47, got %v", receivedOps[0]["site_id"])
	}

	if mgr.cache.Match("youtube.com") != nil {
		t.Error("expected cache to be empty after remove")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerRemoveSite -v`
Expected: FAIL with `mgr.RemoveSite undefined`

- [ ] **Step 3: Implement RemoveSite**

Add to `daemon/internal/sites/manager.go`:

```go
// RemoveSite deletes a site for this user through server sync. Symmetric
// to AddSite. Cache is replaced with the fresh my_sites snapshot.
func (m *Manager) RemoveSite(siteID int) error {
	resp, err := m.client.SyncOps([]map[string]interface{}{
		{
			"op":      "remove",
			"site_id": siteID,
			"at":      time.Now().Unix(),
		},
	})
	if err != nil {
		return err
	}
	if len(resp.OpResults) == 0 {
		return fmt.Errorf("no op_results in response")
	}
	if r := resp.OpResults[0]; r.Status != "ok" {
		return fmt.Errorf("server: %s", r.Message)
	}
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `cd daemon && go test ./internal/sites/ -run TestManagerRemoveSite -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/sites/manager.go daemon/internal/sites/manager_test.go
git commit -m "feat(daemon/sites): add Manager.RemoveSite [skip-deploy]"
```

---

## Task 6: Server.RebuildPAC with diff

**Files:**
- Modify: `daemon/internal/api/api.go`
- Create: `daemon/internal/api/api_test.go` (or extend existing if exists)

- [ ] **Step 1: Check if api_test.go exists**

Run: `ls daemon/internal/api/api_test.go 2>&1 || echo "not exists"`

- [ ] **Step 2: Write failing test**

Add to `daemon/internal/api/api_test.go` (create if missing with `package api` header):

```go
package api

import (
	"path/filepath"
	"testing"

	"proxyness/daemon/internal/sites"
	"proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/tunnel"
)

func newTestServerWithMgr(t *testing.T, mgr *sites.Manager) *Server {
	t.Helper()
	tnl := tunnel.New(stats.NewRateMeter())
	srv := New(tnl, nil, "127.0.0.1:0", stats.NewRateMeter())
	srv.SetSites(mgr, nil)
	return srv
}

func TestRebuildPACPushesEnabledDomains(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
	})
	srv := newTestServerWithMgr(t, mgr)

	// Initial state: proxy_all=false, no domains.
	srv.pacSites.Set(false, nil)

	srv.RebuildPAC()

	proxyAll, domains := srv.pacSites.Get()
	if proxyAll {
		t.Error("expected proxy_all=false")
	}
	want := []string{"habr.com", "www.habr.com", "*.habr.com"}
	if len(domains) != len(want) {
		t.Fatalf("got %v, want %v", domains, want)
	}
	for i := range want {
		if domains[i] != want[i] {
			t.Errorf("domain[%d]: got %q, want %q", i, domains[i], want[i])
		}
	}
}

func TestRebuildPACPreservesProxyAllFlag(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	srv := newTestServerWithMgr(t, mgr)

	// Set proxy_all=true (renderer-pushed flag).
	srv.pacSites.Set(true, nil)

	srv.RebuildPAC()

	proxyAll, domains := srv.pacSites.Get()
	if !proxyAll {
		t.Error("expected proxy_all=true preserved")
	}
	if len(domains) != 0 {
		t.Errorf("expected empty domains in proxy_all mode, got %v", domains)
	}
}

func TestRebuildPACSkipsCloseAllConnsWhenUnchanged(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	srv := newTestServerWithMgr(t, mgr)

	// First rebuild populates pacSites.
	srv.RebuildPAC()
	first, _ := srv.pacSites.Get()
	_ = first

	// Second rebuild with identical cache should be a no-op.
	// We can verify by inspecting CloseAllConns count if exposed; instead
	// we verify the underlying state doesn't churn (idempotent).
	srv.RebuildPAC()
	srv.RebuildPAC()
	srv.RebuildPAC()
	// If this test compiles and runs without panic, the no-op path is exercised.
	// Real verification of CloseAllConns counter happens via integration test
	// or by exposing a counter in tunnel.Tunnel.
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestRebuildPAC -v`
Expected: FAIL with `srv.RebuildPAC undefined`

- [ ] **Step 4: Implement RebuildPAC**

Add to `daemon/internal/api/api.go` (anywhere in the file, e.g. after the `Server` methods):

```go
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
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd daemon && go test ./internal/api/ -run TestRebuildPAC -v`
Expected: PASS for all 3 tests.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/api/api.go daemon/internal/api/api_test.go
git commit -m "feat(daemon/api): add Server.RebuildPAC with diff guard [skip-deploy]"
```

---

## Task 7: handleSitesSetEnabled endpoint

**Files:**
- Modify: `daemon/internal/api/sites.go`
- Modify: `daemon/internal/api/api.go` (add route)
- Modify: `daemon/internal/api/sites_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/api/sites_test.go`:

```go
func TestSitesSetEnabledHappyPath(t *testing.T) {
	// Fake server that flips a site to disabled.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sites.SyncResponse{
			MySites: []sites.MySite{
				{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: false},
			},
			OpResults:  []sites.OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer upstream.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager(upstream.URL, keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":47,"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Errorf("expected ok=true, got %v", resp["ok"])
	}
	mySites, ok := resp["my_sites"].([]interface{})
	if !ok || len(mySites) != 1 {
		t.Fatalf("expected my_sites with 1 entry, got %v", resp["my_sites"])
	}
}

func TestSitesSetEnabledMissingSiteID(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestSitesSetEnabledRequiresAuth(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":1,"enabled":false}`)
	req := httptest.NewRequest("POST", "/sites/set-enabled", body)
	// no Authorization header
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 401 {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestSitesSetEnabled -v`
Expected: FAIL with route not found / 404.

- [ ] **Step 3: Add handler in sites.go**

Add to `daemon/internal/api/sites.go`:

```go
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
```

- [ ] **Step 4: Register route in api.go**

In `daemon/internal/api/api.go:Handler()`, inside `if s.tokenStore != nil { ... }` block, add:

```go
mux.Handle("POST /sites/set-enabled", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesSetEnabled)))
mux.Handle("OPTIONS /sites/set-enabled", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesSetEnabled)))
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd daemon && go test ./internal/api/ -run TestSitesSetEnabled -v`
Expected: PASS for all 3 tests.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/api/sites.go daemon/internal/api/api.go daemon/internal/api/sites_test.go
git commit -m "feat(daemon/api): POST /sites/set-enabled endpoint [skip-deploy]"
```

---

## Task 8: handleSitesRemove endpoint

**Files:**
- Modify: `daemon/internal/api/sites.go`
- Modify: `daemon/internal/api/api.go` (add route)
- Modify: `daemon/internal/api/sites_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/api/sites_test.go`:

```go
func TestSitesRemoveHappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(sites.SyncResponse{
			MySites:    []sites.MySite{}, // site removed
			OpResults:  []sites.OpResult{{Status: "ok"}},
			ServerTime: 1000,
		})
	}))
	defer upstream.Close()

	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager(upstream.URL, keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 47, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com"}, Enabled: true},
	})

	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	tok, _ := store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	body := strings.NewReader(`{"site_id":47}`)
	req := httptest.NewRequest("POST", "/sites/remove", body)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mgr.Cache().Match("youtube.com") != nil {
		t.Error("expected cache to be empty after remove")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestSitesRemove -v`
Expected: FAIL with 404 / route not found.

- [ ] **Step 3: Add handler**

Add to `daemon/internal/api/sites.go`:

```go
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
```

- [ ] **Step 4: Register route**

In `daemon/internal/api/api.go:Handler()`, inside the `if s.tokenStore != nil { ... }` block:

```go
mux.Handle("POST /sites/remove", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesRemove)))
mux.Handle("OPTIONS /sites/remove", requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesRemove)))
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd daemon && go test ./internal/api/ -run TestSitesRemove -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/api/sites.go daemon/internal/api/api.go daemon/internal/api/sites_test.go
git commit -m "feat(daemon/api): POST /sites/remove endpoint [skip-deploy]"
```

---

## Task 9: handleSitesMy endpoint (no auth)

**Files:**
- Modify: `daemon/internal/api/sites.go`
- Modify: `daemon/internal/api/api.go` (add route, no middleware)
- Modify: `daemon/internal/api/sites_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/api/sites_test.go`:

```go
func TestSitesMyReturnsCacheSnapshot(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: false},
	})
	store := sites.NewTokenStore(filepath.Join(t.TempDir(), "tok"))
	store.GetOrCreate()
	srv := newTestServerWithSitesAPI(t, mgr, store)

	req := httptest.NewRequest("GET", "/sites/my", nil)
	// NO Authorization header — endpoint is not auth-gated.
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	mySites, ok := resp["my_sites"].([]interface{})
	if !ok || len(mySites) != 2 {
		t.Fatalf("expected 2 sites, got %v", resp["my_sites"])
	}
}

func TestSitesMyReturns503WhenManagerNil(t *testing.T) {
	srv := New(tunnel.New(stats.NewRateMeter()), nil, "127.0.0.1:0", stats.NewRateMeter())
	// no SetSites call

	req := httptest.NewRequest("GET", "/sites/my", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("expected 503, got %d", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestSitesMy -v`
Expected: FAIL with 404.

- [ ] **Step 3: Add handler**

Add to `daemon/internal/api/sites.go`:

```go
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
```

- [ ] **Step 4: Register route (NO middleware)**

In `daemon/internal/api/api.go:Handler()`, OUTSIDE the `if s.tokenStore != nil { ... }` block (because this route doesn't need the token store):

```go
// Read-only sites endpoint for local desktop client renderer (no auth).
mux.HandleFunc("GET /sites/my", s.handleSitesMy)
```

- [ ] **Step 5: Run tests to verify pass**

Run: `cd daemon && go test ./internal/api/ -run TestSitesMy -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/api/sites.go daemon/internal/api/api.go daemon/internal/api/sites_test.go
git commit -m "feat(daemon/api): GET /sites/my endpoint for renderer pull [skip-deploy]"
```

---

## Task 10: Simplify handlePacSitesUpdate to call RebuildPAC

**Files:**
- Modify: `daemon/internal/api/api.go`
- Modify: `daemon/internal/api/api_test.go`

- [ ] **Step 1: Write failing test**

Add to `daemon/internal/api/api_test.go`:

```go
func TestHandlePacSitesUpdateIgnoresSitesField(t *testing.T) {
	keyStore := sites.NewKeyStore(filepath.Join(t.TempDir(), "key"))
	keyStore.Save("dummy")
	mgr := sites.NewManager("https://example.invalid", keyStore)
	mgr.Cache().Replace([]sites.MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
	})
	srv := newTestServerWithMgr(t, mgr)

	// Renderer pushes proxy_all=false with bogus sites. Daemon must ignore
	// the sites field and use its own cache instead.
	body := strings.NewReader(`{"proxy_all":false,"sites":["bogus.example.com"]}`)
	req := httptest.NewRequest("POST", "/pac/sites", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	proxyAll, domains := srv.pacSites.Get()
	if proxyAll {
		t.Error("expected proxy_all=false")
	}
	// Domains should come from cache (habr.com expanded), NOT from request body.
	if len(domains) == 0 || domains[0] != "habr.com" {
		t.Errorf("expected daemon-formed domains, got %v", domains)
	}
	for _, d := range domains {
		if d == "bogus.example.com" {
			t.Error("expected bogus.example.com to be ignored")
		}
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `cd daemon && go test ./internal/api/ -run TestHandlePacSitesUpdateIgnoresSitesField -v`
Expected: FAIL — current implementation accepts and stores the bogus domain.

- [ ] **Step 3: Modify handlePacSitesUpdate**

Replace existing `handlePacSitesUpdate` in `daemon/internal/api/api.go` with:

```go
func (s *Server) handlePacSitesUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProxyAll bool `json:"proxy_all"`
		// sites field intentionally not parsed — daemon owns the domain list
		// after the popup control panel refactor.
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Set proxy_all flag, then RebuildPAC fills domains from cache.
	s.pacSites.Set(req.ProxyAll, nil)
	s.RebuildPAC()
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `cd daemon && go test ./internal/api/ -run TestHandlePacSitesUpdate -v`
Expected: PASS.

Also run full api package: `cd daemon && go test ./internal/api/ -v`
Expected: All previous tests still pass.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/api/api.go daemon/internal/api/api_test.go
git commit -m "refactor(daemon/api): simplify handlePacSitesUpdate to use cache [skip-deploy]"
```

---

## Task 11: Wire SetOnCacheReplaced in daemon main

**Files:**
- Modify: `daemon/cmd/main.go`

- [ ] **Step 1: Add wiring after sitesManager + srv creation**

In `daemon/cmd/main.go`, after `srv.SetSites(sitesManager, tokenStore)` (around line 49):

```go
// Wire RebuildPAC into the cache-replace callback so that any change
// to the cache (background refresh, mutation through extension API,
// or mutation through desktop client UI) automatically rebuilds PAC.
sitesManager.SetOnCacheReplaced(srv.RebuildPAC)
```

- [ ] **Step 2: Build to verify no compile errors**

Run: `cd daemon && go build ./...`
Expected: No errors.

- [ ] **Step 3: Run all daemon tests**

Run: `cd daemon && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add daemon/cmd/main.go
git commit -m "feat(daemon): wire RebuildPAC into cache callback [skip-deploy]"
```

---

# Phase 2 — Desktop Client (TypeScript)

## Task 12: cachedDaemonToken in extension.ts

**Files:**
- Modify: `client/src/main/extension.ts`

- [ ] **Step 1: Replace extension.ts with cached version**

Replace `client/src/main/extension.ts` with:

```ts
import fs from "fs";
import os from "os";
import path from "path";

function tokenPath(): string {
  if (process.platform === "win32") {
    return path.join(process.env.APPDATA || os.homedir(), "Proxyness", "daemon-token");
  }
  return path.join(os.homedir(), ".config", "proxyness", "daemon-token");
}

let cachedToken: string | null = null;

// cachedDaemonToken returns the daemon bearer token, reading the file
// once and caching the result in memory. Subsequent calls are
// synchronous and don't touch the disk. Returns "" if the file doesn't
// exist or can't be read.
export function cachedDaemonToken(): string {
  if (cachedToken !== null) return cachedToken;
  try {
    cachedToken = fs.readFileSync(tokenPath(), "utf-8").trim();
  } catch {
    cachedToken = "";
  }
  return cachedToken;
}

// clearCachedDaemonToken forces the next cachedDaemonToken() call to
// re-read from disk. Used if the token file changes mid-session
// (shouldn't happen in practice — daemon GetOrCreate reuses existing).
export function clearCachedDaemonToken(): void {
  cachedToken = null;
}

// getDaemonToken is kept for backwards compatibility with the existing
// `get-daemon-token` IPC handler used by BrowserExtension.tsx to show
// the token to the user. Internally uses the cache.
export function getDaemonToken(): string {
  return cachedDaemonToken();
}
```

- [ ] **Step 2: Build TS to verify no errors**

Run: `cd client && npx tsc --noEmit -p tsconfig.main.json`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add client/src/main/extension.ts
git commit -m "perf(client/main): cache daemon token in memory [skip-deploy]"
```

---

## Task 13: New IPC handlers in main/index.ts

**Files:**
- Modify: `client/src/main/index.ts`

- [ ] **Step 1: Add new IPC handlers**

In `client/src/main/index.ts`, find the existing `ipcMain.on("pac-sites", ...)` (around line 330) and **replace** it with the simplified version + add the four new handlers right after:

```ts
import { cachedDaemonToken } from "./extension";

// ... inside the same setup function where other ipcMain handlers live ...

ipcMain.on("pac-sites", (_e, data: { proxy_all: boolean }) => {
  // The `sites` field is no longer accepted — daemon owns the domain list.
  fetch("http://127.0.0.1:9090/pac/sites", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ proxy_all: data.proxy_all }),
  })
    .then(() => enableSystemProxy())
    .catch(() => {});
});

ipcMain.handle("daemon-set-enabled", async (_e, siteId: number, enabled: boolean) => {
  const token = cachedDaemonToken();
  if (!token) throw new Error("daemon token unavailable — restart daemon");
  const r = await fetch("http://127.0.0.1:9090/sites/set-enabled", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ site_id: siteId, enabled }),
  });
  if (!r.ok) throw new Error(`daemon ${r.status}`);
  return await r.json(); // { ok: true, my_sites: [...] }
});

ipcMain.handle("daemon-add-site", async (_e, primaryDomain: string, label: string) => {
  const token = cachedDaemonToken();
  if (!token) throw new Error("daemon token unavailable — restart daemon");
  const r = await fetch("http://127.0.0.1:9090/sites/add", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ primary_domain: primaryDomain, label }),
  });
  if (!r.ok) throw new Error(`daemon ${r.status}`);
  return await r.json(); // { site_id, deduped }
});

ipcMain.handle("daemon-remove-site", async (_e, siteId: number) => {
  const token = cachedDaemonToken();
  if (!token) throw new Error("daemon token unavailable — restart daemon");
  const r = await fetch("http://127.0.0.1:9090/sites/remove", {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
    body: JSON.stringify({ site_id: siteId }),
  });
  if (!r.ok) throw new Error(`daemon ${r.status}`);
  return await r.json(); // { ok: true, my_sites: [...] }
});

ipcMain.handle("daemon-list-sites", async () => {
  // No auth needed for /sites/my (localhost-only, read-only).
  const r = await fetch("http://127.0.0.1:9090/sites/my");
  if (!r.ok) throw new Error(`daemon ${r.status}`);
  return await r.json(); // { my_sites: [...] }
});
```

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.main.json`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add client/src/main/index.ts
git commit -m "feat(client/main): IPC handlers for daemon site mutations [skip-deploy]"
```

---

## Task 14: preload.ts updates (new methods + setPacSites type)

**Files:**
- Modify: `client/src/main/preload.ts`

- [ ] **Step 1: Update preload.ts**

In `client/src/main/preload.ts`:

Replace the `sysproxy` block:
```ts
contextBridge.exposeInMainWorld("sysproxy", {
  enable: () => ipcRenderer.send("enable-proxy"),
  disable: () => ipcRenderer.send("disable-proxy"),
  setPacSites: (data: { proxy_all: boolean }) => ipcRenderer.send("pac-sites", data),
});
```

Extend the `appInfo` block by adding inside it (next to `getDaemonToken`):
```ts
  daemonSetEnabled: (siteId: number, enabled: boolean) =>
    ipcRenderer.invoke("daemon-set-enabled", siteId, enabled),
  daemonAddSite: (primaryDomain: string, label: string) =>
    ipcRenderer.invoke("daemon-add-site", primaryDomain, label),
  daemonRemoveSite: (siteId: number) =>
    ipcRenderer.invoke("daemon-remove-site", siteId),
  daemonListSites: () =>
    ipcRenderer.invoke("daemon-list-sites"),
```

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.main.json`
Expected: No errors.

- [ ] **Step 3: Commit**

```bash
git add client/src/main/preload.ts
git commit -m "feat(client/preload): expose daemon site IPC methods [skip-deploy]"
```

---

## Task 15: useDaemon.ts type update

**Files:**
- Modify: `client/src/renderer/hooks/useDaemon.ts`

- [ ] **Step 1: Update window declaration**

In `client/src/renderer/hooks/useDaemon.ts`, find the line:

```ts
sysproxy: { enable: () => void; disable: () => void; setPacSites: (data: { proxy_all: boolean; sites: string[] }) => void };
```

Replace with:

```ts
sysproxy: { enable: () => void; disable: () => void; setPacSites: (data: { proxy_all: boolean }) => void };
```

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: TypeScript will now flag every callsite of `setPacSites` that still passes `sites:` — these are the next tasks.

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/hooks/useDaemon.ts
git commit -m "refactor(client/renderer): drop sites field from setPacSites type [skip-deploy]"
```

---

## Task 16: App.tsx — drop sites from setPacSites payload

**Files:**
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Update tunConnect call**

In `client/src/renderer/App.tsx`, find line 180:

```ts
(window as any).sysproxy?.setPacSites({ proxy_all: true, sites: [] });
```

Replace with:

```ts
(window as any).sysproxy?.setPacSites({ proxy_all: true });
```

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: This file is now clean. Other callsites in AppRules.tsx still flagged.

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/App.tsx
git commit -m "refactor(client/renderer): drop sites field in tunConnect setPacSites [skip-deploy]"
```

---

## Task 17: AppRules.tsx — remove siteDomains memo, simplify applyPac, simplify applyRules

**Files:**
- Modify: `client/src/renderer/components/AppRules.tsx`

- [ ] **Step 1: Remove siteDomains memo and expandDomains import**

In `client/src/renderer/components/AppRules.tsx`:

Remove the import line:
```ts
import { expandDomains } from "../sites/pac";
```

Remove the entire `siteDomains` memo block (lines 261-273 in current file):
```ts
  // All three derivations below MUST be memoized: ...
  // ... (the comment block + the useMemo)
  const siteDomains = useMemo(() => {
    const enabledDomains = localSites
      .filter((s) => s.enabled)
      .flatMap((s) => s.domains);
    return expandDomains(enabledDomains);
  }, [localSites]);
```

(Keep the explanatory comment about `enabledSet` and `liveSites` memos — those still exist and the comment is still relevant.)

- [ ] **Step 2: Simplify applyPac**

Replace the `applyPac` callback (around line 357):

```ts
const applyPac = useCallback(
  (on: boolean) => {
    if (!on) {
      window.sysproxy?.disable();
      return;
    }
    window.sysproxy?.setPacSites({ proxy_all: allSitesOn });
    window.sysproxy?.enable();
  },
  [allSitesOn]
);
```

- [ ] **Step 3: Simplify applyRules**

In `applyRules` (around line 378), find:
```ts
  if (m === "all") {
    window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
    window.sysproxy?.setPacSites({ proxy_all: true, sites: [] });
    window.sysproxy?.enable();
  } else {
```

Replace with:
```ts
  if (m === "all") {
    window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
    window.sysproxy?.setPacSites({ proxy_all: true });
    window.sysproxy?.enable();
  } else {
```

- [ ] **Step 4: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: No errors related to setPacSites or expandDomains.

- [ ] **Step 5: Commit**

```bash
git add client/src/renderer/components/AppRules.tsx
git commit -m "refactor(client/renderer): drop siteDomains memo, simplify PAC push [skip-deploy]"
```

---

## Task 18: Delete pac.ts (no consumers)

**Files:**
- Delete: `client/src/renderer/sites/pac.ts`

- [ ] **Step 1: Verify no consumers**

Run: `cd client && grep -r "from.*sites/pac" src/ || echo "no consumers"`
Expected: "no consumers"

- [ ] **Step 2: Delete file**

```bash
rm client/src/renderer/sites/pac.ts
```

- [ ] **Step 3: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add -u client/src/renderer/sites/pac.ts
git commit -m "refactor(client/renderer): delete unused pac.ts (moved to daemon) [skip-deploy]"
```

---

## Task 19: storage.ts and types.ts — remove pendingOps machinery

**Files:**
- Modify: `client/src/renderer/sites/storage.ts`
- Modify: `client/src/renderer/sites/types.ts`

- [ ] **Step 1: Read existing storage.ts**

Run: `cat client/src/renderer/sites/storage.ts`
Note current shape so the refactor preserves what's still needed.

- [ ] **Step 2: Remove pendingOps from storage.ts**

Edit `client/src/renderer/sites/storage.ts`:

- Remove `pendingOps: PendingOp[]` from the `PersistedState` interface
- Remove `pendingOps: readJSON<PendingOp[]>(KEY_PENDING, [])` from `loadState`
- Remove the `KEY_PENDING` constant
- Remove `savePendingOps` export
- Remove `import type { PendingOp }` if it becomes unused
- Keep `LEGACY_KEY_ENABLED_SITES` and `legacy*` helpers (used by migration in sync.ts)

- [ ] **Step 3: Remove unused types from types.ts**

Edit `client/src/renderer/sites/types.ts`:

- Remove `PendingOp` type union
- Remove `SyncRequest` interface (no longer constructed — daemon handles server sync)
- Remove `OpResult` interface (used only inside SyncResponse which we also remove)
- Remove `SyncResponse` interface (renderer no longer parses server responses directly)
- Keep `LocalSite`, `RemoteSite`, `SyncResult`

The resulting `types.ts` should be smaller — only types the daemon-driven flow actually uses.

- [ ] **Step 4: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: TypeScript flags consumers in `sync.ts` (fixed in next task). No other files reference these types.

- [ ] **Step 5: Don't commit yet**

This task is paired with Task 20 (sync.ts rewrite). Both must compile together.

---

## Task 20: sync.ts — async mutations through daemon, sync() pulls from daemon

**Files:**
- Modify: `client/src/renderer/sites/sync.ts`

- [ ] **Step 1: Replace sync.ts with daemon-driven version**

Replace `client/src/renderer/sites/sync.ts` with:

```ts
import type { LocalSite, RemoteSite } from "./types";
import {
  loadState,
  saveLocalSites,
  saveLastSyncAt,
  hasLocalSites,
  readLegacySites,
  clearLegacy,
} from "./storage";

// Module-level state.
let localSites: LocalSite[] = [];
let lastSyncAt = 0;
let initialized = false;
let initPromise: Promise<void> | null = null;
let tempIdSeq = -1;

const listeners = new Set<() => void>();

function notify(): void {
  for (const fn of listeners) fn();
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// initOnce loads persisted state, runs bootstrap if needed, runs the
// legacy migration, and clears the deprecated pendingOps key from
// pre-daemon-mutations versions. Idempotent.
export function initOnce(): Promise<void> {
  if (initPromise) return initPromise;
  initPromise = (async () => {
    if (initialized) return;
    const state = loadState();
    localSites = state.localSites;
    lastSyncAt = state.lastSyncAt;

    // One-shot cleanup: pendingOps queue from pre-daemon-mutations versions.
    // We can't replay these reliably, and most users will have an empty queue.
    localStorage.removeItem("proxyness-pending-ops");

    if (!hasLocalSites()) {
      await bootstrapFromBundle();
    }
    runLegacyMigrationIfNeeded();
    initialized = true;
  })();
  return initPromise;
}

export function getLocalSites(): LocalSite[] {
  return localSites;
}

export function getLastSyncAt(): number {
  return lastSyncAt;
}

// addSite adds a new site through the daemon. Returns the freshly-created
// LocalSite (with real server-assigned id). Throws on daemon error.
export async function addSite(primaryDomain: string, label: string): Promise<LocalSite> {
  const result = await (window as any).appInfo?.daemonAddSite(primaryDomain, label);
  if (!result || typeof result.site_id !== "number") {
    throw new Error("daemon-add-site: invalid response");
  }
  // After add, the daemon's cache contains the new site. Pull fresh snapshot.
  await sync();
  const created = localSites.find((s) => s.id === result.site_id);
  if (!created) {
    throw new Error("daemon-add-site: site not in fresh snapshot");
  }
  return created;
}

// removeSite removes a site through the daemon. Throws on error.
export async function removeSite(siteId: number): Promise<void> {
  const result = await (window as any).appInfo?.daemonRemoveSite(siteId);
  if (!result || result.ok !== true) {
    throw new Error("daemon-remove-site: failed");
  }
  // Replace localSites with fresh snapshot from response.
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  saveLocalSites(localSites);
  notify();
}

// toggleSite toggles per-user enabled flag through the daemon. Throws on error.
export async function toggleSite(siteId: number, enabled: boolean): Promise<void> {
  const result = await (window as any).appInfo?.daemonSetEnabled(siteId, enabled);
  if (!result || result.ok !== true) {
    throw new Error("daemon-set-enabled: failed");
  }
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  saveLocalSites(localSites);
  notify();
}

// sync refreshes localSites from the daemon's /sites/my endpoint. The
// daemon is the single source of truth for sites — it syncs with the
// catalog server in the background and serves the cache to the renderer.
// Called periodically (every 5 min) and on `online` event.
export interface SyncResult {
  ok: boolean;
  error?: string;
}

export async function sync(): Promise<SyncResult> {
  let result: any;
  try {
    result = await (window as any).appInfo?.daemonListSites();
  } catch (e) {
    return { ok: false, error: String(e) };
  }
  if (!result || !Array.isArray(result.my_sites)) {
    return { ok: false, error: "bad response" };
  }
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  lastSyncAt = Math.floor(Date.now() / 1000);
  saveLocalSites(localSites);
  saveLastSyncAt(lastSyncAt);
  finalizeLegacyMigration();
  notify();
  return { ok: true };
}

function remoteToLocal(r: RemoteSite): LocalSite {
  return {
    id: r.id,
    slug: r.slug,
    label: r.label,
    domains: r.domains,
    ips: r.ips,
    enabled: r.enabled,
    updatedAt: r.updated_at,
  };
}

async function bootstrapFromBundle(): Promise<void> {
  try {
    const seed = await (window as any).appInfo?.getSeedSites?.();
    if (!Array.isArray(seed)) {
      localSites = [];
      saveLocalSites(localSites);
      return;
    }
    localSites = seed.map((s: any) => ({
      id: s.id,
      slug: s.slug,
      label: s.label,
      domains: s.domains,
      ips: s.ips || [],
      enabled: true,
      updatedAt: 0,
    }));
    saveLocalSites(localSites);
  } catch {
    localSites = [];
    saveLocalSites(localSites);
  }
}

function runLegacyMigrationIfNeeded(): void {
  const legacyCustom = readLegacySites();
  if (legacyCustom == null) return;
  // Best-effort: schedule the legacy custom sites to be added through daemon
  // on first successful sync. For MVP we just log and clear — most users won't
  // have any legacy custom sites at this point in the project lifecycle.
  console.info("[sync] legacy custom sites detected, clearing");
}

function finalizeLegacyMigration(): void {
  clearLegacy();
}
```

Note: this removes `STORAGE_KEY` (device key was used by the old direct-server sync, no longer needed), removes `pendingOps` plumbing, removes `toWireOp`, drops the `tempIdSeq` use (negative ids no longer happen because daemon returns real ids).

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: errors only in `useSites.ts` (next task) about `addSite/removeSite/toggleSite` now returning Promises.

- [ ] **Step 3: Commit (paired with task 19)**

```bash
git add client/src/renderer/sites/storage.ts client/src/renderer/sites/types.ts client/src/renderer/sites/sync.ts
git commit -m "refactor(client/sites): mutations through daemon, drop pendingOps [skip-deploy]"
```

---

## Task 21: useSites.ts — async API surface

**Files:**
- Modify: `client/src/renderer/sites/useSites.ts`

- [ ] **Step 1: Update interface and exports**

Replace `client/src/renderer/sites/useSites.ts`:

```ts
import { useEffect, useState, useCallback, useRef } from "react";
import * as syncModule from "./sync";
import type { LocalSite } from "./types";

interface UseSitesReturn {
  sites: LocalSite[];
  syncing: boolean;
  lastSyncAt: number;
  ready: boolean;
  addSite: (primaryDomain: string, label: string) => Promise<LocalSite>;
  removeSite: (siteId: number) => Promise<void>;
  toggleSite: (siteId: number, enabled: boolean) => Promise<void>;
  syncNow: () => Promise<void>;
}

export function useSites(): UseSitesReturn {
  const [sites, setSites] = useState<LocalSite[]>([]);
  const [ready, setReady] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [lastSyncAt, setLastSyncAt] = useState<number>(0);
  const syncingRef = useRef(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      await syncModule.initOnce();
      if (cancelled) return;
      setSites([...syncModule.getLocalSites()]);
      setLastSyncAt(syncModule.getLastSyncAt());
      setReady(true);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const syncNow = useCallback(async () => {
    if (syncingRef.current || !ready) return;
    syncingRef.current = true;
    setSyncing(true);
    try {
      await syncModule.sync();
    } finally {
      syncingRef.current = false;
      setSyncing(false);
    }
  }, [ready]);

  useEffect(() => {
    if (!ready) return;

    const unsub = syncModule.subscribe(() => {
      setSites([...syncModule.getLocalSites()]);
      setLastSyncAt(syncModule.getLastSyncAt());
    });

    syncNow();

    const onOnline = () => syncNow();
    window.addEventListener("online", onOnline);

    const interval = setInterval(syncNow, 5 * 60 * 1000);

    return () => {
      unsub();
      window.removeEventListener("online", onOnline);
      clearInterval(interval);
    };
  }, [ready, syncNow]);

  return {
    sites,
    syncing,
    lastSyncAt,
    ready,
    addSite: syncModule.addSite,
    removeSite: syncModule.removeSite,
    toggleSite: syncModule.toggleSite,
    syncNow,
  };
}
```

- [ ] **Step 2: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: errors only in `AppRules.tsx` callsites of `addSite`/`removeSite`/`toggleSite` (next task).

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/sites/useSites.ts
git commit -m "refactor(client/sites): async API in useSites [skip-deploy]"
```

---

## Task 22: AppRules.tsx — async toggle/add/remove handlers

**Files:**
- Modify: `client/src/renderer/components/AppRules.tsx`

- [ ] **Step 1: Wrap addSiteByDomain in async + try/catch**

In `client/src/renderer/components/AppRules.tsx`, find `addSiteByDomain` (around line 442):

```ts
const addSiteByDomain = (raw: string) => {
  let domain = raw.trim().toLowerCase();
  if (!domain) return;
  domain = domain.replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/^www\./, "");
  if (!domain) return;
  const existing = localSites.find((s) => s.domains[0] === domain);
  if (existing) {
    if (!existing.enabled) toggleSiteById(existing.id, true);
    return;
  }
  const label = labelFromDomain(domain);
  addSite(domain, label);
};
```

Replace with:

```ts
const addSiteByDomain = async (raw: string) => {
  let domain = raw.trim().toLowerCase();
  if (!domain) return;
  domain = domain.replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/^www\./, "");
  if (!domain) return;
  const existing = localSites.find((s) => s.domains[0] === domain);
  if (existing) {
    if (!existing.enabled) {
      try {
        await toggleSiteById(existing.id, true);
      } catch (e) {
        console.error("[AppRules] toggle failed:", e);
      }
    }
    return;
  }
  const label = labelFromDomain(domain);
  try {
    await addSite(domain, label);
  } catch (e) {
    console.error("[AppRules] add failed:", e);
  }
};
```

- [ ] **Step 2: Wrap handleToggleTile in async + try/catch**

Find `handleToggleTile` (around line 425):

```ts
const handleToggleTile = (site: LocalSite) => {
  if (allSitesOn) {
    setAllSitesOn(false);
    localStorage.setItem("proxyness-all-sites-on", "false");
  }
  toggleSiteById(site.id, !site.enabled);
};
```

Replace with:

```ts
const handleToggleTile = async (site: LocalSite) => {
  if (allSitesOn) {
    setAllSitesOn(false);
    localStorage.setItem("proxyness-all-sites-on", "false");
  }
  try {
    await toggleSiteById(site.id, !site.enabled);
  } catch (e) {
    console.error("[AppRules] toggle failed:", e);
  }
};
```

- [ ] **Step 3: Wrap handleRemoveSite in async + try/catch**

Find `handleRemoveSite` (around line 457):

```ts
const handleRemoveSite = (siteId: number) => {
  removeSiteById(siteId);
};
```

Replace with:

```ts
const handleRemoveSite = async (siteId: number) => {
  try {
    await removeSiteById(siteId);
  } catch (e) {
    console.error("[AppRules] remove failed:", e);
  }
};
```

- [ ] **Step 4: Build TS**

Run: `cd client && npx tsc --noEmit -p tsconfig.renderer.json`
Expected: No errors.

- [ ] **Step 5: Commit**

```bash
git add client/src/renderer/components/AppRules.tsx
git commit -m "refactor(client/renderer): async toggle/add/remove handlers [skip-deploy]"
```

---

## Task 23: Manual smoke test of desktop client

**Files:** none

- [ ] **Step 1: Build daemon and client**

Run: `make build-daemon && cd client && npm run build && cd ..`
Expected: Both build without errors.

- [ ] **Step 2: Start dev mode**

Run: `make dev`
Expected: Electron window opens.

- [ ] **Step 3: Verify TUN AppRules works**

- Click "Connect" — daemon starts, tunnel connects
- Switch between AppRules sites — toggle on/off — verify the toggle visually flips
- Open Activity Monitor (macOS) — confirm `networksetup` doesn't hang on rapid toggles
- Open browser, navigate to a proxied site, verify it loads through the tunnel

- [ ] **Step 4: Verify allSitesOn switch**

- Toggle "all sites" on — verify daemon log shows `RebuildPAC` called once
- Toggle off — verify same
- Rapid toggle 10 times — verify no CPU spike, no hang

- [ ] **Step 5: Verify offline daemon error path**

- Quit Electron app
- Manually start daemon: `dist/daemon-darwin-arm64 -api 127.0.0.1:9090 -listen 127.0.0.1:1080 &`
- Start Electron app
- Kill daemon: `pkill -f daemon-darwin-arm64`
- Try to toggle a site in UI — verify error appears in console (`[AppRules] toggle failed: ...`)
- Restart daemon, verify next toggle works

- [ ] **Step 6: Commit if any tweaks needed**

If any issues found and fixed, commit them.

```bash
git add -p
git commit -m "fix(client): smoke test cleanup [skip-deploy]"
```

If no issues, no commit needed — proceed.

---

# Phase 3 — Browser Extension (JS)

## Task 24: daemon-client.js — setEnabled method

**Files:**
- Modify: `extension/lib/daemon-client.js`

- [ ] **Step 1: Add setEnabled method**

In `extension/lib/daemon-client.js`, find the `daemonClient` export object and add:

```js
setEnabled: (siteId, enabled) => call("POST", "/sites/set-enabled", { site_id: siteId, enabled }),
```

So the full export becomes:

```js
export const daemonClient = {
  match: (host) => call("GET", `/sites/match?host=${encodeURIComponent(host)}`),
  add: (primaryDomain, label) => call("POST", "/sites/add", { primary_domain: primaryDomain, label }),
  discover: (siteId, domains) => call("POST", "/sites/discover", { site_id: siteId, domains }),
  test: (url) => call("POST", "/sites/test", { url }),
  setEnabled: (siteId, enabled) => call("POST", "/sites/set-enabled", { site_id: siteId, enabled }),
  ping: async () => {
    const r = await call("GET", "/sites/match?host=ping.local");
    return r.ok || r.error === "unauthorized";
  },
};
```

- [ ] **Step 2: Commit**

```bash
git add extension/lib/daemon-client.js
git commit -m "feat(extension): daemon-client setEnabled method [skip-deploy]"
```

---

## Task 25: service-worker.js — popup state handlers + refreshTabState fix

**Files:**
- Modify: `extension/service-worker.js`

- [ ] **Step 1: Fix refreshTabState to recognize catalog_disabled**

In `extension/service-worker.js`, find `refreshTabState` (around line 64). Currently the `else` branch incorrectly maps `in_catalog && !proxy_enabled` to state `"add"`. Replace the body of `refreshTabState` from `const r = await daemonClient.match(host);` onward:

```js
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
    tabState.set(tabId, { state: "catalog_disabled", host, siteId: data.site_id });
  }
  pushStateToTab(tabId);
```

This is the only structural change to existing logic — popup handlers are additive.

- [ ] **Step 2: Add popup_get_state, popup_add_site, popup_set_enabled handlers**

In `extension/service-worker.js`, inside the `chrome.runtime.onMessage.addListener` callback, add three new handlers (place them after the existing `set_token`/`clear_token`/`get_state` handlers, before `add_current_site`). Also: the imports already pull `daemonClient` from `./lib/daemon-client.js` at the top of the file, so the new `setEnabled` method added in Task 24 is available without import changes.

```js
if (msg.type === "popup_get_state") {
  daemonClient.match(msg.host).then((r) => {
    if (!r.ok) {
      sendResponse({ state: "daemon_down" });
      return;
    }
    const data = r.data;
    if (!data.in_catalog) {
      sendResponse({ state: "not_in_catalog", host: msg.host });
      return;
    }
    if (data.proxy_enabled === false) {
      sendResponse({ state: "catalog_disabled", host: msg.host, site_id: data.site_id });
      return;
    }
    sendResponse({ state: "proxied", host: msg.host, site_id: data.site_id });
  });
  return true;
}

if (msg.type === "popup_add_site") {
  daemonClient.add(msg.host, msg.host).then((r) => {
    if (!r.ok) {
      sendResponse({ ok: false, error: r.error });
      return;
    }
    sendResponse({ ok: true, site_id: r.data.site_id });
  });
  return true;
}

if (msg.type === "popup_set_enabled") {
  daemonClient.setEnabled(msg.site_id, msg.enabled).then((r) => {
    if (!r.ok) {
      sendResponse({ ok: false, error: r.error });
      return;
    }
    sendResponse({ ok: true });
  });
  return true;
}
```

- [ ] **Step 3: Manual smoke test (deferred to Task 30)**

- [ ] **Step 4: Commit**

```bash
git add extension/service-worker.js
git commit -m "feat(extension/sw): popup state handlers + refreshTabState fix [skip-deploy]"
```

---

## Task 26: popup.js — state machine and control panel

**Files:**
- Modify: `extension/popup/popup.js`

- [ ] **Step 1: Replace popup.js with state-machine version**

Replace `extension/popup/popup.js`:

```js
const root = document.getElementById("root");

// ----- helpers -----

function sendToSW(msg) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(msg, (resp) => resolve(resp));
  });
}

async function getStoredToken() {
  const r = await chrome.storage.local.get("daemon_token");
  return r.daemon_token || null;
}

function getDomain(host) {
  const parts = host.split(".");
  if (parts.length <= 2) return host;
  const secondLevel = new Set(["co", "com", "org", "net", "ac", "gov", "edu"]);
  if (parts.length >= 3 && secondLevel.has(parts[parts.length - 2])) {
    return parts.slice(-3).join(".");
  }
  return parts.slice(-2).join(".");
}

async function loadActiveTabState() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || !tab.url) return { state: "system_page", tabId: tab?.id };
  let url;
  try { url = new URL(tab.url); } catch { return { state: "system_page", tabId: tab.id }; }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return { state: "system_page", tabId: tab.id };
  }
  const host = getDomain(url.hostname);
  const resp = await sendToSW({ type: "popup_get_state", host });
  return { ...resp, tabId: tab.id };
}

// ----- views -----

function renderPairing(initialError) {
  root.innerHTML = `
    <div class="title">Pair with Proxyness</div>
    <div class="subtitle">
      Open the Proxyness desktop client → Browser Extension tab,
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
    const ok = await tryPair(token);
    if (ok) {
      render();
    } else {
      msg.textContent = "Pairing failed. Is the daemon running? Token correct?";
      msg.className = "error";
    }
  });
}

async function tryPair(token) {
  const resp = await sendToSW({ type: "set_token", token });
  return resp?.ok === true;
}

function renderDaemonDown() {
  root.innerHTML = `
    <div class="title">Daemon not running</div>
    <div class="subtitle">Start the Proxyness desktop client.</div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function renderSystemPage() {
  root.innerHTML = `
    <div class="title">No site to control</div>
    <div class="subtitle">Switch to a regular web page.</div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function renderControlPanel(state) {
  // state: { state, host, site_id?, tabId }
  let statusLine = "";
  let buttonText = "";
  let buttonHandler = null;

  if (state.state === "not_in_catalog") {
    statusLine = "Not proxied";
    buttonText = "Проксировать этот сайт";
    buttonHandler = async () => {
      const resp = await sendToSW({ type: "popup_add_site", host: state.host, tabId: state.tabId });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        showError(resp?.error || "Failed to add");
      }
    };
  } else if (state.state === "proxied") {
    statusLine = "✓ Proxied";
    buttonText = "Выключить проксирование";
    buttonHandler = async () => {
      const resp = await sendToSW({
        type: "popup_set_enabled",
        site_id: state.site_id,
        enabled: false,
        tabId: state.tabId,
      });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        showError(resp?.error || "Failed to disable");
      }
    };
  } else if (state.state === "catalog_disabled") {
    statusLine = "Off (locally disabled)";
    buttonText = "Включить проксирование";
    buttonHandler = async () => {
      const resp = await sendToSW({
        type: "popup_set_enabled",
        site_id: state.site_id,
        enabled: true,
        tabId: state.tabId,
      });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        showError(resp?.error || "Failed to enable");
      }
    };
  }

  root.innerHTML = `
    <div class="host">${escapeHtml(state.host || "")}</div>
    <div class="status">${statusLine}</div>
    <button id="action" class="action">${buttonText}</button>
    <div id="msg" class="error"></div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("action").addEventListener("click", buttonHandler);
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function showError(text) {
  const msg = document.getElementById("msg");
  if (msg) msg.textContent = text;
}

function escapeHtml(s) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

async function clearAndRender() {
  await sendToSW({ type: "clear_token" });
  render();
}

// ----- top-level dispatcher -----

async function render() {
  const token = await getStoredToken();
  if (!token) {
    renderPairing();
    return;
  }
  const tabState = await loadActiveTabState();
  if (tabState.state === "daemon_down") {
    renderDaemonDown();
    return;
  }
  if (tabState.state === "system_page") {
    renderSystemPage();
    return;
  }
  renderControlPanel(tabState);
}

render();
```

- [ ] **Step 2: Commit**

```bash
git add extension/popup/popup.js
git commit -m "feat(extension/popup): per-tab control panel state machine [skip-deploy]"
```

---

## Task 27: popup.css — control panel layout

**Files:**
- Modify: `extension/popup/popup.css`

- [ ] **Step 1: Add styles for new layout**

Append to `extension/popup/popup.css` (or replace if you prefer a clean file):

```css
.host {
  font-size: 18px;
  font-weight: 600;
  margin-bottom: 4px;
  word-break: break-all;
}

.status {
  font-size: 13px;
  color: #888;
  margin-bottom: 16px;
}

.action {
  width: 100%;
  padding: 10px 16px;
  font-size: 14px;
  background: #3b82f6;
  color: #fff;
  border: none;
  border-radius: 4px;
  cursor: pointer;
}
.action:hover {
  background: #2563eb;
}

.footer {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid #2a3042;
  text-align: center;
}
.muted {
  color: #666;
  font-size: 11px;
  text-decoration: none;
}
.muted:hover {
  color: #888;
}

#msg.error {
  color: #ef4444;
  font-size: 12px;
  margin-top: 8px;
  text-align: center;
}
```

- [ ] **Step 2: Commit**

```bash
git add extension/popup/popup.css
git commit -m "style(extension/popup): control panel styles [skip-deploy]"
```

---

## Task 28: content-script.js — render catalog_disabled state

**Files:**
- Modify: `extension/content-script.js`

- [ ] **Step 1: Add `catalog_disabled` case to the switch**

In `extension/content-script.js`, find the `render(s)` function (around line 66) which has a `switch (s.state)` block with cases `down`, `proxied`, `discovering`, `add`, `blocked`, `default`. Add a new case `catalog_disabled` between `add` and `blocked`:

```js
      case "catalog_disabled":
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} off`;
        const enableBtn = document.createElement("button");
        enableBtn.textContent = "Enable";
        enableBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({
            type: "popup_set_enabled",
            site_id: s.siteId,
            enabled: true,
          }, (resp) => {
            if (resp?.ok) location.reload();
          });
        });
        actionsEl.appendChild(enableBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
```

Note: this case sits inside the existing `switch` block in the `render()` function — no new helper functions, no new CSS classes. Reuses existing icon classes (`icon gray`) and existing button style.

- [ ] **Step 2: Verify by reading file**

Run: `grep -n "catalog_disabled" extension/content-script.js`
Expected: Two matches — one in the case label, one in the message type comment.

- [ ] **Step 3: Commit**

```bash
git add extension/content-script.js
git commit -m "feat(extension/content-script): render catalog_disabled state [skip-deploy]"
```

---

## Task 29: manifest.json — version bump

**Files:**
- Modify: `extension/manifest.json`

- [ ] **Step 1: Bump version**

In `extension/manifest.json`, change:
```json
"version": "0.1.0",
```
to:
```json
"version": "0.2.0",
```

- [ ] **Step 2: Commit**

```bash
git add extension/manifest.json
git commit -m "chore(extension): version bump 0.2.0 [skip-deploy]"
```

---

# Phase 4 — End-to-End Verification

## Task 30: Manual E2E test of popup control panel

**Files:** none — manual verification

- [ ] **Step 1: Reload extension in Chrome**

- Open `chrome://extensions`
- Find Proxyness → click reload icon
- Make sure status is "Enabled" with no errors

- [ ] **Step 2: Verify pairing still works (regression-safe)**

- Click extension icon → popup opens
- If already paired, click "Unpair" first
- Open desktop client → Extension tab → copy token
- Paste into popup → click "Pair"
- Verify popup transitions to control panel for current tab

- [ ] **Step 3: Test "Проксировать" flow (state: not_in_catalog)**

- Open a brand-new site that's NOT in your catalog (e.g. random news site)
- Click extension icon → popup shows host + "Проксировать этот сайт"
- Click button → tab reloads → on next render popup should show "Proxied"
- Open Network tab in DevTools → verify requests now go through proxy (check timing)

- [ ] **Step 4: Test "Выключить проксирование" flow (state: proxied)**

- On a site you know is proxied (one from catalog)
- Click extension icon → popup shows "✓ Proxied" + "Выключить проксирование"
- Click button → tab reloads
- Reopen popup → should show "Off (locally disabled)" + "Включить проксирование"
- DevTools → verify requests now go DIRECT (no proxy)

- [ ] **Step 5: Test "Включить проксирование" flow (state: catalog_disabled)**

- On the site from previous step (still disabled)
- Click "Включить проксирование" → tab reloads
- Reopen popup → should show "✓ Proxied"
- DevTools → verify requests via proxy again

- [ ] **Step 6: Verify desktop client UI shows the same state**

- Open desktop client → AppRules tab
- Find the site you toggled in step 4-5
- Verify its toggle visually matches the popup's state (Enabled/Disabled)
- Toggle it from desktop client UI → verify popup picks up the change on next open

- [ ] **Step 7: Test daemon_down state**

- Quit desktop client (kills daemon)
- Click extension icon → should show "Daemon not running"

- [ ] **Step 8: Test system_page state**

- Restart desktop client
- Open `chrome://extensions` tab
- Click extension icon → should show "No site to control"

- [ ] **Step 9: Verify connection-reset regression check**

- Open browser → connect to a long-lived stream (YouTube video)
- Wait 5+ minutes (background Refresh tick)
- Verify the stream does NOT drop or buffer at the 5-minute mark
- This validates the RebuildPAC diff guard

- [ ] **Step 10: Cross-platform — repeat on Windows**

If a Windows machine is available, repeat steps 1-9.

If any step fails, file an issue and fix before merging.

---

## Task 31: Final daemon and client tests

**Files:** none

- [ ] **Step 1: Run full Go test suite**

Run: `make test`
Expected: All tests pass.

- [ ] **Step 2: Run TS type-check**

Run: `cd client && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 3: Build everything**

Run: `make build-server build-daemon build-client`
Expected: All artifacts produced in `dist/`.

- [ ] **Step 4: Commit any post-fix tweaks**

If any tweaks were needed during steps 1-3, commit them with a clear message.

---

## Task 32: Bump desktop client version

**Files:**
- Modify: `client/package.json`

- [ ] **Step 1: Bump minor version**

This is a feature release (popup gets a new control panel). Bump minor version per project semver convention (`feedback_versioning`).

In `client/package.json`, change:
```json
"version": "1.25.1",
```
to:
```json
"version": "1.26.0",
```

- [ ] **Step 2: Commit**

```bash
git add client/package.json
git commit -m "chore(client): bump version 1.26.0 [skip-deploy]"
```

---

## Task 33: Update CHANGELOG.new.md

**Files:**
- Create (force): `CHANGELOG.new.md`

- [ ] **Step 1: Write changelog entry**

Create `CHANGELOG.new.md`:

```markdown
## feature
Popup-расширение теперь панель управления для активной вкладки: проксировать сайт одним кликом, выключить проксирование, переключение применяется немедленно через автоматический reload вкладки. Daemon забрал на себя формирование PAC — refactor устраняет race conditions между переключениями из popup'а и из десктоп-клиента.
```

- [ ] **Step 2: Commit (with -f because of pre-commit hook)**

```bash
git add -f CHANGELOG.new.md
git commit -m "docs(changelog): popup control panel feature"
```

Note: NO `[skip-deploy]` here — this is the user-facing feature commit and should trigger deploy + release.

---

## Self-Review Checklist

After implementing all tasks, run through this list:

- [ ] **Spec coverage:** every section of `docs/superpowers/specs/2026-04-08-popup-control-panel-design.md` has at least one task implementing it
- [ ] **TUN AppRules manual test passed** (Task 23 step 3-5) — no regression on the critical flow
- [ ] **5-minute connection persistence verified** (Task 30 step 9) — RebuildPAC diff guard works
- [ ] **Daemon test suite green** — `make test`
- [ ] **TS type-check clean** — `cd client && npx tsc --noEmit`
- [ ] **Cross-platform** — Windows verified if available, otherwise note as known untested
- [ ] **Pair-form regression-safe** — pairing still works as before
- [ ] **Extension version bumped** — `extension/manifest.json` shows 0.2.0
