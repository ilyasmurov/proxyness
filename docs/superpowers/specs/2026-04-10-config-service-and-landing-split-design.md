# Config Service + Landing Split — Design Spec

**Date**: 2026-04-10
**Status**: Draft
**Scope**: New `smurov-config` microservice for notifications and service discovery, landing page extraction into standalone container, client changes to consume the new config API.

## Context

Currently the proxy server is a monolith: TLS/UDP proxy, admin panel, landing page, and version check logic all live in one binary. The client polls GitHub `latest.yml` directly for updates and has no server-driven notification channel.

Problems this solves:
- **No way to push notifications to clients** (version updates, server migration, maintenance)
- **No service discovery** — server addresses are hardcoded in client; if proxy IP is blocked by TSPU, clients are stuck with no way to learn a new address
- **Monolith coupling** — landing page and config/notifications don't need to live on the same host as the proxy; separating them enables future migration (config to always-available host, landing to CDN)

## Architecture

```
Today (same VPS):                        Future (split):

┌────────────────────────┐           ┌──────────┐  ┌──────────┐  ┌──────────┐
│ VPS 95.181.162.242     │           │ VPS-1    │  │ VPS-2    │  │ CDN      │
│                        │           │ proxy    │  │ config   │  │ landing  │
│ smurov-proxy    :443   │           │ :443     │  │ :443     │  │          │
│ smurov-config   :8443  │           │          │  │          │  │          │
│ smurov-landing  :80    │           └──────────┘  └──────────┘  └──────────┘
└────────────────────────┘
```

Three containers on the same VPS now, independently deployable later.

### Container 1: `smurov-config`

New Go service. Own module (`config/`) in the go.work workspace. Own Dockerfile. Own SQLite DB.

**Responsibilities:**
- Serve `/api/client-config` — the single endpoint clients poll for everything
- Store and serve notifications (CRUD via admin API)
- Store service config (proxy_server, relay_url, config_url)
- Check for new client versions (polls GitHub `latest.yml` periodically, creates notification automatically when new version detected)

**API:**

Public (device key auth):
```
GET /api/client-config?key=<device_key>&v=<client_version>
→ {
    "config_url": "https://...",     // self-referencing, for future migration
    "proxy_server": "95.181.162.242:443",
    "relay_url": null,               // null until relay is set up
    "notifications": [
      {
        "id": "uuid",
        "type": "update|migration|maintenance|info",
        "title": "...",
        "message": "...",
        "action": {                  // optional
          "label": "Button text",
          "type": "update|reconnect|open_url",
          "url": "...",              // for open_url
          "server": "..."            // for reconnect
        },
        "created_at": "ISO8601"
      }
    ]
  }
```

Admin (Basic Auth):
```
GET    /api/admin/notifications         — list all
POST   /api/admin/notifications         — create { type, title, message, action?, active }
DELETE /api/admin/notifications/:id      — delete
PATCH  /api/admin/notifications/:id      — update (toggle active, edit text)

GET    /api/admin/services              — list service config
PUT    /api/admin/services              — update { proxy_server, relay_url, config_url }
```

**Database (SQLite):**
```sql
CREATE TABLE notifications (
  id         TEXT PRIMARY KEY,
  type       TEXT NOT NULL,           -- update, migration, maintenance, info
  title      TEXT NOT NULL,
  message    TEXT,
  action     TEXT,                    -- JSON, nullable
  active     INTEGER DEFAULT 1,
  created_at TEXT DEFAULT (datetime('now'))
);

CREATE TABLE service_config (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
-- Seeded with: proxy_server, relay_url, config_url
```

**Auto version check:**
Background goroutine polls GitHub `releases/latest/download/latest.yml` every hour. When a new version is detected (comparing against last known), auto-creates a notification of type `update`. This replaces the client-side GitHub polling entirely.

**Admin UI:**
Minimal embedded web page (single HTML file with inline JS, like a stripped-down version of the existing admin-ui pattern). Two tabs:
- **Notifications**: table of all notifications, create/edit/delete/toggle active
- **Services**: form showing current proxy_server, relay_url, config_url — edit and save

**Docker:**
```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /app
COPY config/ config/
COPY pkg/ pkg/
COPY go.work go.work.sum ./
RUN cd config && go build -o /smurov-config ./cmd

FROM alpine:3.20
COPY --from=build /smurov-config /smurov-config
EXPOSE 8443
ENTRYPOINT ["/smurov-config"]
```

Run: `docker run -d --name smurov-config -p 8443:8443 -v smurov-config-data:/data smurov-config -addr :8443 -db /data/config.db`

### Container 2: `smurov-landing`

Static nginx container serving the landing page files currently embedded in `server/landing/`.

**Responsibilities:**
- Serve `proxy.smurov.com` landing page (HTML/CSS/JS)
- Nothing else — no API, no backend logic

**Implementation:**
- Extract `server/landing/` into `landing/` top-level directory
- Simple nginx Dockerfile:
  ```dockerfile
  FROM nginx:alpine
  COPY landing/ /usr/share/nginx/html/
  ```
- Run: `docker run -d --name smurov-landing -p 80:80 smurov-landing`

Later: can be moved to Cloudflare Pages, Vercel, or any static hosting. No code changes needed.

### Container 3: `smurov-proxy` (existing, modified)

**Changes:**
- Remove landing page serving from ListenerMux HTTP path
- Remove `/api/check-version` or any version-related endpoints (moved to config service)
- Keep admin panel + admin API (shares SQLite DB, stays in-process)
- Keep proxy relay (TCP/UDP binary protocol)
- Keep all existing functionality except landing + version check

ListenerMux HTTP path after change:
- Admin API endpoints → unchanged
- Admin UI → unchanged
- Landing page → gone (returns 301 redirect to landing container's URL, or 404)
- Health check → keep `/health`

### Client Changes

**Config polling (replaces GitHub polling):**
- On startup: read cached config from disk (`config.json` in app data dir)
- If no cache: use hardcoded default `config_url` (points to current VPS)
- Poll `config_url` every 30 minutes + on window show/focus (reuse existing throttle)
- On response: cache to disk, update in-memory state
- If `config_url` changed in response: update cache, next poll goes to new URL
- If `proxy_server` changed: show notification "Server moved" (already in notifications array)
- If poll fails: use cached values, retry next interval

**Notification display (replaces UpdateBanner):**
- New `NotificationBanner` component replaces `UpdateBanner`
- Renders the highest-priority active notification from `notifications[]`
- Priority: migration > update > maintenance > info
- Each notification type has its own visual style (color, icon)
- Action button dispatches based on `action.type`:
  - `update`: trigger download flow (same as current UpdateBanner download)
  - `reconnect`: disconnect, update server address, reconnect
  - `open_url`: open URL in default browser

**Removed:**
- `UpdateBanner.tsx` — deleted entirely
- `fetchYml()` in main/index.ts — no longer polls GitHub
- `checkForUpdatesAndNotify()` — replaced by config polling
- `check-update-version` IPC handler — removed
- `update-available` IPC event — replaced by config-based notifications

**New:**
- `configPoller` in main process — fetches `/api/client-config`, caches, broadcasts to renderer
- `config-updated` IPC event — pushes fresh config to renderer
- `NotificationBanner.tsx` — renders server-pushed notifications
- `configCache.ts` — read/write config JSON to app data directory

**Hardcoded defaults (safety net):**
```typescript
const DEFAULT_CONFIG_URL = "https://95.181.162.242:8443/api/client-config";
const DEFAULT_PROXY_SERVER = "95.181.162.242:443";
```
Used only when no cached config exists (fresh install).

## Device Key Auth for Config Endpoint

The `/api/client-config` endpoint requires `?key=<device_key>` — same hex key the client uses to connect to the proxy. Config service validates the key by calling the proxy server's existing `/api/check-key` endpoint (or by sharing the same key list).

Simpler approach for MVP: config service has its own `allowed_keys` list (seeded from the proxy DB, or a shared secret). Or even simpler: no auth at all for the config endpoint — the data it returns is not sensitive (service URLs + public notifications). Auth can be added later if needed.

**Decision: no auth for MVP.** The config endpoint returns public information. Notifications are broadcast to all clients. Service URLs are not secret. If we need per-device targeting later, add key auth then.

## Deployment

**CI/CD:**
- New workflow `.github/workflows/deploy-config.yml` triggered on push to main when `config/` files change
- New workflow `.github/workflows/deploy-landing.yml` triggered on push to main when `landing/` files change
- Existing `deploy.yml` continues to deploy proxy container
- All three deploy to the same VPS for now (different container names, different ports)

**docker-compose.yml** (optional, for local dev and single-VPS deployment):
```yaml
services:
  proxy:
    image: ghcr.io/ilyasmurov/smurov-proxy:latest
    ports: ["443:443", "443:443/udp"]
    volumes: ["proxy-data:/data"]
    
  config:
    image: ghcr.io/ilyasmurov/smurov-config:latest
    ports: ["8443:8443"]
    volumes: ["config-data:/data"]
    
  landing:
    image: ghcr.io/ilyasmurov/smurov-landing:latest
    ports: ["80:80"]
```

**TLS for config service:**
Config service needs HTTPS (client polls it). Options:
- Share the same Let's Encrypt cert as proxy (mount cert volume)
- Use a reverse proxy (nginx/caddy) in front of config that handles TLS
- For MVP: run on HTTP behind the proxy's existing TLS termination (proxy forwards `/api/client-config` to config container internally)

**Decision for MVP:** Proxy server acts as reverse proxy for config endpoint. Client hits `https://95.181.162.242:443/api/client-config` → proxy recognizes this HTTP path and forwards to `http://smurov-config:8443/api/client-config` internally. This means:
- No extra TLS cert needed for config
- Client doesn't need to know about port 8443
- When config moves to its own server later, client's cached `config_url` updates to the new HTTPS endpoint

## Migration Path

1. **Now:** All three containers on one VPS. Config endpoint proxied through port 443. Landing on port 80.
2. **Later (config moves):** Deploy config container on new VPS with own domain/cert. Update `config_url` in current config response → clients migrate automatically within 1 hour.
3. **Later (landing moves):** Point `proxy.smurov.com` DNS to Cloudflare Pages / Vercel / separate server. No client impact.
4. **Later (admin splits):** Migrate to PostgreSQL, extract admin into own container. Separate project.

## What This Does NOT Include

- Relay server / WebSocket transport (separate future spec)
- Client auto-failover to relay (depends on relay existing)
- UDP transport obfuscation (depends on bare-UDP transport)
- Admin panel extraction from proxy (depends on PostgreSQL migration)
- Per-device notification targeting (add when needed)
