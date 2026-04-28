# Architecture

**Go workspace** (`go.work`) with 6 modules: `server`, `daemon`, `helper`, `pkg`, `config`, `test`.

## Server (`server/`)
TLS listener on port 443. `ListenerMux` peeks first byte to route:
- `0x01` (MsgTypeTCP) → TCP proxy relay (HMAC auth → device lookup → bidirectional relay with traffic counting)
- `0x02` (MsgTypeUDP) → UDP proxy relay (framed UDP over TLS)
- HTTP → admin API (Basic Auth, REST endpoints for users/devices/traffic stats) + landing page

Admin API endpoints remain in the server binary (REST + SSE), but the UI is a separate service (see Admin Dashboard below). SSE endpoint `GET /admin/api/stats/stream` pushes active connections + device rates every second. CORS allows `https://admin.proxyness.smurov.com`.

Key internals: `mux/` (connection routing), `db/` (Postgres — users, devices, traffic_stats; 60s-TTL `deviceCache` in front of `GetDeviceByKey` with admin-side invalidation), `admin/` (HTTP API + SSE stats), `stats/` (atomic connection tracker), `proxy/` (TCP/UDP relay handlers).

## Daemon (`daemon/`)
Local SOCKS5 server (default `127.0.0.1:1080`) + TUN proxy engine. Exposes HTTP control API (default `127.0.0.1:9090`).

**SOCKS5 endpoints**: `/connect`, `/disconnect`, `/status`, `/health`.
**TUN endpoints**: `/tun/start`, `/tun/stop`, `/tun/status`, `/tun/rules`.

Key internals:
- `tunnel/` — SOCKS5 listener + TLS relay
- `socks5/` — SOCKS5 protocol handshake
- `tun/` — TUN proxy engine (gVisor netstack, packet bridge, per-app routing)
- `api/` — HTTP control endpoints

### TUN Engine (`daemon/internal/tun/`)
- `engine.go` — Main TUN engine: connects to helper, bridges packets between helper IPC and gVisor netstack, handles TCP/UDP via forwarders
- `device.go` — gVisor netstack + channel endpoint setup
- `rules.go` — Split tunneling: `proxy_all_except` or `proxy_only` modes with per-app matching
- `dialer_darwin.go` / `dialer_windows.go` — Protected dialer using `IP_BOUND_IF` (macOS) / `IP_UNICAST_IF` (Windows) to bypass TUN routes
- `procinfo_darwin.go` / `procinfo_windows.go` — Process identification by local port (sysctl on macOS, iphlpapi on Windows)

**Packet flow**: Helper reads raw IP packets from TUN device → length-prefixed relay over IPC → daemon injects into gVisor via `channel.Endpoint.InjectInbound()` → TCP/UDP forwarders → proxy (TLS to server) or bypass (protected dial to destination). Outgoing packets: gVisor → `ReadContext()` → relay to helper → TUN device.

**QUIC blocking**: UDP port 443 is dropped to force browsers to fall back from QUIC to TCP/HTTPS.

**Self-detection**: Daemon identifies its own process via `os.Executable()` and always bypasses its own traffic to prevent routing loops.

## Helper (`helper/`)
Privileged process for TUN device management. Runs as launchd service on macOS, spawned by client on Windows.

- Creates/destroys TUN device (utun on macOS via wireguard/tun, wintun on Windows)
- Manages system routes (server route via default gateway, 0.0.0.0/1 + 128.0.0.0/1 via TUN)
- Packet relay: after TUN creation, IPC connection stays open for bidirectional length-prefixed packet relay between TUN device and daemon
- Auto-cleanup: destroys TUN if daemon disconnects

IPC: Unix socket `/var/run/proxyness-helper.sock` (macOS), TCP `127.0.0.1:9091` (Windows).

## Shared packages (`pkg/`)
- `auth/` — HMAC-SHA256 authentication. 41-byte messages: version(1) + timestamp(8) + HMAC(32). 30s clock skew tolerance.
- `proto/` — Wire protocol helpers: auth exchange, message types (TCP=0x01, UDP=0x02), connect message (addr type + host + port), UDP framing, bidirectional relay with byte counting.

## Client (`client/`)
Electron 33 + React 19 + TypeScript + Vite. Custom frameless window. Spawns daemon and helper binaries. Auto-updates via custom updater.

**Hybrid TUN+SOCKS5 mode**: In TUN mode, also starts SOCKS5 tunnel + enables system proxy. Apps (Telegram, Discord, etc.) go through TUN; browsers use SOCKS5 (avoids QUIC issues).

**Server picker**: A `SERVERS` const + `serverAddrFor(id)` helper in `App.tsx` survives from the dual-VPS era. Currently `SERVERS.length === 1` (Aeza only) and the Settings → General "Proxy Server" segmented switch hides itself in that case; the row reappears automatically the day a second entry is added back. `localStorage["proxyness-server"]` still persists the choice; legacy `"timeweb"` values are silently migrated to `"aeza"` at module load.

Main process: `src/main/` (daemon lifecycle + log capture in `daemon.ts`, system proxy in `sysproxy.ts`, installed apps detection in `apps.ts`).
Renderer: `src/renderer/` (React UI with `App.tsx`, curated app list in `AppRules.tsx`, mode selector in `ModeSelector.tsx`, notification banner with dismiss in `NotificationBanner.tsx`).

## Admin Dashboard (`admin/`)
Standalone React 19 + Vite + TypeScript SPA served by nginx. Deployed as its own container on Aeza at `admin.proxyness.smurov.com`. Communicates with proxy server exclusively via REST API + SSE (no direct DB access).

- `src/lib/api.ts` — fetch wrapper, injects Basic Auth, base URL from `VITE_API_URL`
- `src/hooks/useStatsStream.ts` — SSE client for live stats (fetch+ReadableStream, not EventSource, because native EventSource doesn't support custom headers)
- `src/lib/auth.ts` — credentials in sessionStorage, login form on missing credentials
- Pages: Dashboard (SSE-driven), Users, UserDetail, Sites, SiteDetail, Notifications, Releases, Changelog, Logs

## Browser Extension (`extension/`)
Chrome MV3 extension. Popup (348px) + floating in-page panel (shadow DOM content script) + service worker. Pairs with desktop daemon via token stored in `chrome.storage.local`.

**Visual design**: Shares the desktop client's OKLCH dark palette (`oklch(0.12 0.014 250)` base, amber `oklch(0.78 0.155 75)` accents, green/red status). Typography: Barlow Semi Condensed (headings), Figtree (body), loaded via Google Fonts in popup.html and `@import` in content-script shadow DOM. Approved mockup: `assets/design-extension-final.html`.

**Popup** (`popup/`): Header with ghost icon + "Proxyness" + status dot. Body shows current site domain, status tag (proxied/not-proxied/disabled/discovering), action button. Footer has D-style icon+label buttons (Add site, Hide panel, Unpair). Entrance animations via CSS `@keyframes pn-fade-in` / `pn-slide-up`.

**Floating panel** (`content-script.js`): Shadow DOM widget injected into every page. Uses a 3px colored accent stripe on the left edge (green=proxied, grey=not proxied, amber=scanning, red=blocked/down) + 14px ghost icon for branding. Draggable, position persisted via `chrome.storage.local`. Entrance animation `fp-enter` (fade + slide up).

**Service worker** (`service-worker.js`): Per-tab state tracking, domain discovery (queues subresource hosts, flushes to daemon's `/sites/discover`), block detection (tests failed main-frame loads through tunnel), daemon status polling (5s interval + 30s alarm). Swaps toolbar icon between closed/open eye based on daemon connection status.

**Daemon client** (`lib/daemon-client.js`): Token-aware fetch wrapper for `http://127.0.0.1:9090`. Methods: `match`, `add`, `discover`, `test`, `setEnabled`, `ping`. Auto-clears token on 401.

## Config Service (`config/`)
Separate Go microservice (port 8443, `network_mode: host`). SQLite DB. Manages client configuration and push notifications.

- `db/` — Notification CRUD, `device_seen` (first-visit tracking), `notification_deliveries` (delivery tracking), `service_config` (key-value store for proxy_server, config_url, relay_url)
- `api/` — `GET /api/client-config` (public, device key auth via proxy server callback), admin CRUD for notifications + services
- `poller/` — Polls GitHub releases for latest version, auto-creates update notifications

**Notification lifecycle**: Admin creates notification (with `expires_at`, default 7 days) → config service stores in SQLite → client polls `/api/client-config?key=...&v=...` → service filters by: `active=1`, `created_at > device.first_seen_at`, not expired, beta_only gating → deduplicates per type (update/maintenance/migration: latest only; info: unlimited) → returns filtered list → async records delivery in `notification_deliveries`.

**Delivery tracking**: `device_seen` table records first poll timestamp per device key (INSERT OR IGNORE). `notification_deliveries` records which device received which notification. Admin endpoint `GET /api/admin/notifications/{id}/deliveries` returns delivery list. Admin UI joins with proxy server's user/device data on the frontend to group by user.

Proxy server reverse-proxies config endpoints: `/api/client-config`, `/api/admin/notifications`, `/api/admin/notifications/`, `/api/admin/services`. Config service validates device keys by calling back to proxy's `/api/validate-key`.

## Integration tests (`test/`)
Separate Go module with end-to-end tests covering auth and connection flow.
