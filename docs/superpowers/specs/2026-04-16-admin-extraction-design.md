# Admin Extraction: Separate Service Design

**Date:** 2026-04-16
**Status:** Approved

## Goal

Выделить admin panel из прокси-сервера в отдельный контейнер. Прокси-сервер становится единственным владельцем данных (Postgres), админка обращается к нему исключительно через REST API + SSE. Чистая граница: админка — это React SPA за nginx, прокси — API-backend.

## Architecture

```
                    admin.proxyness.smurov.com          proxyness.smurov.com
                    ┌───────────────────────┐           ┌──────────────────────────┐
                    │  nginx (Alpine)       │           │  proxy-server (Go)       │
  Browser ────────► │  - React SPA static   │──CORS───► │  - TLS mux (443)         │
  (admin)           │  - Let's Encrypt TLS  │  API      │  - Proxy relay (0x01/02)  │
                    │  - SPA fallback       │           │  - Admin REST API         │
                    └───────────────────────┘           │  - SSE /stats/stream      │
                          Aeza only                     │  - Config reverse-proxy   │
                                                        │  - Device API             │
                    ┌───────────────────────┐           │                            │
  Client apps ─────►│  proxy-server (Go)    │           └──────────┬───────────────┘
  (Timeweb)         │  (same binary)        │                      │
                    └───────────────────────┘                      │
                                                        ┌──────────▼───────────────┐
                                                        │  Postgres 16 (Aeza)      │
                                                        │  10.88.0.1:5432          │
                                                        └──────────────────────────┘
```

## Components

### 1. Proxy Server Changes

**Keep:**
- All 20 admin REST API endpoints under `/admin/api/*`
- Device API endpoints (`/api/report-version`, `/api/sync`, etc.)
- Config service reverse-proxy (`/api/client-config`, `/api/admin/notifications`)
- Basic Auth for admin endpoints
- TLS mux routing (0x01 → proxy, HTTP → admin API)

**Add:**
- **SSE endpoint** `GET /admin/api/stats/stream` — pushes JSON events every 1s:
  - `event: active` — list of active connections (replaces polling `/stats/active`)
  - `event: rate` — per-device rates with history (replaces polling `/stats/rate`)
  - Uses `text/event-stream` content type, `Cache-Control: no-cache`
  - One goroutine per SSE client, reads from stats.Tracker, writes to `http.Flusher`
  - Auto-closes on client disconnect (context cancellation)
- **CORS middleware** on `/admin/api/*` endpoints — `Access-Control-Allow-Origin: https://admin.proxyness.smurov.com`, allow `Authorization` header, handle preflight `OPTIONS`

**Remove:**
- `server/internal/admin/static.go` — go:embed of React dist
- `server/internal/admin/landing.go` — landing page template + GitHub release fetching
- Landing page reverse-proxy (`GET /` → `http://172.17.0.1:80`)
- Dockerfile stage 1 (node:22-alpine UI build)
- `server/admin-ui/` directory (moves to `admin/`)

### 2. Admin Container (new)

**Stack:** nginx (Alpine) + React 19 + Vite + TypeScript

**Structure:**
```
admin/
├── Dockerfile          # node build → nginx serve
├── nginx.conf          # SPA fallback, gzip, cache headers
├── package.json
├── vite.config.ts      # base: "/", proxy for dev
├── src/
│   ├── main.tsx
│   ├── App.tsx
│   ├── lib/
│   │   └── api.ts      # fetch wrapper with Basic Auth
│   ├── hooks/
│   │   └── useStatsStream.ts  # SSE hook for live stats
│   ├── pages/
│   │   ├── Dashboard.tsx
│   │   ├── Users.tsx
│   │   ├── UserDetail.tsx
│   │   ├── Sites.tsx
│   │   ├── SiteDetail.tsx
│   │   ├── Notifications.tsx
│   │   ├── Releases.tsx
│   │   ├── Changelog.tsx
│   │   └── Logs.tsx
│   └── components/
│       └── ...          # shared UI components
└── public/
```

**API client (`lib/api.ts`):**
- Base URL configured via env var `VITE_API_URL` (build-time) — defaults to `https://proxyness.smurov.com`
- Basic Auth credentials stored in memory (prompted on first load, no localStorage for passwords)
- All requests include `Authorization: Basic base64(user:pass)` header

**SSE hook (`hooks/useStatsStream.ts`):**
- `EventSource` with `withCredentials` for Basic Auth (or polyfill that supports auth headers — native EventSource doesn't support custom headers)
- Fallback: if EventSource can't carry Basic Auth, use `fetch` with `ReadableStream` reader to parse SSE manually
- Reconnects automatically on disconnect
- Returns reactive state: `{ activeConnections, deviceRates }`

**Deployment:**
- Dockerfile: `node:22-alpine` builds React → `nginx:alpine` serves `dist/`
- nginx config: `try_files $uri /index.html` for SPA routing, gzip on
- Container name: `proxyness-admin`
- Runs on Aeza only (single instance)
- Port: 80 internally, SSL terminated by host-level certbot/nginx or standalone Let's Encrypt
- Tag pattern: `admin-*` (e.g. `admin-1.0.0`), separate workflow `deploy-admin.yml`
- VERSION file: `admin/VERSION`

### 3. SSL for admin.proxyness.smurov.com

DNS: A record `admin.proxyness.smurov.com` → `95.181.162.242` (Aeza)

SSL options:
- **Standalone certbot** on Aeza — run before container start, mount certs into nginx container
- **Or**: host-level nginx reverse-proxy on Aeza (port 443 → container port 80), certbot on host

Since port 443 is already occupied by the proxy server on Aeza, admin needs a different approach:
- Run admin nginx on a different port (e.g. 8080) behind a host-level nginx that multiplexes by SNI/hostname on 443
- Or: admin container binds to a separate IP (if available)
- Or: proxy server itself reverse-proxies `/admin/*` static to admin container (but this defeats the purpose of separation)
- **Recommended**: Host-level nginx on Aeza that terminates TLS for `admin.proxyness.smurov.com` on port 443 and proxies to admin container on `127.0.0.1:8080`. Proxy server keeps its own TLS on port 443 for `proxyness.smurov.com` — this works because they're on the same IP, routed by SNI. Actually, SNI-based routing requires a single process on 443 — so either use a host-level multiplexer or give admin a different port.

**Simplest approach**: Admin listens on port 8443 with its own self-signed or Let's Encrypt cert. The admin URL becomes `https://admin.proxyness.smurov.com:8443`. Not ideal (non-standard port) but zero conflict with the proxy server.

**Better approach**: Use a lightweight SNI-based TCP proxy (like `sniproxy` or `haproxy`) on port 443 that routes `proxyness.smurov.com` → proxy server (:4430), `admin.proxyness.smurov.com` → admin nginx (:8080). Both get standard port 443 from the outside. One-time setup.

Decision deferred to implementation plan — depends on what's simplest to set up on Aeza.

## SSE Protocol

Endpoint: `GET /admin/api/stats/stream`
Auth: Basic Auth (same as REST endpoints)
Content-Type: `text/event-stream`

```
event: active
data: [{"id":1,"deviceId":5,"deviceName":"hunt macbook","userName":"ilya","tls":true,"startedAt":"...","bytesIn":12345,"bytesOut":67890}]

event: rate
data: [{"deviceId":5,"deviceName":"hunt macbook","userName":"ilya","download":1048576,"upload":524288,"history":[...]}]

event: overview
data: {"totalUsers":7,"totalDevices":8,"activeConnections":2,"totalTrafficIn":...,"totalTrafficOut":...}

```

Events pushed every 1 second. Each `data:` line is a JSON array/object.
Client-side: parse with `EventSource` or fetch+stream reader.

## CORS Policy

Applied to all `/admin/api/*` endpoints on proxy server:
- `Access-Control-Allow-Origin: https://admin.proxyness.smurov.com`
- `Access-Control-Allow-Headers: Authorization, Content-Type`
- `Access-Control-Allow-Methods: GET, POST, PATCH, DELETE, OPTIONS`
- `Access-Control-Allow-Credentials: true`
- `Access-Control-Max-Age: 3600`
- Preflight `OPTIONS` returns 204 with above headers

## Migration Path

1. Add SSE + CORS to proxy server, keep existing embedded admin working
2. Create `admin/` with React app pointing at proxy API
3. Deploy admin container on Aeza
4. Set up DNS + SSL for `admin.proxyness.smurov.com`
5. Verify admin works standalone
6. Remove embedded admin from proxy server (static.go, landing.go, Dockerfile node stage, admin-ui/)
7. Tag and deploy cleaned-up proxy server

## What Doesn't Change

- Client (Electron) — connects to proxy as before
- Daemon — no changes
- Config service — stays on Aeza, reverse-proxied through proxy server
- Timeweb proxy — same binary, same API, no admin UI
- DB schema — no changes
- Device API — no changes
