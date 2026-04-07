# Sites Catalog + Per-User Sync (Phase A) Design

## Overview

Move the currently-hardcoded browser sites list out of the client React code into a shared server-side catalog, and add per-user "enabled sites" state that syncs across all of a user's devices via an offline-first batch sync endpoint. This is **Phase A** of a three-phase feature. Phase B (server-side search UI) and Phase C (daemon watch-mode for automatic domain discovery) get their own specs later and both build on the data model established here.

## Goals

- Move the built-in browser sites (Instagram, Telegram, YouTube, etc.) and their related-domain mappings out of `client/src/renderer/components/AppRules.tsx` and into the server SQLite DB.
- Store per-user enabled state on the server so a site enabled on the user's macbook appears enabled on their Windows device automatically.
- Work fully offline: the user can add sites, toggle them, and delete them while disconnected from the proxy (and from the internet entirely); all those changes flush on next sync.
- Let the user add arbitrary new sites; if the domain is already in the catalog, deduplicate silently; if not, create a new catalog entry owned by that user.
- Keep the existing PAC generation in the daemon unchanged — the client still POSTs a flat expanded domain list to `/pac/sites` just like today.

## Non-Goals

- **Server-side search** (`GET /api/sites/search?q=...`) — Phase B.
- **Watch-mode** domain auto-discovery in the daemon — Phase C.
- Per-user custom labels, per-user ordering of the grid.
- Admin UI for moderating user-submitted sites — deferred until the service opens for subscription.
- Migration from SQLite to Postgres — SQLite is sufficient for the expected scale.
- Changes to the tunnel, TUN engine, transport, helper, or the `/pac/sites` daemon endpoint.

## Architecture

### Security and Auth

The Electron client talks directly to `https://proxy.smurov.com/api/sync` via `net.fetch`. It sends its existing access key in `Authorization: Bearer <key>`. The server adds a middleware that:

- Looks up the key in the existing `devices` table, pulling the associated `user_id`.
- Rejects requests with an invalid or missing key with `401`.
- Scrubs the `Authorization` header from all access logs.
- Applies a per-key rate limit of **60 requests per minute** (in-memory sliding window — no new table, just a `map[key]*limiter` with periodic cleanup).

This is the same trust level the key already grants via the tunnel protocol — adding a sync API doesn't widen the key's scope.

### Data Model (SQLite)

Four new tables, all added to `server/internal/db/db.go` using the existing `CREATE TABLE IF NOT EXISTS` pattern — no new migration framework.

```sql
CREATE TABLE sites (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  slug                TEXT UNIQUE NOT NULL,            -- "instagram"
  label               TEXT NOT NULL,                   -- "Instagram"
  primary_domain      TEXT UNIQUE NOT NULL,            -- "instagram.com" — dedup key
  approved            BOOLEAN NOT NULL DEFAULT 1,      -- for future moderation
  created_by_user_id  INTEGER,                         -- NULL for seeded built-ins
  created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (created_by_user_id) REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE site_domains (
  site_id     INTEGER NOT NULL,
  domain      TEXT NOT NULL,                           -- "cdninstagram.com"
  is_primary  BOOLEAN NOT NULL DEFAULT 0,
  PRIMARY KEY (site_id, domain),
  FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE
);

CREATE TABLE site_ips (
  site_id  INTEGER NOT NULL,
  cidr     TEXT NOT NULL,                              -- "149.154.160.0/20"
  PRIMARY KEY (site_id, cidr),
  FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE
);

CREATE TABLE user_sites (
  user_id     INTEGER NOT NULL,
  site_id     INTEGER NOT NULL,
  enabled     BOOLEAN NOT NULL DEFAULT 1,
  updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (user_id, site_id),
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (site_id) REFERENCES sites(id) ON DELETE CASCADE
);

CREATE INDEX idx_site_domains_domain ON site_domains(domain);
```

**Design notes:**

- `sites.primary_domain` is the dedup key. Two users both adding "instagram.com" get the same `site_id`.
- `site_domains` is separate from `sites` so Phase C (watch-mode) can `INSERT OR IGNORE` new domains one at a time without read-modify-write on a JSON blob.
- `site_ips` is populated only for apps that connect by raw IP (Telegram, etc.); unused in Phase A's PAC flow but wired through so the schema is stable.
- `approved` defaults to `1` in Phase A because there's only one user (you); the column is added now so Phase B can filter search results without a migration.
- "Demand" signal (how many users enabled a given site) is derived on-the-fly via `SELECT COUNT(*) FROM user_sites WHERE site_id = ?` — no dedicated counter.

### Sync API

**Single endpoint.** `POST /api/sync` does both flush and fetch in one round trip.

**Request body:**

```json
{
  "last_sync_at": 1712000000,
  "ops": [
    {"op": "add",     "local_id": -1, "site": {"primary_domain": "example.com", "label": "Example"}, "at": 1712345678},
    {"op": "add",     "local_id": -2, "site": {"primary_domain": "habr.com",    "label": "Habr"},    "at": 1712345679},
    {"op": "enable",  "site_id": 5,  "at": 1712345680},
    {"op": "disable", "site_id": 3,  "at": 1712345681},
    {"op": "remove",  "site_id": 8,  "at": 1712345682}
  ]
}
```

**Response body:**

```json
{
  "op_results": [
    {"local_id": -1, "status": "ok",     "site_id": 42, "deduped": false},
    {"local_id": -2, "status": "ok",     "site_id": 17, "deduped": true},
    {"site_id": 5,   "status": "ok"},
    {"site_id": 3,   "status": "error",  "message": "site not found"},
    {"site_id": 8,   "status": "stale",  "message": "newer server state"}
  ],
  "my_sites": [
    {
      "id": 42, "slug": "example", "label": "Example",
      "domains": ["example.com", "www.example.com"],
      "ips": [],
      "enabled": true,
      "updated_at": 1712345678
    },
    {"id": 17, "slug": "habr", "...": "..."}
  ],
  "server_time": 1712345700
}
```

**Processing semantics:**

- All ops are processed inside a **single `BEGIN IMMEDIATE` SQL transaction** so either the whole batch commits or none of it does. Business-level failures (site not found, invalid domain) do not roll back — they're returned in `op_results` with `status != "ok"` and the rest of the ops still apply.
- **Op ordering inside the batch is preserved** — if the client sends `add example.com` followed by `disable <newid>`, the server handles them in order. Since `add` returns the real site_id, the second op would have to reference the local id — not supported in v1; the client must flush `add` separately if it wants to immediately toggle the new site. This is fine because the client optimistic UI handles the local case and queued ops on fresh sites are naturally "add + enabled=true".
- **Last-write-wins via `at`.** For `enable`/`disable`/`remove`, if the op's `at` is older than the existing `user_sites.updated_at`, the server returns `status: "stale"` and skips the write. If newer or equal, the server applies the change and sets `updated_at = op.at`.
- **Idempotent retries.** Re-submitting the same batch produces the same `my_sites` snapshot: duplicate `add` returns the existing `site_id`, `enable` on already-enabled is a no-op (status `ok`), `disable` on already-disabled is a no-op, `remove` on missing row is `error` not crash.
- **Domain sanitization.** `primary_domain` must match `^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$` — lowercased before matching. Failed domain returns `status: "invalid"` in the op_result.
- **Slug generation.** When creating a new `sites` row from an `add` op, the server derives `slug` from `primary_domain` by stripping the TLD and non-alphanumeric characters: `instagram.com` → `instagram`, `news.ycombinator.com` → `newsycombinator`. If a slug collision exists (another site with the same slug but different primary_domain), append a numeric suffix: `instagram2`, `instagram3`.
- **`my_sites` is always a complete snapshot** of the user's current state after ops apply. It joins `user_sites`, `sites`, `site_domains`, and `site_ips` in one query per user. The client replaces its local cache wholesale with this snapshot — no delta merging needed.
- **First-time call with empty ops** is valid and cheap; it's the pull-only path.

**Operation types (v1):**

| op | fields | effect |
|---|---|---|
| `add` | `local_id`, `site: {primary_domain, label}`, `at` | If site exists by primary_domain → reuse its `site_id` and set `deduped: true`. Else create `sites`+`site_domains` row. If no `user_sites` row exists for (user, site) → insert one with `enabled=true, updated_at=at`. If a `user_sites` row already exists → no-op (status `ok`, no changes to `enabled`/`updated_at`). This makes `add` idempotent. |
| `remove` | `site_id`, `at` | `DELETE FROM user_sites WHERE user_id=? AND site_id=?`. Catalog untouched. |
| `enable` | `site_id`, `at` | `UPDATE user_sites SET enabled=1, updated_at=at WHERE ... AND updated_at <= at`. |
| `disable` | `site_id`, `at` | Same as `enable` but `enabled=0`. |

No `rename`, no `reorder`, no per-user `custom_label` in v1. These require a new op and column and are easy to add later.

### Client State Management

**Storage module.** New file `client/src/renderer/sites/sync.ts`:

- Persists three keys in `localStorage`:
  - `smurov-proxy-local-sites` — `Array<LocalSite>` where each `LocalSite` is `{id: number, slug, label, domains: string[], ips: string[], enabled: boolean, updatedAt: number, pending?: boolean}`. Negative ids (`-1`, `-2`, ...) are unconfirmed new entries waiting for the server to assign a real id.
  - `smurov-proxy-pending-ops` — the queue flushed on next sync.
  - `smurov-proxy-last-sync-at` — unix timestamp.
- Exports functions:
  - `loadState(): {localSites, pendingOps, lastSyncAt}` — read-only snapshot on mount.
  - `addSite(primaryDomain, label)` — append to both `localSites` (with a fresh negative id) and `pendingOps`. Returns the new `LocalSite` synchronously so the UI can show it immediately.
  - `removeSite(siteId)` — remove from `localSites`, append `{op: "remove", site_id, at}` to `pendingOps`.
  - `toggleSite(siteId, enabled)` — update `localSites[i].enabled`, append `{op: "enable"|"disable", site_id, at}`.
  - `sync(): Promise<SyncResult>` — see below.
- Emits a `"change"` event (via a tiny EventEmitter) so the `useSites()` React hook can re-render on any mutation.

**`sync()` flow:**

1. Read `pendingOps` and `lastSyncAt` from storage.
2. Send `POST /api/sync` with the queue, using the access key in the Authorization header.
3. On network error or non-2xx response → keep the queue intact, return `{ok: false, error}`. Caller can retry later.
4. On success, the client **does not need to patch individual LocalSites** — it simply replaces `localSites` entirely with `response.my_sites`. `op_results` is used only for logging and user-facing warnings:
   - Results with `status: "error"`, `"invalid"`, or `"stale"` — log and (optionally) surface a toast. Drop the op either way; it's done.
   - Results with `status: "ok"` — nothing to do; the `my_sites` snapshot already reflects the change.
   - Any `LocalSite` with a negative id that isn't represented in `my_sites` after the swap is a lost temp entry from a failed `add` — it's correctly gone.
   - Clear `pendingOps`.
   - Write `lastSyncAt = response.server_time`.
5. Emit `"change"`, return `{ok: true}`.

**Sync triggers:**

- On app start (inside `useEffect` of the root component — fires once after mount).
- On `window.addEventListener("online", ...)` — when the browser detects connectivity.
- On tunnel connect — hook into the existing tunnel-connected callback in `App.tsx`. A connect is a strong signal that the user wants to reach the server.
- Periodically every **5 minutes** while the app is open — `setInterval` inside the hook, cleared on unmount.
- Manually when the user explicitly adds/removes/toggles a site — we schedule a `sync()` call via a small debounce (~500ms) so multiple rapid clicks collapse into one HTTP request.

**First-run bootstrap (no localSites, no connectivity).**

The client ships a bundled seed file at `client/resources/seed_sites.json`. This file is generated during `make build-client` by `go run ./server/cmd/export-seed > client/resources/seed_sites.json` so there is exactly one source of truth — the Go seed slice.

On first launch, if `smurov-proxy-local-sites` is absent in localStorage, the sync module loads the bundled JSON and uses it as the initial `localSites` (with positive ids matching the seed's own ids — same ids as the server uses). The first successful `sync()` will return the authoritative snapshot and replace the bootstrap data cleanly.

**Legacy localStorage migration.**

On first launch of the new version, the sync module checks for the old keys `smurov-proxy-sites` and `smurov-proxy-enabled-sites`. If present:

1. Parse the old custom sites list.
2. For each custom site, push an `{op: "add", local_id, site: {primary_domain, label}, at: now}` into `pendingOps`. Built-in sites that were enabled don't need explicit ops — they're already represented by the bootstrap seed; the user's enabled/disabled state for them becomes the default (enabled).
3. Preserve the old enabled/disabled state where possible: if the old `enabledSites` set excluded a built-in, push a `disable` op for its corresponding site_id (looked up from the bundled seed by domain).
4. After the next successful sync that processes those ops, delete both old keys.
5. If sync fails (offline), keep the old keys and retry on the next launch.

**`AppRules.tsx` rewrite.** The component loses its `BROWSER_SITES` hardcode, `RELATED_DOMAINS` map, `saveCustomSites()`/`loadSites()` helpers, and `STORAGE_KEY_SITES`/`STORAGE_KEY_ENABLED_SITES` constants. It wraps a new `useSites()` hook that returns `{sites, addSite, removeSite, toggleSite, syncing, lastSyncAt}`. All existing visual behavior (tiles, LIVE indicators, add-site modal, toggle UI) stays the same; only the data source changes.

**PAC update path.** Today, `AppRules.tsx` calls `window.sysproxy.setPacSites({proxy_all, sites: siteDomains})` with a flat list of domains expanded via `expandDomains()`. This stays identical — the expansion logic is pulled out into `client/src/renderer/sites/pac.ts` so it can be unit-tested, but the daemon-facing contract is unchanged.

### Server-Side Components

**`server/internal/db/seed_sites.go`** — new file containing the built-in catalog as a Go slice:

```go
type SeedSite struct {
    Slug           string
    Label          string
    PrimaryDomain  string
    Domains        []string  // additional domains, primary_domain not included
    IPs            []string  // CIDRs
}

var seedSites = []SeedSite{
    {Slug: "instagram", Label: "Instagram", PrimaryDomain: "instagram.com",
     Domains: []string{"cdninstagram.com", "fbcdn.net"}, IPs: nil},
    {Slug: "telegram", Label: "Telegram", PrimaryDomain: "telegram.org",
     Domains: []string{"t.me"},
     IPs: []string{"149.154.160.0/20", "91.108.4.0/22"}},
    // ... all existing built-in sites from AppRules.tsx ported here
}
```

The existing built-in list and `RELATED_DOMAINS` map from `AppRules.tsx` are translated into this slice exactly once, at spec-implementation time.

**`db.SeedSitesIfEmpty()`** — new method called from `db.Open()` (or wherever the `CREATE TABLE IF NOT EXISTS` block runs). Logic:

1. `SELECT COUNT(*) FROM sites` — if > 0, return (already seeded, nothing to do on redeploys).
2. Begin transaction.
3. For each `SeedSite` at index `i` (zero-based), insert with an **explicit id** equal to `i+1`:
   `INSERT INTO sites (id, slug, label, primary_domain, approved, created_by_user_id) VALUES (?, ?, ?, ?, 1, NULL)`. Using explicit ids guarantees the bundled `seed_sites.json` can predict the exact same ids without having to query the DB post-insert, and keeps the client's first-run bootstrap trivially in sync. SQLite's `AUTOINCREMENT` sequence then continues from `len(seedSites)+1` for user-created sites.
4. Insert `site_domains` rows: one with `is_primary=1` for `primary_domain`, and one with `is_primary=0` for each additional domain.
5. Insert `site_ips` rows for each CIDR.
6. Commit.

If any step fails, roll back and log — the server still starts, sites just won't be seeded. Next deploy will retry.

**`server/cmd/export-seed/main.go`** — new CLI tool, ~20 lines. Imports `seedSites` from `internal/db` and marshals them to JSON with explicit ids matching the slice index (`i+1`), so the emitted JSON has the same ids the DB seeding will use. Output is an array of `{id, slug, label, domains, ips}` objects, ordered by id. Prints to stdout, exit code 0 on success. Used in `make build-client` like so (updated rule):

```makefile
build-client: build-daemon build-helper
	go run ./server/cmd/export-seed > client/resources/seed_sites.json
	cd client && npm run build
```

**`server/internal/admin/sync.go`** (or the idiomatic file name matching the existing handler layout) — new handler `handleSync(w, r)`:

- Parse request body.
- Pull `user_id` from the request context (set by the new auth middleware).
- Validate — if `ops` is missing or not an array, return `400`.
- Begin `BEGIN IMMEDIATE` transaction.
- Loop through ops, dispatch to `applyAddOp`, `applyRemoveOp`, `applyEnableOp`, `applyDisableOp`. Each helper returns an `OpResult` and either writes to the transaction or marks the result as `error`/`stale`/`invalid`.
- Commit.
- Run the `my_sites` snapshot query:
  ```sql
  SELECT s.id, s.slug, s.label, us.enabled, us.updated_at
  FROM user_sites us JOIN sites s ON us.site_id = s.id
  WHERE us.user_id = ?
  ORDER BY s.label
  ```
  Then for each row fetch its domains and ips in a second query (or one compound query with `GROUP_CONCAT`).
- Return JSON.

**Auth middleware.** New file `server/internal/admin/auth_device.go`:

- `func AuthenticateDevice(next http.Handler) http.Handler` — reads `Authorization: Bearer <key>` header, `SELECT user_id FROM devices WHERE access_key = ?`, stashes `user_id` in `r.Context()`, then calls `next`. On missing/invalid key returns `401`. This middleware wraps only `/api/sync` in v1, not the existing admin endpoints (those keep Basic Auth).
- Rate limit: in-memory `sync.Map[key]*rateLimiter` where rateLimiter is a simple sliding-window counter with a goroutine that runs every minute to evict idle entries.

### Mounting the new endpoint

In the existing server router setup, wrap `/api/sync` with the new `AuthenticateDevice` middleware:

```go
mux.Handle("POST /api/sync", admin.AuthenticateDevice(http.HandlerFunc(adminServer.handleSync)))
```

## Testing Strategy

**Server-side unit tests** (`server/internal/db/sites_test.go`):

- Seed idempotency: call `SeedSitesIfEmpty` twice, verify no duplicates.
- `add` op: new site gets inserted with correct domains/ips; re-add of same primary_domain returns existing id with `deduped: true`.
- `enable`/`disable` LWW: op with `at` older than `updated_at` returns `stale` and leaves row untouched.
- `remove` op: deletes `user_sites` row but leaves `sites` row intact.
- Slug collision: two sites with colliding slug bases get `foo` and `foo2`.

**Server-side handler tests** (`server/internal/admin/sync_test.go`):

- Auth middleware rejects missing/invalid key with 401.
- Full round-trip: send a batch with `add + enable + disable`, assert the response `my_sites` matches expected snapshot.
- Invalid primary_domain in an `add` op returns `status: "invalid"` for that op but the rest of the batch succeeds.
- Rate limit: 61st request in a minute from the same key returns 429.

**Client-side unit tests** (`client/src/renderer/sites/sync.test.ts`, using Vitest — new dev dep if not present):

- `addSite` → `localSites` grows by one entry with a negative id and `pending: true`; `pendingOps` has one `add` entry.
- `sync()` with mocked fetch: after successful response, negative id is replaced with the real id and `pendingOps` is empty.
- `sync()` with network error: state is untouched so a retry sees the same queue.
- Legacy migration: pre-populate old localStorage keys, first run generates the right `pendingOps` and clears them after a mocked successful sync.

**Integration test** (`test/sync_integration_test.go`):

- Spin up an in-memory SQLite DB, bootstrap a user and device, POST `/api/sync` end-to-end with a minimal batch, verify response shape.

**Manual smoke** (after implementation):

1. Build client and server. Start local server pointing at a fresh SQLite file. Deploy once to seed.
2. Open client: grid shows seeded sites.
3. Add a custom site "example.com". Click add. Grid updates immediately. Check server DB: `sites` has an "example" row with `created_by_user_id` set to your user.
4. Disable connection (Wi-Fi off). Toggle Instagram off. Toggle YouTube on. Add another custom site. Grid reflects all changes.
5. Re-enable Wi-Fi. Within ~5 seconds sync runs. Refresh server DB: all three changes are there. Refresh client: state unchanged (no flicker).
6. Open the same client on a second machine (use a second fake device/key). Within first-sync the same sites appear enabled/disabled as on the first machine.

## Acceptance Criteria

- Server SQLite DB has the four new tables and they are seeded from Go code on a fresh install.
- `POST /api/sync` returns a valid snapshot for any authorized device, even with empty ops.
- Client no longer references `BROWSER_SITES` or `RELATED_DOMAINS` hardcodes — they're gone.
- Adding a site offline works: grid updates immediately, the site persists after restart, and flushes to the server once online.
- Enabling a site on device A shows up as enabled on device B after device B's next sync (≤ 5 minutes or immediately on proxy connect, whichever comes first).
- Deduplication works: two users adding `instagram.com` see the same `site_id` and both `user_sites` rows reference it.
- `make build-client` produces `client/resources/seed_sites.json` from the Go source of truth, without manual steps.
- All existing PAC behavior (browsers, LIVE indicators, toggle, per-tile UI) still works — no visual regression in `AppRules.tsx`.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| Sync conflicts between devices (simultaneous offline edits) | Last-write-wins by op `at` timestamp. Good enough for the expected concurrency (one user, a handful of devices). If it becomes a problem → upgrade to a CRDT later. |
| Malicious or buggy client floods `/api/sync` | 60 req/min per-key rate limit; 429 response. |
| User deletes their access key in the client → orphaned `user_sites` | `devices.access_key` changes don't cascade to `users.id`, so `user_sites` stays tied to the user, which is correct. Deleting the user cascades everything. |
| Server DB is unreachable during sync | Client keeps `pendingOps` intact and retries. UI shows nothing bad — the optimistic state is already live. |
| Seed JSON export breaks the client build | `make build-client` fails fast if `export-seed` exits non-zero. CI catches it. |
| SQLite `BEGIN IMMEDIATE` contention under concurrent syncs | Phase A has one user; irrelevant. When subscription opens, a handful of concurrent writes is still well below SQLite's capacity in WAL mode. |
| Old legacy localStorage keys cause incorrect migration | First-run migration code is guarded on "new key absent AND old key present" so it's one-shot and idempotent by construction. |

## Deferred to Later Phases

- **Phase B**: server-side search endpoint (`GET /api/sites/search?q=...`) and client-side autocomplete when adding a site.
- **Phase C**: daemon "watch mode" that observes traffic for ~2 minutes after a user adds a new site and contributes discovered domains back into the catalog.
- **Admin moderation UI**: view/approve/reject user-submitted sites (needed when subscription opens up).
- **Per-user custom labels and ordering**.
- **Analytics**: display demand rankings in the admin panel (`SELECT site_id, COUNT(*) FROM user_sites GROUP BY site_id ORDER BY ... DESC`).
