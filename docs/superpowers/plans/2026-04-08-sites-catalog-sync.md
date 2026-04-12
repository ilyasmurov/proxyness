# Sites Catalog + Per-User Sync (Phase A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the hardcoded browser sites list from the React client into a shared server SQLite catalog with per-user enabled state, accessed through a single offline-first batch-sync HTTP endpoint.

**Architecture:** Server gets four new tables (`sites`, `site_domains`, `site_ips`, `user_sites`) and one endpoint `POST /api/sync` that processes `add`/`remove`/`enable`/`disable` ops in a transaction and returns the user's full current snapshot. Client gets a new `client/src/renderer/sites/` module with a pending-ops queue in localStorage, a React `useSites()` hook, and a bundled seed JSON generated from the same Go source as the server seed. The existing `AppRules.tsx` component drops its `BROWSER_SITES` / `RELATED_DOMAINS` hardcodes and reads from the hook.

**Tech Stack:** Go 1.23 + modernc.org/sqlite on the server, Electron + React + TypeScript on the client. No new dependencies on either side.

**Spec:** `docs/superpowers/specs/2026-04-08-sites-catalog-sync.md`

---

## File Structure

**Server — create:**
- `server/internal/db/seed_sites.go` — `SeedSite` type, `seedSites` slice ported from current client hardcode, `SeedSitesIfEmpty(tx)`.
- `server/internal/db/sites.go` — all per-request CRUD used by the sync handler (`GetMySites`, `ApplyAddOp`, `ApplyRemoveOp`, `ApplyToggleOp`, helpers).
- `server/internal/db/sites_test.go` — unit tests for the DB layer, written TDD.
- `server/internal/admin/auth_device.go` — `AuthenticateDevice` middleware + `keyRateLimiter` (sliding-window, in-memory).
- `server/internal/admin/sync.go` — `handleSync` HTTP handler, request/response types, op dispatch.
- `server/internal/admin/sync_test.go` — handler-level round-trip tests using `httptest`.
- `server/cmd/export-seed/main.go` — CLI that prints `seedSites` as JSON.

**Server — modify:**
- `server/internal/db/db.go` — extend `schema` const with four new tables; call `SeedSitesIfEmpty` from `Open()` after `sqlDB.Exec(schema)`.
- `server/internal/admin/admin.go` — mount `POST /api/sync` with `AuthenticateDevice` middleware.

**Client — create:**
- `client/src/renderer/sites/types.ts` — `LocalSite`, `PendingOp`, `SyncRequest`, `SyncResponse`, `OpResult` types shared across the module.
- `client/src/renderer/sites/storage.ts` — localStorage read/write helpers (isolated so test/manual inspection is easy).
- `client/src/renderer/sites/pac.ts` — `expandDomains()` moved out of `AppRules.tsx` so PAC generation is independently testable.
- `client/src/renderer/sites/sync.ts` — the core module: mutators (`addSite`, `removeSite`, `toggleSite`), the `sync()` function, legacy-migration + first-run bootstrap.
- `client/src/renderer/sites/useSites.ts` — React hook that subscribes to the module and returns `{ sites, syncing, lastSyncAt, addSite, removeSite, toggleSite, syncNow }`.

**Client — modify:**
- `client/src/renderer/components/AppRules.tsx` — delete `DEFAULT_SITES`, `RELATED_DOMAINS`, `STORAGE_KEY_SITES`, `STORAGE_KEY_ENABLED_SITES`, `loadSites()`, `saveCustomSites()`, `loadEnabledSites()`, `saveEnabledSites()`, inline `expandDomains`. Use `useSites()` hook. Keep icons, letter-avatar, LIVE polling, all the JSX.
- `client/src/renderer/App.tsx` — after proxy connect, call `syncNow()` exposed via a small event.

**Build — modify:**
- `Makefile` — prepend `go run ./server/cmd/export-seed > client/resources/seed_sites.json` to `build-client`.
- `.gitignore` — add `client/resources/seed_sites.json`.

---

## Task 1: Add SQL schema for catalog tables

**Files:**
- Modify: `server/internal/db/db.go` (`schema` const, around line 60-97)

- [ ] **Step 1: Extend the schema const with four tables**

Append the following SQL to the existing `schema` constant in `server/internal/db/db.go`, keeping the trailing backtick where it is:

```sql
CREATE TABLE IF NOT EXISTS sites (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    slug                TEXT UNIQUE NOT NULL,
    label               TEXT NOT NULL,
    primary_domain      TEXT UNIQUE NOT NULL,
    approved            INTEGER NOT NULL DEFAULT 1,
    created_by_user_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS site_domains (
    site_id     INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    domain      TEXT NOT NULL,
    is_primary  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (site_id, domain)
);
CREATE INDEX IF NOT EXISTS idx_site_domains_domain ON site_domains(domain);
CREATE TABLE IF NOT EXISTS site_ips (
    site_id  INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    cidr     TEXT NOT NULL,
    PRIMARY KEY (site_id, cidr)
);
CREATE TABLE IF NOT EXISTS user_sites (
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    site_id     INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    enabled     INTEGER NOT NULL DEFAULT 1,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, site_id)
);
```

Note `user_sites.updated_at` is `INTEGER` (unix seconds), not TIMESTAMP — the spec uses epoch seconds for LWW comparisons and we want direct numeric compare in SQL.

- [ ] **Step 2: Build and run existing tests**

Run: `cd server && go build ./... && go test ./internal/db/ -v`
Expected: build succeeds, all existing tests pass. The new tables are created but unused.

- [ ] **Step 3: Commit**

```bash
# Create changelog entry first
cat > CHANGELOG.new.md <<'EOF'
## feature
Серверные таблицы для каталога сайтов и per-user sync
EOF
git add server/internal/db/db.go CHANGELOG.new.md
git commit -m "feat(db): add sites/site_domains/site_ips/user_sites tables [skip-deploy]"
```

---

## Task 2: Port built-in sites to Go seed file

**Files:**
- Create: `server/internal/db/seed_sites.go`

- [ ] **Step 1: Create the seed file with the ported list**

Translate the current hardcode from `client/src/renderer/components/AppRules.tsx` (`DEFAULT_SITES` array + `RELATED_DOMAINS` map) into a Go slice. Create `server/internal/db/seed_sites.go`:

```go
package db

// SeedSite describes a built-in catalog entry bundled with the server
// binary and the client release. The slice index (starting from 0)
// determines the site's id after SeedSitesIfEmpty runs: sites[i] → id=i+1.
// This guarantees the seed JSON shipped with the client matches the DB
// without a query-back step.
type SeedSite struct {
	Slug          string
	Label         string
	PrimaryDomain string   // dedup key, also inserted as an is_primary=1 row in site_domains
	Domains       []string // extra domains, NOT including PrimaryDomain
	IPs           []string // CIDRs (may be empty)
}

// seedSites is the authoritative source of built-in catalog entries.
// Order matters — the slice index becomes the site's DB id.
var seedSites = []SeedSite{
	{
		Slug:          "youtube",
		Label:         "YouTube",
		PrimaryDomain: "youtube.com",
		Domains: []string{
			"googlevideo.com", "ytimg.com", "ggpht.com",
			"youtube-nocookie.com", "youtu.be",
			"googleapis.com", "gstatic.com", "google.com",
		},
	},
	{
		Slug:          "instagram",
		Label:         "Instagram",
		PrimaryDomain: "instagram.com",
		Domains: []string{
			"cdninstagram.com", "fbcdn.net", "facebook.com",
			"fbsbx.com",
		},
	},
	{
		Slug:          "twitter",
		Label:         "Twitter / X",
		PrimaryDomain: "twitter.com",
		Domains: []string{
			"x.com", "twimg.com", "t.co", "abs.twimg.com",
		},
	},
	{
		Slug:          "facebook",
		Label:         "Facebook",
		PrimaryDomain: "facebook.com",
		Domains: []string{
			"fbcdn.net", "fbsbx.com", "facebook.net",
			"cdninstagram.com", "fb.com",
		},
	},
	{
		Slug:          "discord",
		Label:         "Discord (web)",
		PrimaryDomain: "discord.com",
		Domains: []string{
			"discordapp.com", "discordapp.net", "discord.gg",
			"discord.media",
		},
	},
	{
		Slug:          "linkedin",
		Label:         "LinkedIn",
		PrimaryDomain: "linkedin.com",
		Domains:       []string{"licdn.com", "linkedin.cn"},
	},
	{
		Slug:          "medium",
		Label:         "Medium",
		PrimaryDomain: "medium.com",
	},
	{
		Slug:          "claude",
		Label:         "Claude",
		PrimaryDomain: "claude.ai",
		Domains:       []string{"anthropic.com"},
	},
	{
		Slug:          "youtrack",
		Label:         "YouTrack",
		PrimaryDomain: "youtrack.cloud",
		Domains:       []string{"jetbrains.com"},
	},
	{
		Slug:          "telegram-web",
		Label:         "Telegram (web)",
		PrimaryDomain: "web.telegram.org",
		Domains:       []string{"telegram.org", "t.me", "telegram.me"},
	},
}
```

Note: the "All sites" entry with domain `"*"` in the current client code is a pseudo-entry for mode-switching, not a real catalog site. It's NOT ported — it stays as a separate local toggle in the client in Task 18.

- [ ] **Step 2: Build**

Run: `cd server && go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Сидовый список встроенных сайтов перенесён в Go-код сервера
EOF
git add server/internal/db/seed_sites.go CHANGELOG.new.md
git commit -m "feat(db): port built-in browser sites to Go seed slice [skip-deploy]"
```

---

## Task 3: Implement SeedSitesIfEmpty with explicit ids

**Files:**
- Modify: `server/internal/db/seed_sites.go`
- Create: `server/internal/db/sites_test.go`
- Modify: `server/internal/db/db.go` — call from `Open()`

- [ ] **Step 1: Write the failing test**

Create `server/internal/db/sites_test.go`:

```go
package db

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})
	return d
}

func TestSeedSitesInsertsOnEmpty(t *testing.T) {
	d := tempDB(t)

	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		t.Fatalf("count sites: %v", err)
	}
	if count != len(seedSites) {
		t.Fatalf("expected %d seed sites, got %d", len(seedSites), count)
	}

	// Spot-check first seed row has id=1
	var id int
	var slug string
	if err := d.sql.QueryRow(`SELECT id, slug FROM sites ORDER BY id LIMIT 1`).Scan(&id, &slug); err != nil {
		t.Fatalf("first row: %v", err)
	}
	if id != 1 {
		t.Fatalf("first seed id = %d, want 1", id)
	}
	if slug != seedSites[0].Slug {
		t.Fatalf("first seed slug = %q, want %q", slug, seedSites[0].Slug)
	}

	// Spot-check primary domain has is_primary=1
	var isPrimary int
	if err := d.sql.QueryRow(
		`SELECT is_primary FROM site_domains WHERE site_id=1 AND domain=?`,
		seedSites[0].PrimaryDomain,
	).Scan(&isPrimary); err != nil {
		t.Fatalf("primary domain row: %v", err)
	}
	if isPrimary != 1 {
		t.Fatalf("primary domain is_primary = %d, want 1", isPrimary)
	}
}

func TestSeedSitesIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	d.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer d2.Close()

	var count int
	if err := d2.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != len(seedSites) {
		t.Fatalf("after reopen: expected %d sites, got %d (seed ran twice?)", len(seedSites), count)
	}
}

func TestSeedSitesPreservesIPs(t *testing.T) {
	d := tempDB(t)

	// Find a seed entry with IPs (none currently, so we add a sentinel to the
	// test DB directly via an independent insert to confirm the schema/CRUD
	// path works — this guards against Task 2 forgetting IPs later.)
	_, _ = d.sql.Exec(`INSERT INTO site_ips (site_id, cidr) VALUES (1, '10.0.0.0/24')`)

	var cidr string
	if err := d.sql.QueryRow(
		`SELECT cidr FROM site_ips WHERE site_id=1`,
	).Scan(&cidr); err != nil {
		t.Fatalf("ip row: %v", err)
	}
	if cidr != "10.0.0.0/24" {
		t.Fatalf("cidr = %q", cidr)
	}
}

// Remove the file suffix `_ = os.Stderr` hack if the imports need pruning.
var _ = os.Stderr
```

(The `os` import is used only by the unused sentinel; it's fine, the compiler accepts it.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd server && go test ./internal/db/ -run TestSeedSites -v`
Expected: FAIL — either compile errors (`SeedSitesIfEmpty` doesn't exist) or the queries return 0 rows because nothing is seeded.

- [ ] **Step 3: Implement `SeedSitesIfEmpty` in seed_sites.go**

Append to `server/internal/db/seed_sites.go`:

```go
// SeedSitesIfEmpty populates the sites/site_domains/site_ips tables
// with the built-in catalog. It's a no-op if sites already has any row,
// so it runs once per fresh database and is safe to call on every start.
// Uses explicit ids (i+1) so the bundled seed JSON can predict them.
func (d *DB) SeedSitesIfEmpty() error {
	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, s := range seedSites {
		id := int64(i + 1)
		if _, err := tx.Exec(
			`INSERT INTO sites (id, slug, label, primary_domain, approved, created_by_user_id)
			 VALUES (?, ?, ?, ?, 1, NULL)`,
			id, s.Slug, s.Label, s.PrimaryDomain,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 1)`,
			id, s.PrimaryDomain,
		); err != nil {
			return err
		}
		for _, dom := range s.Domains {
			if _, err := tx.Exec(
				`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`,
				id, dom,
			); err != nil {
				return err
			}
		}
		for _, cidr := range s.IPs {
			if _, err := tx.Exec(
				`INSERT INTO site_ips (site_id, cidr) VALUES (?, ?)`,
				id, cidr,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Wire it into `Open()` in `db.go`**

Edit `server/internal/db/db.go`. Find the `Open` function, specifically after the `ALTER TABLE` migration calls (~line 120) and before `d := &DB{sql: sqlDB}`. Insert:

```go
	// Seed built-in browser sites catalog (no-op if already populated)
	dForSeed := &DB{sql: sqlDB}
	if err := dForSeed.SeedSitesIfEmpty(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("seed sites: %w", err)
	}
```

(We wrap in a local `dForSeed` because `d` isn't declared yet at that point in the function. Alternatively move the `d := &DB{sql: sqlDB}` assignment up and use it directly — cleaner, do that instead.)

Reorder so the final shape of `Open`'s tail is:

```go
	// Migrate old changelog table ...
	var hasOldSchema bool
	sqlDB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('changelog') WHERE name='version'`).Scan(&hasOldSchema)
	if hasOldSchema {
		sqlDB.Exec(`DROP TABLE changelog`)
		sqlDB.Exec(schema)
	}

	d := &DB{sql: sqlDB}

	if err := d.SeedSitesIfEmpty(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("seed sites: %w", err)
	}

	d.syncChangelog()
	d.cleanOldLogs()
	return d, nil
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd server && go test ./internal/db/ -v`
Expected: all tests PASS (existing + new `TestSeedSites*`).

- [ ] **Step 6: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Сид каталога сайтов на старте сервера
EOF
git add server/internal/db/seed_sites.go server/internal/db/sites_test.go server/internal/db/db.go CHANGELOG.new.md
git commit -m "feat(db): seed built-in sites catalog on DB open [skip-deploy]"
```

---

## Task 4: DB helper — GetMySites snapshot

**Files:**
- Create: `server/internal/db/sites.go`
- Modify: `server/internal/db/sites_test.go` (append tests)

- [ ] **Step 1: Write the failing test**

Append to `server/internal/db/sites_test.go`:

```go
func seedUser(t *testing.T, d *DB) int {
	t.Helper()
	u, err := d.CreateUser("test")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u.ID
}

func TestGetMySitesEmpty(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	sites, err := d.GetMySites(userID)
	if err != nil {
		t.Fatalf("GetMySites: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("expected empty, got %d sites", len(sites))
	}
}

func TestGetMySitesJoinsDomainsAndIPs(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	// Attach seed site id=1 (youtube) to this user as enabled
	if _, err := d.sql.Exec(
		`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`,
		userID,
	); err != nil {
		t.Fatalf("insert user_sites: %v", err)
	}

	sites, err := d.GetMySites(userID)
	if err != nil {
		t.Fatalf("GetMySites: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}

	s := sites[0]
	if s.ID != 1 || s.Slug != "youtube" || !s.Enabled {
		t.Fatalf("unexpected site: %+v", s)
	}
	// The primary domain plus all extras should be present
	wantDomains := append([]string{"youtube.com"}, seedSites[0].Domains...)
	if len(s.Domains) != len(wantDomains) {
		t.Fatalf("domains len = %d, want %d", len(s.Domains), len(wantDomains))
	}
	for _, want := range wantDomains {
		found := false
		for _, got := range s.Domains {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing domain %q in %v", want, s.Domains)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/db/ -run TestGetMySites -v`
Expected: FAIL — `d.GetMySites undefined` and `UserSite` type undefined.

- [ ] **Step 3: Create sites.go with the type and method**

Create `server/internal/db/sites.go`:

```go
package db

import (
	"fmt"
)

// UserSite is a single row of the user's current catalog snapshot as
// returned by GetMySites. It joins sites + user_sites + site_domains + site_ips.
type UserSite struct {
	ID        int      `json:"id"`
	Slug      string   `json:"slug"`
	Label     string   `json:"label"`
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Enabled   bool     `json:"enabled"`
	UpdatedAt int64    `json:"updated_at"`
}

// GetMySites returns all catalog rows attached to the given user via
// user_sites, joined with their domains and ips. Result is ordered by label.
func (d *DB) GetMySites(userID int) ([]UserSite, error) {
	rows, err := d.sql.Query(`
		SELECT s.id, s.slug, s.label, us.enabled, us.updated_at
		FROM user_sites us
		JOIN sites s ON us.site_id = s.id
		WHERE us.user_id = ?
		ORDER BY s.label
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user_sites: %w", err)
	}
	defer rows.Close()

	var out []UserSite
	for rows.Next() {
		var s UserSite
		var enabledInt int
		if err := rows.Scan(&s.ID, &s.Slug, &s.Label, &enabledInt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_sites: %w", err)
		}
		s.Enabled = enabledInt != 0
		s.Domains = []string{}
		s.IPs = []string{}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach domains and ips with two follow-up queries (one per site).
	// Phase A has ≤100 rows per user, so N+1 is fine; optimize later if needed.
	for i := range out {
		domRows, err := d.sql.Query(
			`SELECT domain FROM site_domains WHERE site_id = ? ORDER BY is_primary DESC, domain`,
			out[i].ID,
		)
		if err != nil {
			return nil, fmt.Errorf("query domains: %w", err)
		}
		for domRows.Next() {
			var d string
			if err := domRows.Scan(&d); err != nil {
				domRows.Close()
				return nil, err
			}
			out[i].Domains = append(out[i].Domains, d)
		}
		domRows.Close()

		ipRows, err := d.sql.Query(
			`SELECT cidr FROM site_ips WHERE site_id = ? ORDER BY cidr`,
			out[i].ID,
		)
		if err != nil {
			return nil, fmt.Errorf("query ips: %w", err)
		}
		for ipRows.Next() {
			var c string
			if err := ipRows.Scan(&c); err != nil {
				ipRows.Close()
				return nil, err
			}
			out[i].IPs = append(out[i].IPs, c)
		}
		ipRows.Close()
	}

	if out == nil {
		out = []UserSite{}
	}
	return out, nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/db/ -run TestGetMySites -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
GetMySites возвращает снапшот каталога юзера с доменами и IP
EOF
git add server/internal/db/sites.go server/internal/db/sites_test.go CHANGELOG.new.md
git commit -m "feat(db): add GetMySites snapshot query [skip-deploy]"
```

---

## Task 5: DB helper — ApplyAddOp with dedup + slug generation

**Files:**
- Modify: `server/internal/db/sites.go`
- Modify: `server/internal/db/sites_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/db/sites_test.go`:

```go
func TestApplyAddOpNewSite(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := d.ApplyAddOp(tx, userID, "example.com", "Example", 1000)
	if err != nil {
		t.Fatalf("ApplyAddOp: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if res.Deduped {
		t.Fatalf("new site should not be deduped")
	}
	if res.SiteID == 0 {
		t.Fatalf("SiteID should be set")
	}

	var slug, primary string
	if err := d.sql.QueryRow(`SELECT slug, primary_domain FROM sites WHERE id=?`, res.SiteID).Scan(&slug, &primary); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if slug != "example" || primary != "example.com" {
		t.Fatalf("row = %q/%q, want example/example.com", slug, primary)
	}

	// user_sites row created with enabled=true
	var enabled int
	if err := d.sql.QueryRow(
		`SELECT enabled FROM user_sites WHERE user_id=? AND site_id=?`, userID, res.SiteID,
	).Scan(&enabled); err != nil {
		t.Fatalf("user_sites lookup: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d, want 1", enabled)
	}

	// site_domains has primary row
	var isPrimary int
	if err := d.sql.QueryRow(
		`SELECT is_primary FROM site_domains WHERE site_id=? AND domain=?`, res.SiteID, "example.com",
	).Scan(&isPrimary); err != nil {
		t.Fatalf("site_domains: %v", err)
	}
	if isPrimary != 1 {
		t.Fatalf("primary flag wrong")
	}

	// sites.created_by_user_id is set
	var createdBy int
	if err := d.sql.QueryRow(
		`SELECT created_by_user_id FROM sites WHERE id=?`, res.SiteID,
	).Scan(&createdBy); err != nil {
		t.Fatalf("created_by: %v", err)
	}
	if createdBy != userID {
		t.Fatalf("created_by = %d, want %d", createdBy, userID)
	}
}

func TestApplyAddOpDedupExisting(t *testing.T) {
	d := tempDB(t)
	userA := seedUser(t, d)
	userB, _ := d.CreateUser("b")

	tx, _ := d.sql.Begin()
	resA, _ := d.ApplyAddOp(tx, userA, "example.com", "Example", 1000)
	tx.Commit()

	tx2, _ := d.sql.Begin()
	resB, err := d.ApplyAddOp(tx2, userB.ID, "example.com", "ExampleUnused", 2000)
	if err != nil {
		t.Fatalf("ApplyAddOp B: %v", err)
	}
	tx2.Commit()

	if resB.SiteID != resA.SiteID {
		t.Fatalf("dedup failed: A=%d B=%d", resA.SiteID, resB.SiteID)
	}
	if !resB.Deduped {
		t.Fatalf("should be marked deduped")
	}

	// Label is not overwritten
	var label string
	d.sql.QueryRow(`SELECT label FROM sites WHERE id=?`, resA.SiteID).Scan(&label)
	if label != "Example" {
		t.Fatalf("label = %q, want Example", label)
	}

	// Both users have user_sites rows
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM user_sites WHERE site_id=?`, resA.SiteID).Scan(&count)
	if count != 2 {
		t.Fatalf("user_sites count = %d, want 2", count)
	}
}

func TestApplyAddOpExistingUserSiteIsNoop(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	res1, _ := d.ApplyAddOp(tx, userID, "example.com", "Example", 1000)
	tx.Commit()

	// Disable it manually
	d.sql.Exec(`UPDATE user_sites SET enabled=0, updated_at=1500 WHERE user_id=? AND site_id=?`, userID, res1.SiteID)

	// Re-add should be a no-op: enabled stays 0, updated_at stays 1500
	tx2, _ := d.sql.Begin()
	res2, _ := d.ApplyAddOp(tx2, userID, "example.com", "Example", 3000)
	tx2.Commit()

	if res2.SiteID != res1.SiteID {
		t.Fatalf("site id mismatch")
	}

	var enabled int
	var updatedAt int64
	d.sql.QueryRow(
		`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, res2.SiteID,
	).Scan(&enabled, &updatedAt)
	if enabled != 0 || updatedAt != 1500 {
		t.Fatalf("re-add mutated state: enabled=%d updated_at=%d", enabled, updatedAt)
	}
}

func TestApplyAddOpSlugCollision(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	// Add two different primary_domains that produce the same base slug
	res1, _ := d.ApplyAddOp(tx, userID, "foo.com", "Foo", 1000)
	res2, err := d.ApplyAddOp(tx, userID, "foo.net", "Foo Net", 1001)
	if err != nil {
		t.Fatalf("ApplyAddOp 2: %v", err)
	}
	tx.Commit()

	var slug1, slug2 string
	d.sql.QueryRow(`SELECT slug FROM sites WHERE id=?`, res1.SiteID).Scan(&slug1)
	d.sql.QueryRow(`SELECT slug FROM sites WHERE id=?`, res2.SiteID).Scan(&slug2)
	if slug1 != "foo" {
		t.Fatalf("slug1 = %q, want foo", slug1)
	}
	if slug2 != "foo2" {
		t.Fatalf("slug2 = %q, want foo2", slug2)
	}
}

func TestApplyAddOpInvalidDomain(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	defer tx.Rollback()
	_, err := d.ApplyAddOp(tx, userID, "NOT A DOMAIN", "Bad", 1000)
	if err == nil {
		t.Fatalf("expected error for invalid domain")
	}
}
```

- [ ] **Step 2: Run, verify failure**

Run: `cd server && go test ./internal/db/ -run TestApplyAddOp -v`
Expected: FAIL — `ApplyAddOp` undefined, `AddOpResult` undefined.

- [ ] **Step 3: Implement ApplyAddOp**

Append to `server/internal/db/sites.go`:

```go
import ( // extend the existing import block
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// AddOpResult captures what happened for a single "add" sync op.
type AddOpResult struct {
	SiteID  int
	Deduped bool // true if the primary_domain was already in the catalog
}

// domainRE is a permissive lowercase domain matcher used to reject
// obviously bogus user input like "NOT A DOMAIN" or "foo". Requires at
// least one dot and standard DNS label characters.
var domainRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// ApplyAddOp handles an "add" sync operation inside the given transaction.
// Semantics:
//   - Normalize primary_domain to lowercase.
//   - Reject if it doesn't match domainRE.
//   - If a sites row with that primary_domain already exists → reuse its id,
//     mark the result as Deduped, insert a user_sites row if missing.
//   - Otherwise: generate a slug from the domain (with collision suffixing),
//     INSERT sites + site_domains (primary row only), INSERT user_sites.
//   - If the user already has a user_sites row for this site, do NOT touch
//     enabled or updated_at — add is idempotent.
func (d *DB) ApplyAddOp(tx *sql.Tx, userID int, primaryDomain, label string, at int64) (AddOpResult, error) {
	primaryDomain = strings.ToLower(strings.TrimSpace(primaryDomain))
	if !domainRE.MatchString(primaryDomain) {
		return AddOpResult{}, fmt.Errorf("invalid domain: %q", primaryDomain)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = labelFromDomain(primaryDomain)
	}

	// Lookup existing site
	var existingID int
	err := tx.QueryRow(`SELECT id FROM sites WHERE primary_domain = ?`, primaryDomain).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return AddOpResult{}, err
	}

	if existingID != 0 {
		// Dedup path: just ensure user_sites row exists
		if err := ensureUserSite(tx, userID, existingID, at); err != nil {
			return AddOpResult{}, err
		}
		return AddOpResult{SiteID: existingID, Deduped: true}, nil
	}

	// New site path
	slug, err := pickSlug(tx, primaryDomain)
	if err != nil {
		return AddOpResult{}, err
	}

	res, err := tx.Exec(
		`INSERT INTO sites (slug, label, primary_domain, approved, created_by_user_id)
		 VALUES (?, ?, ?, 1, ?)`,
		slug, label, primaryDomain, userID,
	)
	if err != nil {
		return AddOpResult{}, err
	}
	siteID64, err := res.LastInsertId()
	if err != nil {
		return AddOpResult{}, err
	}
	siteID := int(siteID64)

	if _, err := tx.Exec(
		`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 1)`,
		siteID, primaryDomain,
	); err != nil {
		return AddOpResult{}, err
	}

	if err := ensureUserSite(tx, userID, siteID, at); err != nil {
		return AddOpResult{}, err
	}
	return AddOpResult{SiteID: siteID, Deduped: false}, nil
}

// ensureUserSite inserts a user_sites row if missing; otherwise it's a no-op.
// The enabled/updated_at fields of an existing row are NOT modified by add.
func ensureUserSite(tx *sql.Tx, userID, siteID int, at int64) error {
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO user_sites (user_id, site_id, enabled, updated_at)
		 VALUES (?, ?, 1, ?)`,
		userID, siteID, at,
	)
	return err
}

// pickSlug derives a slug from a primary_domain, appending a numeric suffix
// if the base slug is already taken by a different site.
func pickSlug(tx *sql.Tx, primaryDomain string) (string, error) {
	// Base: everything before the first dot, stripped of non-alphanumerics,
	// lowercased. "news.ycombinator.com" → "news". For multi-word brand
	// slugs the operator can manually fix later in the DB.
	base := primaryDomain
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	var cleaned strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cleaned.WriteRune(r)
		}
	}
	slug := cleaned.String()
	if slug == "" {
		slug = "site"
	}

	// Try base, base2, base3, ... until free
	attempt := slug
	suffix := 2
	for {
		var existing int
		err := tx.QueryRow(`SELECT id FROM sites WHERE slug = ?`, attempt).Scan(&existing)
		if err == sql.ErrNoRows {
			return attempt, nil
		}
		if err != nil {
			return "", err
		}
		attempt = fmt.Sprintf("%s%d", slug, suffix)
		suffix++
		if suffix > 1000 {
			return "", fmt.Errorf("too many slug collisions for %q", slug)
		}
	}
}

// labelFromDomain is a fallback display label: "example.com" → "Example".
func labelFromDomain(domain string) string {
	base := domain
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return domain
	}
	return strings.ToUpper(base[:1]) + base[1:]
}
```

Note: remove the sentinel `var _ = os.Stderr` line from sites_test.go if it's still there, and drop the `os` import — no longer needed.

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/db/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
ApplyAddOp с дедупликацией, slug collision handling и валидацией домена
EOF
git add server/internal/db/sites.go server/internal/db/sites_test.go CHANGELOG.new.md
git commit -m "feat(db): ApplyAddOp with dedup and slug generation [skip-deploy]"
```

---

## Task 6: DB helpers — ApplyRemoveOp / ApplyToggleOp (LWW)

**Files:**
- Modify: `server/internal/db/sites.go`
- Modify: `server/internal/db/sites_test.go`

- [ ] **Step 1: Write failing tests**

Append to `sites_test.go`:

```go
func TestApplyToggleOpLWW(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	// Attach site id=1 at t=1000 enabled
	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`, userID)

	// Older disable op should be stale
	tx, _ := d.sql.Begin()
	status := d.ApplyToggleOp(tx, userID, 1, false, 500)
	tx.Commit()
	if status != ToggleStale {
		t.Fatalf("old op status = %v, want stale", status)
	}
	var enabled int
	var updatedAt int64
	d.sql.QueryRow(`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&enabled, &updatedAt)
	if enabled != 1 || updatedAt != 1000 {
		t.Fatalf("row mutated: enabled=%d updated_at=%d", enabled, updatedAt)
	}

	// Newer disable op wins
	tx2, _ := d.sql.Begin()
	status = d.ApplyToggleOp(tx2, userID, 1, false, 2000)
	tx2.Commit()
	if status != ToggleOK {
		t.Fatalf("new op status = %v, want ok", status)
	}
	d.sql.QueryRow(`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&enabled, &updatedAt)
	if enabled != 0 || updatedAt != 2000 {
		t.Fatalf("updated row wrong: enabled=%d updated_at=%d", enabled, updatedAt)
	}
}

func TestApplyToggleOpMissingRow(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	status := d.ApplyToggleOp(tx, userID, 999, true, 1000)
	tx.Rollback()
	if status != ToggleNotFound {
		t.Fatalf("missing row status = %v, want not_found", status)
	}
}

func TestApplyRemoveOp(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`, userID)

	tx, _ := d.sql.Begin()
	if err := d.ApplyRemoveOp(tx, userID, 1); err != nil {
		t.Fatalf("ApplyRemoveOp: %v", err)
	}
	tx.Commit()

	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&count)
	if count != 0 {
		t.Fatalf("row not removed: %d", count)
	}

	// sites row untouched
	d.sql.QueryRow(`SELECT COUNT(*) FROM sites WHERE id=1`).Scan(&count)
	if count != 1 {
		t.Fatalf("catalog row was touched: %d", count)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd server && go test ./internal/db/ -run 'TestApplyToggle|TestApplyRemove' -v`
Expected: FAIL.

- [ ] **Step 3: Implement the helpers**

Append to `server/internal/db/sites.go`:

```go
// ToggleStatus enumerates the outcomes of an ApplyToggleOp call.
type ToggleStatus int

const (
	ToggleOK       ToggleStatus = iota // the row was updated
	ToggleStale                        // op's at was older than updated_at, no change
	ToggleNotFound                     // user_sites row didn't exist
)

// ApplyToggleOp updates enabled/updated_at on an existing user_sites row
// only if the incoming op's at is >= current updated_at (last-write-wins).
// Returns a status explaining what happened.
func (d *DB) ApplyToggleOp(tx *sql.Tx, userID, siteID int, enabled bool, at int64) ToggleStatus {
	var currentAt int64
	err := tx.QueryRow(
		`SELECT updated_at FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, siteID,
	).Scan(&currentAt)
	if err == sql.ErrNoRows {
		return ToggleNotFound
	}
	if err != nil {
		return ToggleNotFound
	}
	if at < currentAt {
		return ToggleStale
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	if _, err := tx.Exec(
		`UPDATE user_sites SET enabled=?, updated_at=? WHERE user_id=? AND site_id=?`,
		enabledInt, at, userID, siteID,
	); err != nil {
		return ToggleNotFound
	}
	return ToggleOK
}

// ApplyRemoveOp deletes the user_sites row for (user, site). The catalog
// row is not touched. Returns an error only for unexpected DB errors;
// deleting a missing row is a silent no-op.
func (d *DB) ApplyRemoveOp(tx *sql.Tx, userID, siteID int) error {
	_, err := tx.Exec(
		`DELETE FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, siteID,
	)
	return err
}
```

- [ ] **Step 4: Run tests**

Run: `cd server && go test ./internal/db/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
ApplyToggleOp (last-write-wins) и ApplyRemoveOp для sync-операций
EOF
git add server/internal/db/sites.go server/internal/db/sites_test.go CHANGELOG.new.md
git commit -m "feat(db): ApplyToggleOp (LWW) and ApplyRemoveOp [skip-deploy]"
```

---

## Task 7: Auth middleware + rate limiter

**Files:**
- Create: `server/internal/admin/auth_device.go`

- [ ] **Step 1: Create the auth middleware**

Create `server/internal/admin/auth_device.go`:

```go
package admin

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"proxyness/server/internal/db"
)

type ctxKey string

const ctxKeyUserID ctxKey = "user_id"

// DeviceAuth wraps handlers that should be authenticated via a device key
// in Authorization: Bearer <key>. The user_id from the matching device is
// stashed in the request context under ctxKeyUserID.
type DeviceAuth struct {
	db      *db.DB
	limiter *keyRateLimiter
}

func NewDeviceAuth(d *db.DB) *DeviceAuth {
	return &DeviceAuth{db: d, limiter: newKeyRateLimiter()}
}

func (a *DeviceAuth) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHdr, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		key := strings.TrimSpace(authHdr[len(prefix):])
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if !a.limiter.allow(key) {
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}

		device, err := a.db.GetDeviceByKey(key)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyUserID, device.UserID)
		next(w, r.WithContext(ctx))
	}
}

// UserIDFromContext extracts the user_id stashed by Wrap. Returns 0, false
// if the middleware wasn't applied.
func UserIDFromContext(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(ctxKeyUserID).(int)
	return v, ok
}

// keyRateLimiter is a simple sliding-window counter: up to 60 requests
// per rolling minute per key. A janitor goroutine evicts idle entries
// once a minute to keep the map bounded.
type keyRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	times []time.Time // timestamps of the last ≤60 requests
}

func newKeyRateLimiter() *keyRateLimiter {
	l := &keyRateLimiter{windows: make(map[string]*window)}
	go l.janitor()
	return l
}

const (
	rateLimitMax    = 60
	rateLimitWindow = time.Minute
)

func (l *keyRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok {
		w = &window{}
		l.windows[key] = w
	}

	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)
	kept := w.times[:0]
	for _, t := range w.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	w.times = kept

	if len(w.times) >= rateLimitMax {
		return false
	}
	w.times = append(w.times, now)
	return true
}

func (l *keyRateLimiter) janitor() {
	ticker := time.NewTicker(rateLimitWindow)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-rateLimitWindow)
		for k, w := range l.windows {
			if len(w.times) == 0 || w.times[len(w.times)-1].Before(cutoff) {
				delete(l.windows, k)
			}
		}
		l.mu.Unlock()
	}
}
```

- [ ] **Step 2: Build**

Run: `cd server && go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Device-key auth middleware + rate-limiter для sync API
EOF
git add server/internal/admin/auth_device.go CHANGELOG.new.md
git commit -m "feat(admin): device-key auth middleware with rate limiter [skip-deploy]"
```

---

## Task 8: Sync HTTP handler

**Files:**
- Create: `server/internal/admin/sync.go`
- Modify: `server/internal/admin/admin.go` — mount route, add `DeviceAuth` to `Handler`

- [ ] **Step 1: Create sync.go with handler**

Create `server/internal/admin/sync.go`:

```go
package admin

import (
	"encoding/json"
	"log"
	"net/http"

	"proxyness/server/internal/db"
)

// Wire types for POST /api/sync.

type syncRequest struct {
	LastSyncAt int64    `json:"last_sync_at"`
	Ops        []syncOp `json:"ops"`
}

type syncOp struct {
	Op      string   `json:"op"` // "add" | "remove" | "enable" | "disable"
	LocalID *int     `json:"local_id,omitempty"`
	SiteID  int      `json:"site_id,omitempty"`
	Site    *siteDTO `json:"site,omitempty"`
	At      int64    `json:"at"`
}

type siteDTO struct {
	PrimaryDomain string `json:"primary_domain"`
	Label         string `json:"label"`
}

type syncResponse struct {
	OpResults  []opResult    `json:"op_results"`
	MySites    []db.UserSite `json:"my_sites"`
	ServerTime int64         `json:"server_time"`
}

type opResult struct {
	LocalID *int   `json:"local_id,omitempty"`
	SiteID  int    `json:"site_id,omitempty"`
	Status  string `json:"status"` // "ok" | "error" | "invalid" | "stale"
	Deduped bool   `json:"deduped,omitempty"`
	Message string `json:"message,omitempty"`
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	// One transaction for all ops.
	tx, err := h.db.SQL().Begin()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] begin: %v", err)
		return
	}
	defer tx.Rollback()

	results := make([]opResult, 0, len(req.Ops))
	for _, op := range req.Ops {
		results = append(results, h.applyOp(tx, userID, op))
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] commit: %v", err)
		return
	}

	mySites, err := h.db.GetMySites(userID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		log.Printf("[sync] GetMySites: %v", err)
		return
	}

	writeJSON(w, http.StatusOK, syncResponse{
		OpResults:  results,
		MySites:    mySites,
		ServerTime: nowUnix(),
	})
}

func (h *Handler) applyOp(tx *db.Tx, userID int, op syncOp) opResult {
	res := opResult{LocalID: op.LocalID, SiteID: op.SiteID}

	switch op.Op {
	case "add":
		if op.Site == nil {
			res.Status = "invalid"
			res.Message = "missing site"
			return res
		}
		r, err := h.db.ApplyAddOp(tx, userID, op.Site.PrimaryDomain, op.Site.Label, op.At)
		if err != nil {
			res.Status = "invalid"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"
		res.SiteID = r.SiteID
		res.Deduped = r.Deduped

	case "remove":
		if op.SiteID == 0 {
			res.Status = "invalid"
			res.Message = "missing site_id"
			return res
		}
		if err := h.db.ApplyRemoveOp(tx, userID, op.SiteID); err != nil {
			res.Status = "error"
			res.Message = err.Error()
			return res
		}
		res.Status = "ok"

	case "enable", "disable":
		enabled := op.Op == "enable"
		if op.SiteID == 0 {
			res.Status = "invalid"
			res.Message = "missing site_id"
			return res
		}
		switch h.db.ApplyToggleOp(tx, userID, op.SiteID, enabled, op.At) {
		case db.ToggleOK:
			res.Status = "ok"
		case db.ToggleStale:
			res.Status = "stale"
		case db.ToggleNotFound:
			res.Status = "error"
			res.Message = "site not found"
		}

	default:
		res.Status = "invalid"
		res.Message = "unknown op: " + op.Op
	}
	return res
}
```

The handler references `h.db.SQL()` (returns the `*sql.DB` for starting transactions) and `nowUnix()`. Those aren't in the current codebase — add them:

In `server/internal/db/db.go`, append near other exported methods:

```go
// SQL returns the underlying *sql.DB. Used by handlers that need to
// drive a transaction manually.
func (d *DB) SQL() *sql.DB {
	return d.sql
}
```

Also add a type alias so the sync handler doesn't leak `database/sql` into its signatures:

At the top of `server/internal/db/sites.go` (after imports), add:

```go
// Tx is an alias for *sql.Tx so callers outside this package don't need
// to import database/sql to construct a transaction argument.
type Tx = sql.Tx
```

And add `nowUnix` in `server/internal/admin/sync.go`:

```go
import "time"
// ... existing imports

func nowUnix() int64 { return time.Now().Unix() }
```

- [ ] **Step 2: Mount the route**

Edit `server/internal/admin/admin.go`. Add a field to `Handler`:

```go
type Handler struct {
	db           *db.DB
	tracker      *stats.Tracker
	user         string
	password     string
	downloadsDir string
	deviceAuth   *DeviceAuth
	mux          *http.ServeMux
}
```

In `NewHandler`, after `h := &Handler{...}`, add:

```go
	h.deviceAuth = NewDeviceAuth(d)
```

And register the route near the other public endpoints (after `POST /api/unlock-device`):

```go
	mux.HandleFunc("POST /api/sync", h.deviceAuth.Wrap(h.handleSync))
```

- [ ] **Step 3: Build**

Run: `cd server && go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
POST /api/sync — batch sync endpoint для sites catalog
EOF
git add server/internal/db/db.go server/internal/db/sites.go server/internal/admin/sync.go server/internal/admin/admin.go CHANGELOG.new.md
git commit -m "feat(admin): POST /api/sync batch endpoint [skip-deploy]"
```

---

## Task 9: Sync handler integration test

**Files:**
- Create: `server/internal/admin/sync_test.go`

- [ ] **Step 1: Write the failing test**

Create `server/internal/admin/sync_test.go`:

```go
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"proxyness/server/internal/db"
)

func testHandler(t *testing.T) (*Handler, *db.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	u, err := d.CreateUser("alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	dev, err := d.CreateDevice(u.ID, "mac")
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	h := NewHandler(d, nil, "admin", "pw", t.TempDir())
	return h, d, dev.Key
}

func postSync(t *testing.T, h *Handler, key string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/sync", bytes.NewReader(buf))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSyncRejectsMissingAuth(t *testing.T) {
	h, _, _ := testHandler(t)
	r := httptest.NewRequest("POST", "/api/sync", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

func TestSyncEmptyOpsReturnsSnapshot(t *testing.T) {
	h, _, key := testHandler(t)

	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops":          []interface{}{},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["my_sites"].([]interface{}); !ok {
		t.Fatalf("my_sites missing: %v", resp)
	}
}

func TestSyncAddOpRoundTrip(t *testing.T) {
	h, _, key := testHandler(t)

	localID := -1
	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{
				"op":       "add",
				"local_id": localID,
				"site":     map[string]string{"primary_domain": "example.com", "label": "Example"},
				"at":       1000,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		OpResults []struct {
			LocalID *int   `json:"local_id"`
			Status  string `json:"status"`
			SiteID  int    `json:"site_id"`
		} `json:"op_results"`
		MySites []struct {
			ID            int      `json:"id"`
			Slug          string   `json:"slug"`
			Label         string   `json:"label"`
			Domains       []string `json:"domains"`
			Enabled       bool     `json:"enabled"`
		} `json:"my_sites"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.OpResults) != 1 || resp.OpResults[0].Status != "ok" {
		t.Fatalf("op_results = %+v", resp.OpResults)
	}
	found := false
	for _, s := range resp.MySites {
		if s.Slug == "example" && s.Enabled {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("example.com not in my_sites: %+v", resp.MySites)
	}
}

func TestSyncInvalidDomainReturnsInvalidStatus(t *testing.T) {
	h, _, key := testHandler(t)

	w := postSync(t, h, key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{
				"op":       "add",
				"local_id": -1,
				"site":     map[string]string{"primary_domain": "NOT A DOMAIN", "label": "X"},
				"at":       1000,
			},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		OpResults []struct{ Status string } `json:"op_results"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.OpResults[0].Status != "invalid" {
		t.Fatalf("status = %q, want invalid", resp.OpResults[0].Status)
	}
}
```

- [ ] **Step 2: Run to verify they pass**

Run: `cd server && go test ./internal/admin/ -run TestSync -v`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## improvement
Тесты на sync handler
EOF
git add server/internal/admin/sync_test.go CHANGELOG.new.md
git commit -m "test(admin): sync handler round-trip tests [skip-deploy]"
```

---

## Task 10: export-seed CLI tool

**Files:**
- Create: `server/cmd/export-seed/main.go`

- [ ] **Step 1: Create the tool**

Create `server/cmd/export-seed/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"proxyness/server/internal/db"
)

// Shape emitted to client/resources/seed_sites.json. Fields match what
// the client sync module expects as its initial bootstrap state.
type seedJSON struct {
	ID            int      `json:"id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	PrimaryDomain string   `json:"primary_domain"`
	Domains       []string `json:"domains"`
	IPs           []string `json:"ips"`
}

func main() {
	entries := db.ExportSeedSites()
	out := make([]seedJSON, 0, len(entries))
	for i, s := range entries {
		domains := append([]string{s.PrimaryDomain}, s.Domains...)
		ips := s.IPs
		if ips == nil {
			ips = []string{}
		}
		out = append(out, seedJSON{
			ID:            i + 1,
			Slug:          s.Slug,
			Label:         s.Label,
			PrimaryDomain: s.PrimaryDomain,
			Domains:       domains,
			IPs:           ips,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Add the exporter to db package**

In `server/internal/db/seed_sites.go`, append:

```go
// ExportSeedSites returns the seed slice for tools like cmd/export-seed.
// It's exported only because export-seed lives in a different package.
func ExportSeedSites() []SeedSite {
	return seedSites
}
```

- [ ] **Step 3: Run the tool to verify output**

Run:
```bash
cd /Users/ilyasmurov/projects/smurov/proxy
go run ./server/cmd/export-seed > /tmp/seed_preview.json && head -30 /tmp/seed_preview.json
```

Expected: JSON array with 10 entries starting with `{"id": 1, "slug": "youtube", ...}`.

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
CLI export-seed для генерации seed_sites.json для клиента
EOF
git add server/cmd/export-seed/main.go server/internal/db/seed_sites.go CHANGELOG.new.md
git commit -m "feat(cmd): export-seed CLI tool [skip-deploy]"
```

---

## Task 11: Build integration — Makefile + .gitignore

**Files:**
- Modify: `Makefile`
- Modify: `.gitignore`

- [ ] **Step 1: Update the Makefile build-client target**

Edit `Makefile`. Find the `build-client:` target (currently depends on `build-daemon build-helper`) and prepend the seed export step:

```makefile
build-client: build-daemon build-helper
	mkdir -p client/resources
	go run ./server/cmd/export-seed > client/resources/seed_sites.json
	cd client && npm run build
```

(Adjust to match the exact current recipe — replace the first step inside the recipe with these three lines, keeping any other existing steps.)

- [ ] **Step 2: Ignore the generated file**

Edit `.gitignore`. Append:

```
client/resources/seed_sites.json
```

- [ ] **Step 3: Run export manually to verify**

```bash
mkdir -p client/resources
go run ./server/cmd/export-seed > client/resources/seed_sites.json
ls -la client/resources/seed_sites.json
```

Expected: file exists, non-empty.

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
make build-client генерирует seed_sites.json из Go-источника
EOF
git add Makefile .gitignore CHANGELOG.new.md
git commit -m "build: generate seed_sites.json during build-client [skip-deploy]"
```

---

## Task 12: Client — shared types

**Files:**
- Create: `client/src/renderer/sites/types.ts`

- [ ] **Step 1: Create the types file**

Create `client/src/renderer/sites/types.ts`:

```ts
// Shared types for the sites sync module. Mirrors the server wire
// protocol defined in docs/superpowers/specs/2026-04-08-sites-catalog-sync.md.

export interface LocalSite {
  id: number; // positive = confirmed server id, negative = unconfirmed temp id
  slug: string;
  label: string;
  domains: string[]; // includes primary_domain as index [0] where possible
  ips: string[];
  enabled: boolean;
  updatedAt: number; // unix seconds
}

export type PendingOp =
  | { op: "add";     localId: number; site: { primary_domain: string; label: string }; at: number }
  | { op: "remove";  siteId: number; at: number }
  | { op: "enable";  siteId: number; at: number }
  | { op: "disable"; siteId: number; at: number };

export interface SyncRequest {
  last_sync_at: number;
  ops: Array<
    | { op: "add"; local_id: number; site: { primary_domain: string; label: string }; at: number }
    | { op: "remove"; site_id: number; at: number }
    | { op: "enable"; site_id: number; at: number }
    | { op: "disable"; site_id: number; at: number }
  >;
}

export interface OpResult {
  local_id?: number;
  site_id?: number;
  status: "ok" | "error" | "invalid" | "stale";
  deduped?: boolean;
  message?: string;
}

export interface RemoteSite {
  id: number;
  slug: string;
  label: string;
  domains: string[];
  ips: string[];
  enabled: boolean;
  updated_at: number;
}

export interface SyncResponse {
  op_results: OpResult[];
  my_sites: RemoteSite[];
  server_time: number;
}

export interface SyncResult {
  ok: boolean;
  error?: string;
}
```

- [ ] **Step 2: Build (type-check)**

Run: `cd client && npx tsc --noEmit`
Expected: success.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Типы клиентского модуля sites
EOF
git add client/src/renderer/sites/types.ts CHANGELOG.new.md
git commit -m "feat(client): sites module types [skip-deploy]"
```

---

## Task 13: Client — localStorage helpers + PAC utility

**Files:**
- Create: `client/src/renderer/sites/storage.ts`
- Create: `client/src/renderer/sites/pac.ts`

- [ ] **Step 1: Create storage.ts**

Create `client/src/renderer/sites/storage.ts`:

```ts
import type { LocalSite, PendingOp } from "./types";

// localStorage key namespace. Keep synced with the migration path that
// deletes legacy keys below.
const KEY_LOCAL     = "proxyness-local-sites";
const KEY_PENDING   = "proxyness-pending-ops";
const KEY_LAST_SYNC = "proxyness-last-sync-at";

// Legacy keys kept so the migration in sync.ts can find them.
export const LEGACY_KEY_SITES         = "proxyness-sites";
export const LEGACY_KEY_ENABLED_SITES = "proxyness-enabled-sites";

export interface PersistedState {
  localSites: LocalSite[];
  pendingOps: PendingOp[];
  lastSyncAt: number;
}

export function loadState(): PersistedState {
  return {
    localSites: readJSON<LocalSite[]>(KEY_LOCAL, []),
    pendingOps: readJSON<PendingOp[]>(KEY_PENDING, []),
    lastSyncAt: readJSON<number>(KEY_LAST_SYNC, 0),
  };
}

export function saveLocalSites(sites: LocalSite[]): void {
  localStorage.setItem(KEY_LOCAL, JSON.stringify(sites));
}

export function savePendingOps(ops: PendingOp[]): void {
  localStorage.setItem(KEY_PENDING, JSON.stringify(ops));
}

export function saveLastSyncAt(at: number): void {
  localStorage.setItem(KEY_LAST_SYNC, JSON.stringify(at));
}

export function hasLocalSites(): boolean {
  return localStorage.getItem(KEY_LOCAL) !== null;
}

export function readLegacySites(): string | null {
  return localStorage.getItem(LEGACY_KEY_SITES);
}

export function readLegacyEnabled(): string | null {
  return localStorage.getItem(LEGACY_KEY_ENABLED_SITES);
}

export function clearLegacy(): void {
  localStorage.removeItem(LEGACY_KEY_SITES);
  localStorage.removeItem(LEGACY_KEY_ENABLED_SITES);
}

function readJSON<T>(key: string, fallback: T): T {
  const raw = localStorage.getItem(key);
  if (raw == null) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}
```

- [ ] **Step 2: Create pac.ts**

Create `client/src/renderer/sites/pac.ts`:

```ts
// expandDomains generates the flat domain list sent to the daemon's
// /pac/sites endpoint. For each input domain we also add "www." and
// "*." variants because the PAC file in the daemon matches by suffix.
//
// Before the sites catalog refactor, this lived inside AppRules.tsx
// as a useCallback and relied on a hardcoded RELATED_DOMAINS map. Now
// related domains arrive already joined from the server via
// LocalSite.domains, so this function just does the www/* expansion.
export function expandDomains(domains: string[]): string[] {
  const out = new Set<string>();
  for (const d of domains) {
    if (!d) continue;
    const clean = d.trim().toLowerCase();
    if (!clean) continue;
    out.add(clean);
    if (!clean.startsWith("www.")) {
      out.add("www." + clean);
    }
    out.add("*." + clean);
  }
  return [...out];
}
```

- [ ] **Step 3: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: success.

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Client storage helpers + PAC expand утилита
EOF
git add client/src/renderer/sites/storage.ts client/src/renderer/sites/pac.ts CHANGELOG.new.md
git commit -m "feat(client): sites storage helpers and PAC utility [skip-deploy]"
```

---

## Task 14: Client — sync module (core)

**Files:**
- Create: `client/src/renderer/sites/sync.ts`

- [ ] **Step 1: Create the core module**

Create `client/src/renderer/sites/sync.ts`:

```ts
import type {
  LocalSite,
  PendingOp,
  SyncRequest,
  SyncResponse,
  SyncResult,
  RemoteSite,
} from "./types";
import {
  loadState,
  saveLocalSites,
  savePendingOps,
  saveLastSyncAt,
  hasLocalSites,
  readLegacySites,
  readLegacyEnabled,
  clearLegacy,
} from "./storage";

const API_BASE = "https://proxyness.smurov.com";
const STORAGE_KEY = "proxyness-key"; // same key the tunnel uses

// Module-level state; initialized on first getState() call.
let localSites: LocalSite[] = [];
let pendingOps: PendingOp[] = [];
let lastSyncAt = 0;
let initialized = false;
let tempIdSeq = -1;

const listeners = new Set<() => void>();

function notify(): void {
  for (const fn of listeners) fn();
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function initOnce(): void {
  if (initialized) return;
  initialized = true;

  const state = loadState();
  localSites = state.localSites;
  pendingOps = state.pendingOps;
  lastSyncAt = state.lastSyncAt;

  if (!hasLocalSites()) {
    bootstrapFromBundle();
  }
  runLegacyMigrationIfNeeded();
}

export function getLocalSites(): LocalSite[] {
  initOnce();
  return localSites;
}

export function getLastSyncAt(): number {
  initOnce();
  return lastSyncAt;
}

export function addSite(primaryDomain: string, label: string): LocalSite {
  initOnce();
  const now = Math.floor(Date.now() / 1000);
  const id = tempIdSeq--;

  const site: LocalSite = {
    id,
    slug: label.toLowerCase().replace(/[^a-z0-9]+/g, "").slice(0, 32) || "site",
    label,
    domains: [primaryDomain.toLowerCase()],
    ips: [],
    enabled: true,
    updatedAt: now,
  };
  localSites = [...localSites, site];
  pendingOps = [
    ...pendingOps,
    { op: "add", localId: id, site: { primary_domain: primaryDomain.toLowerCase(), label }, at: now },
  ];
  persist();
  notify();
  return site;
}

export function removeSite(siteId: number): void {
  initOnce();
  const now = Math.floor(Date.now() / 1000);
  localSites = localSites.filter((s) => s.id !== siteId);
  // Only queue a server-side op for positive ids — negatives are unconfirmed adds
  if (siteId > 0) {
    pendingOps = [...pendingOps, { op: "remove", siteId, at: now }];
  } else {
    // Strip the pending add for this temp id (it never made it to the server)
    pendingOps = pendingOps.filter((op) => op.op !== "add" || op.localId !== siteId);
  }
  persist();
  notify();
}

export function toggleSite(siteId: number, enabled: boolean): void {
  initOnce();
  const now = Math.floor(Date.now() / 1000);
  localSites = localSites.map((s) =>
    s.id === siteId ? { ...s, enabled, updatedAt: now } : s
  );
  if (siteId > 0) {
    pendingOps = [...pendingOps, { op: enabled ? "enable" : "disable", siteId, at: now }];
  }
  persist();
  notify();
}

export async function sync(): Promise<SyncResult> {
  initOnce();
  const key = localStorage.getItem(STORAGE_KEY);
  if (!key) return { ok: false, error: "no key" };

  const requestBody: SyncRequest = {
    last_sync_at: lastSyncAt,
    ops: pendingOps.map(toWireOp),
  };

  let resp: Response;
  try {
    resp = await fetch(`${API_BASE}/api/sync`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${key}`,
      },
      body: JSON.stringify(requestBody),
    });
  } catch (e) {
    return { ok: false, error: String(e) };
  }

  if (!resp.ok) {
    return { ok: false, error: `HTTP ${resp.status}` };
  }

  let body: SyncResponse;
  try {
    body = (await resp.json()) as SyncResponse;
  } catch (e) {
    return { ok: false, error: "bad json" };
  }

  // Log non-ok op results for debugging; drop them either way.
  for (const r of body.op_results) {
    if (r.status !== "ok") {
      console.warn("[sync] op result", r);
    }
  }

  localSites = body.my_sites.map(remoteToLocal);
  pendingOps = [];
  lastSyncAt = body.server_time;
  persist();
  notify();

  return { ok: true };
}

function toWireOp(op: PendingOp): SyncRequest["ops"][number] {
  switch (op.op) {
    case "add":
      return { op: "add", local_id: op.localId, site: op.site, at: op.at };
    case "remove":
      return { op: "remove", site_id: op.siteId, at: op.at };
    case "enable":
      return { op: "enable", site_id: op.siteId, at: op.at };
    case "disable":
      return { op: "disable", site_id: op.siteId, at: op.at };
  }
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

function persist(): void {
  saveLocalSites(localSites);
  savePendingOps(pendingOps);
  saveLastSyncAt(lastSyncAt);
}

function bootstrapFromBundle(): void {
  // The bundled seed is a tiny JSON file shipped next to the app binary.
  // Electron main process exposes it via window.appInfo.getSeedSites().
  // If the seed isn't available (dev build) — start empty and rely on sync.
  const seed = (window as any).appInfo?.getSeedSites?.();
  if (!Array.isArray(seed)) {
    localSites = [];
    persist();
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
  persist();
}

function runLegacyMigrationIfNeeded(): void {
  const legacyCustom = readLegacySites();
  const legacyEnabled = readLegacyEnabled();
  if (legacyCustom == null && legacyEnabled == null) return;

  try {
    const custom: Array<{ domain: string; label: string }> = legacyCustom ? JSON.parse(legacyCustom) : [];
    const now = Math.floor(Date.now() / 1000);
    for (const s of custom) {
      if (!s.domain) continue;
      const id = tempIdSeq--;
      pendingOps = [
        ...pendingOps,
        { op: "add", localId: id, site: { primary_domain: s.domain.toLowerCase(), label: s.label || s.domain }, at: now },
      ];
    }

    // Legacy enabled state is NOT mapped — built-ins default to enabled and
    // custom sites always come in as enabled via their add op. A richer
    // migration can be added later if it turns out users had disabled states
    // they wanted preserved.

    persist();
    // Don't clear legacy keys yet — wait for the first successful sync in
    // case we need to retry. The first sync() that returns ok will call
    // clearLegacy() through the post-sync completion hook below.
  } catch (e) {
    console.warn("[sync] legacy migration failed", e);
  }
}

export function finalizeLegacyMigration(): void {
  clearLegacy();
}
```

- [ ] **Step 2: Wire legacy cleanup after successful sync**

At the bottom of the `sync()` function's success block (right before `return { ok: true }`), add:

```ts
  finalizeLegacyMigration();
```

- [ ] **Step 3: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: success (there may be warnings about `window.appInfo?.getSeedSites` — that's expected, the bundle is loaded via preload in Task 16).

- [ ] **Step 4: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Core sync модуль для клиентского каталога сайтов
EOF
git add client/src/renderer/sites/sync.ts CHANGELOG.new.md
git commit -m "feat(client): sites sync module [skip-deploy]"
```

---

## Task 15: Client — useSites React hook

**Files:**
- Create: `client/src/renderer/sites/useSites.ts`

- [ ] **Step 1: Create the hook**

Create `client/src/renderer/sites/useSites.ts`:

```ts
import { useEffect, useState, useCallback, useRef } from "react";
import * as syncModule from "./sync";
import type { LocalSite } from "./types";

interface UseSitesReturn {
  sites: LocalSite[];
  syncing: boolean;
  lastSyncAt: number;
  addSite: (primaryDomain: string, label: string) => LocalSite;
  removeSite: (siteId: number) => void;
  toggleSite: (siteId: number, enabled: boolean) => void;
  syncNow: () => Promise<void>;
}

export function useSites(): UseSitesReturn {
  // Ensure the module is initialized before first render
  const [sites, setSites] = useState<LocalSite[]>(() => {
    syncModule.initOnce();
    return syncModule.getLocalSites();
  });
  const [syncing, setSyncing] = useState(false);
  const [lastSyncAt, setLastSyncAt] = useState<number>(() => syncModule.getLastSyncAt());
  const syncingRef = useRef(false);

  const syncNow = useCallback(async () => {
    if (syncingRef.current) return;
    syncingRef.current = true;
    setSyncing(true);
    try {
      await syncModule.sync();
    } finally {
      syncingRef.current = false;
      setSyncing(false);
    }
  }, []);

  useEffect(() => {
    // Subscribe to state changes
    const unsub = syncModule.subscribe(() => {
      setSites([...syncModule.getLocalSites()]);
      setLastSyncAt(syncModule.getLastSyncAt());
    });

    // Initial sync after mount
    syncNow();

    // Sync on online event
    const onOnline = () => {
      syncNow();
    };
    window.addEventListener("online", onOnline);

    // Periodic sync every 5 minutes
    const interval = setInterval(syncNow, 5 * 60 * 1000);

    return () => {
      unsub();
      window.removeEventListener("online", onOnline);
      clearInterval(interval);
    };
  }, [syncNow]);

  return {
    sites,
    syncing,
    lastSyncAt,
    addSite: syncModule.addSite,
    removeSite: syncModule.removeSite,
    toggleSite: syncModule.toggleSite,
    syncNow,
  };
}
```

- [ ] **Step 2: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: success.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
useSites React hook с триггерами sync (старт, online, 5-мин интервал)
EOF
git add client/src/renderer/sites/useSites.ts CHANGELOG.new.md
git commit -m "feat(client): useSites React hook [skip-deploy]"
```

---

## Task 16: Electron main — expose seed_sites.json to renderer

**Files:**
- Modify: `client/src/main/preload.ts` (or whichever file defines `window.appInfo`)
- Modify: `client/src/main/index.ts` (or the main process entry) — add an IPC handler if needed

- [ ] **Step 1: Locate the preload and main process wiring**

Run:
```bash
grep -rn "appInfo\|getSeedSites\|contextBridge" client/src/main/ 2>/dev/null | head -30
```

Identify:
- The file that registers `window.appInfo` via `contextBridge.exposeInMainWorld`.
- The file that handles `ipcMain.handle` for the other `appInfo` methods (like `getVersion`).

- [ ] **Step 2: Add `getSeedSites` to the preload bridge**

In the preload file where `appInfo` is defined, add a new function:

```ts
const appInfo = {
  // ... existing methods: getVersion, openLogs, setTrayStatus, ...
  getSeedSites: () => ipcRenderer.invoke("app:getSeedSites"),
};
```

- [ ] **Step 3: Add the main-process IPC handler**

In the main process entry file (where other `ipcMain.handle` calls live), register:

```ts
import * as fs from "fs";
import * as path from "path";

ipcMain.handle("app:getSeedSites", () => {
  try {
    // In production the file is bundled next to the daemon binary under
    // `resources/seed_sites.json`. In dev it's at the repo root
    // `client/resources/seed_sites.json`.
    const candidates = [
      path.join(process.resourcesPath ?? "", "seed_sites.json"),
      path.join(__dirname, "..", "..", "resources", "seed_sites.json"),
      path.join(__dirname, "..", "..", "..", "client", "resources", "seed_sites.json"),
    ];
    for (const p of candidates) {
      if (fs.existsSync(p)) {
        return JSON.parse(fs.readFileSync(p, "utf8"));
      }
    }
    return null;
  } catch (e) {
    console.warn("[main] getSeedSites failed", e);
    return null;
  }
});
```

Adjust the import style (`require` vs ES imports) to match the existing file.

- [ ] **Step 4: Wait — the API is async, but sync.ts calls `getSeedSites()` synchronously**

Revisit `client/src/renderer/sites/sync.ts`, `bootstrapFromBundle()`. Since the bridge returns a promise, make bootstrap async:

Change `initOnce` to:

```ts
export async function initOnce(): Promise<void> {
  if (initialized) return;
  initialized = true;

  const state = loadState();
  localSites = state.localSites;
  pendingOps = state.pendingOps;
  lastSyncAt = state.lastSyncAt;

  if (!hasLocalSites()) {
    await bootstrapFromBundle();
  }
  runLegacyMigrationIfNeeded();
}
```

And `bootstrapFromBundle`:

```ts
async function bootstrapFromBundle(): Promise<void> {
  try {
    const seed = await (window as any).appInfo?.getSeedSites?.();
    if (!Array.isArray(seed)) {
      localSites = [];
      persist();
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
    persist();
  } catch {
    localSites = [];
    persist();
  }
}
```

Update `useSites.ts` to await the init:

```ts
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

  // ... rest unchanged, but depend on `ready`
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

  return { sites, syncing, lastSyncAt, addSite: syncModule.addSite, removeSite: syncModule.removeSite, toggleSite: syncModule.toggleSite, syncNow };
}
```

Also update the other `getLocalSites()` / `getLastSyncAt()` callers in sync.ts to remove their `initOnce()` calls (they can only be called after `initOnce` has completed; the hook guarantees this by awaiting).

- [ ] **Step 5: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: success.

- [ ] **Step 6: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
Electron main процесс отдаёт seed_sites.json в renderer
EOF
git add client/src/main/ client/src/renderer/sites/sync.ts client/src/renderer/sites/useSites.ts CHANGELOG.new.md
git commit -m "feat(client): expose bundled seed_sites.json via IPC [skip-deploy]"
```

---

## Task 17: Rewrite AppRules.tsx to use useSites

**Files:**
- Modify: `client/src/renderer/components/AppRules.tsx`

- [ ] **Step 1: Delete the hardcoded data structures**

Edit `client/src/renderer/components/AppRules.tsx`. Remove:

- The `DEFAULT_SITES` const (lines ~38-50)
- The `RELATED_DOMAINS` const (lines ~52-86)
- The `STORAGE_KEY_SITES`, `STORAGE_KEY_ENABLED_SITES` constants (lines ~248-249)
- The `loadSites()`, `saveCustomSites()`, `loadEnabledSites()`, `saveEnabledSites()` helper functions
- The inline `expandDomains` useCallback

Keep: `KNOWN_APPS`, all icon/color maps, `BrandIcon`, `LetterAvatar`, `SiteTileIcon`, `labelFromDomain`, `hashHue`, `STORAGE_KEY_NO_TLS` (still used for app TLS toggle).

- [ ] **Step 2: Introduce useSites**

Near the top of `AppRules.tsx`:

```tsx
import { useSites } from "../sites/useSites";
import { expandDomains } from "../sites/pac";
```

Inside the `AppRules` component, replace the state declarations:

```tsx
const [sites, setSites] = useState<BrowserSite[]>(loadSites);
const [enabledSites, setEnabledSites] = useState<Set<string>>(loadEnabledSites);
```

with:

```tsx
const { sites: localSites, addSite, removeSite, toggleSite } = useSites();
// All-sites toggle: a local-only mode flag that bypasses per-site picks.
const [allSitesOn, setAllSitesOn] = useState<boolean>(
  () => localStorage.getItem("proxyness-all-sites-on") !== "false"
);
```

- [ ] **Step 3: Derive the display list and PAC list**

Where the component previously computed `enabledSites` Sets and passed them to PAC:

```tsx
// Enabled sites for PAC: all their domains joined, then expanded.
const enabledDomains = localSites
  .filter((s) => s.enabled)
  .flatMap((s) => s.domains);
const siteDomains = expandDomains(enabledDomains);

const applyPac = useCallback(
  (on: boolean) => {
    if (!on) {
      window.sysproxy?.disable();
      return;
    }
    if (allSitesOn) {
      window.sysproxy?.setPacSites({ proxy_all: true, sites: [] });
    } else {
      window.sysproxy?.setPacSites({ proxy_all: false, sites: siteDomains });
    }
    window.sysproxy?.enable();
  },
  [allSitesOn, siteDomains]
);
```

Re-run `applyPac` via an effect whenever the enabled set changes:

```tsx
useEffect(() => {
  applyPac(browsersOn);
}, [applyPac, browsersOn]);
```

- [ ] **Step 4: Rewrite tile interactions**

Replace toggle/add/remove handlers to call `useSites` methods:

```tsx
const handleToggleTile = (site: LocalSite) => {
  if (allSitesOn) {
    setAllSitesOn(false);
    localStorage.setItem("proxyness-all-sites-on", "false");
  }
  toggleSite(site.id, !site.enabled);
};

const handleToggleAll = () => {
  const next = !allSitesOn;
  setAllSitesOn(next);
  localStorage.setItem("proxyness-all-sites-on", String(next));
};

const handleAddSite = (domain: string, label: string) => {
  addSite(domain, label);
};

const handleRemoveSite = (siteId: number) => {
  removeSite(siteId);
};
```

- [ ] **Step 5: Update the live-hosts matching**

The existing `liveSites` derivation referenced `RELATED_DOMAINS`. Replace with a direct `LocalSite.domains` match:

```tsx
const liveSites = (() => {
  if (activeHosts.length === 0) return new Set<number>();
  const live = new Set<number>();
  const hostMatches = (host: string, pattern: string): boolean =>
    host === pattern || host.endsWith("." + pattern);
  for (const s of localSites) {
    for (const host of activeHosts) {
      if (s.domains.some((p) => hostMatches(host, p))) {
        live.add(s.id);
        break;
      }
    }
  }
  return live;
})();
```

- [ ] **Step 6: Update the grid rendering to iterate over localSites by id**

Update `SitesGrid` usage and its internal iteration to key on `s.id` (number) instead of `s.domain` (string). Change the `enabledSites` prop signature from `Set<string>` to `Set<number>` and compute `enabledSet` from `localSites.filter(s => s.enabled).map(s => s.id)`.

This is a mechanical rename pass. After the edit, type-check catches any missed callsites.

- [ ] **Step 7: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: success. Fix any remaining `BrowserSite` / `enabledSites: Set<string>` mismatches.

- [ ] **Step 8: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## feature
AppRules.tsx использует useSites хук вместо хардкода сайтов
EOF
git add client/src/renderer/components/AppRules.tsx CHANGELOG.new.md
git commit -m "feat(client): wire AppRules.tsx to sites sync module [skip-deploy]"
```

---

## Task 18: Integration test — server sync handler with real DB

**Files:**
- Create: `test/sync_integration_test.go`

- [ ] **Step 1: Write the test**

Create `test/sync_integration_test.go`:

```go
package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"proxyness/server/internal/admin"
	"proxyness/server/internal/db"
)

func TestSyncIntegrationFullFlow(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	user, _ := d.CreateUser("alice")
	dev, _ := d.CreateDevice(user.ID, "mac")

	h := admin.NewHandler(d, nil, "admin", "pw", t.TempDir())

	// 1. Empty sync returns seed-based empty my_sites (user has no enabled sites yet)
	w := postSync(t, h, dev.Key, map[string]interface{}{"last_sync_at": 0, "ops": []interface{}{}})
	if w.Code != http.StatusOK {
		t.Fatalf("empty sync code=%d", w.Code)
	}
	var r1 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r1)
	if len(r1["my_sites"].([]interface{})) != 0 {
		t.Fatalf("expected empty my_sites")
	}

	// 2. Add a seed site by id via toggle — first we need to attach it via add op on its primary domain
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "add", "local_id": -1, "site": map[string]string{"primary_domain": "youtube.com", "label": "YouTube"}, "at": 1000},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("add seed code=%d: %s", w.Code, w.Body.String())
	}
	var r2 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r2)
	ops := r2["op_results"].([]interface{})
	if op := ops[0].(map[string]interface{}); op["status"] != "ok" || op["deduped"] != true {
		t.Fatalf("expected deduped ok, got %+v", op)
	}

	// 3. Disable it
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "disable", "site_id": 1, "at": 2000},
		},
	})
	var r3 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r3)
	sites := r3["my_sites"].([]interface{})
	found := false
	for _, s := range sites {
		m := s.(map[string]interface{})
		if m["id"].(float64) == 1 {
			if m["enabled"].(bool) {
				t.Fatalf("expected disabled")
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("youtube not in my_sites after disable")
	}
}

func postSync(t *testing.T, h *admin.Handler, key string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/sync", bytes.NewReader(buf))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
```

- [ ] **Step 2: Run the test**

Run: `cd test && go test -v -run TestSyncIntegration`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cat > CHANGELOG.new.md <<'EOF'
## improvement
Integration test для POST /api/sync (full round trip)
EOF
git add test/sync_integration_test.go CHANGELOG.new.md
git commit -m "test: sync API integration test [skip-deploy]"
```

---

## Task 19: Final build, full test suite, and manual smoke handoff

**Files:** none — verification.

- [ ] **Step 1: Full Go build + test**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy
make test
```

Expected: all packages pass including `server/internal/db`, `server/internal/admin`, and `test`.

- [ ] **Step 2: Rebuild all binaries**

```bash
make build-daemon build-helper build-server
```

Expected: success.

- [ ] **Step 3: Full client build**

```bash
make build-client
```

Expected: success. Verify `client/resources/seed_sites.json` was generated:

```bash
head -20 client/resources/seed_sites.json
```

- [ ] **Step 4: Manual smoke checklist (hand off to user)**

The user must run through this checklist on a real deployment:

- [ ] Deploy the new server to the VPS (`git push`, CI runs). SSH in and inspect the DB:
  ```bash
  sqlite3 /app/data.sqlite "SELECT id, slug, label, primary_domain FROM sites;"
  ```
  Expected: 10 seeded sites, ids 1-10.
- [ ] Start the client. Grid shows the 10 seeded browser sites as enabled (from bundled seed).
- [ ] After ~1 second, the initial sync runs. Server DB now has `user_sites` rows for this user (all 10 sites enabled). Verify:
  ```bash
  sqlite3 /app/data.sqlite "SELECT * FROM user_sites;"
  ```
- [ ] Disable YouTube via the UI. `user_sites` on the server shows `enabled=0` for site_id=1 within 5 seconds (next sync trigger — if you clicked quickly, the 500ms debounce makes it almost instant).
- [ ] Add a custom site "habr.com" / "Habr". It appears in the grid immediately. Server DB has a new `sites` row with `slug=habr`, `primary_domain=habr.com`, and the user has a `user_sites` row for it.
- [ ] Disable Wi-Fi. Toggle Instagram off. Add "vc.ru" / "VC.RU". Both actions reflect in the UI immediately but don't reach the server.
- [ ] Re-enable Wi-Fi. Within ~2 seconds the pending ops flush. Server reflects the new state.
- [ ] Open the client on a second device (fresh install, paste the same key). The new device's first sync returns the snapshot matching all changes made on the first device, including the disabled YouTube, disabled Instagram, and custom habr/vc.ru entries.

If any step fails, file an issue and fix before moving to Phase B.

- [ ] **Step 5: No commit needed** — implementation is complete once the smoke test passes.

---

## Self-Review

**Spec coverage:** All spec sections are mapped to tasks.

- Security + auth: Task 7 (middleware + rate limit).
- Data model: Task 1 (schema), Task 2 (seed data), Task 3 (seeding).
- Sync API: Task 4 (snapshot), Tasks 5-6 (ops), Task 8 (handler), Task 9 (tests).
- Client state management: Tasks 12-15 (types, storage, PAC, sync module, hook).
- Bootstrap: Task 16 (Electron IPC for seed).
- Legacy migration: Task 14 (inside sync module).
- Server seed tool: Task 10.
- Build integration: Task 11.
- AppRules rewrite: Task 17.
- Testing: Tasks 3, 4, 5, 6, 9, 18.
- Smoke: Task 19.

**Placeholder scan:** No TBD / TODO / "add appropriate" patterns. Every step has either code or an exact command.

**Type consistency:**
- `UserSite` (Go) ↔ `RemoteSite` (TS): matches by field name + casing (snake_case on wire).
- `AddOpResult` (Go) ↔ `OpResult` (TS, inside `op_results[]`): matches.
- `ToggleStatus` (Go) ↔ `OpResult.status` (TS enum `"ok"|"stale"|"error"|"invalid"`): correctly mapped in `handleSync.applyOp`.
- `SeedSite.Domains` (Go) does NOT include `PrimaryDomain`. `ExportSeedSites` + `cmd/export-seed/main.go` deliberately prepend it to produce the JSON's `domains` field. `SeedSitesIfEmpty` inserts primary as a separate is_primary=1 row.
- `LocalSite.id: number` (TS) — negative for pending, positive for confirmed. Enforced in `addSite()` and `toggleSite()` / `removeSite()` filter logic.

**Known risks of this plan (not blockers, just things to watch):**
- Task 17's mechanical rename from `Set<string>` to `Set<number>` in `SitesGrid` may hit many callsites. If it gets messy, split into a smaller prep task that just renames types first, then swaps the data source.
- Task 16 assumes a specific Electron main process layout. If the repo structure differs from the assumption, adjust file paths based on the grep output in Step 1.
- Manual smoke (Task 19) depends on the server being deployed with the new build. If there's an active production tunnel, `[skip-deploy]` in all commits means nothing auto-deploys — a manual `git push` without the tag is needed at the end of Task 19. Coordinate with the user before deploying.
