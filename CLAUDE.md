# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SmurovProxy — proxy system with a Go server (VPS in Netherlands), Go daemon (local SOCKS5 + TUN tunnel), privileged helper (TUN device management), and Electron desktop client. Single TLS port (443) multiplexes proxy traffic and HTTP admin panel.

**Landing page**: https://proxy.smurov.com

## Build & Test Commands

```bash
# Run all Go tests (pkg, daemon, server, test)
make test

# Build server (Linux amd64, output: dist/proxy-server)
make build-server

# Build daemon (macOS arm64/amd64 + Windows, output: dist/daemon-*)
make build-daemon

# Build helper (macOS arm64/amd64 + Windows, output: dist/helper-*)
cd helper && go build -o ../dist/helper-darwin-arm64 ./cmd/

# Build Electron client (bundles daemon+helper into resources, builds PKG/exe)
make build-client

# Run Electron in dev mode
make dev

# Run a single Go test
cd server && go test ./internal/db/ -run TestDeviceCRUD -v

# Clean build artifacts
make clean
```

## Architecture

**Go workspace** (`go.work`) with 6 modules: `server`, `daemon`, `helper`, `pkg`, `config`, `test`.

### Server (`server/`)
TLS listener on port 443. `ListenerMux` peeks first byte to route:
- `0x01` (MsgTypeTCP) → TCP proxy relay (HMAC auth → device lookup → bidirectional relay with traffic counting)
- `0x02` (MsgTypeUDP) → UDP proxy relay (framed UDP over TLS)
- HTTP → admin API (Basic Auth, REST endpoints for users/devices/traffic stats) + landing page

Admin UI (`server/admin-ui/`) is a React/Vite app compiled and embedded into the server binary via Go embed.

Key internals: `mux/` (connection routing), `db/` (SQLite — users, devices, traffic_stats), `admin/` (HTTP API + stats), `stats/` (atomic connection tracker), `proxy/` (TCP/UDP relay handlers).

### Daemon (`daemon/`)
Local SOCKS5 server (default `127.0.0.1:1080`) + TUN proxy engine. Exposes HTTP control API (default `127.0.0.1:9090`).

**SOCKS5 endpoints**: `/connect`, `/disconnect`, `/status`, `/health`.
**TUN endpoints**: `/tun/start`, `/tun/stop`, `/tun/status`, `/tun/rules`.

Key internals:
- `tunnel/` — SOCKS5 listener + TLS relay
- `socks5/` — SOCKS5 protocol handshake
- `tun/` — TUN proxy engine (gVisor netstack, packet bridge, per-app routing)
- `api/` — HTTP control endpoints

#### TUN Engine (`daemon/internal/tun/`)
- `engine.go` — Main TUN engine: connects to helper, bridges packets between helper IPC and gVisor netstack, handles TCP/UDP via forwarders
- `device.go` — gVisor netstack + channel endpoint setup
- `rules.go` — Split tunneling: `proxy_all_except` or `proxy_only` modes with per-app matching
- `dialer_darwin.go` / `dialer_windows.go` — Protected dialer using `IP_BOUND_IF` (macOS) / `IP_UNICAST_IF` (Windows) to bypass TUN routes
- `procinfo_darwin.go` / `procinfo_windows.go` — Process identification by local port (sysctl on macOS, iphlpapi on Windows)

**Packet flow**: Helper reads raw IP packets from TUN device → length-prefixed relay over IPC → daemon injects into gVisor via `channel.Endpoint.InjectInbound()` → TCP/UDP forwarders → proxy (TLS to server) or bypass (protected dial to destination). Outgoing packets: gVisor → `ReadContext()` → relay to helper → TUN device.

**QUIC blocking**: UDP port 443 is dropped to force browsers to fall back from QUIC to TCP/HTTPS.

**Self-detection**: Daemon identifies its own process via `os.Executable()` and always bypasses its own traffic to prevent routing loops.

### Helper (`helper/`)
Privileged process for TUN device management. Runs as launchd service on macOS, spawned by client on Windows.

- Creates/destroys TUN device (utun on macOS via wireguard/tun, wintun on Windows)
- Manages system routes (server route via default gateway, 0.0.0.0/1 + 128.0.0.0/1 via TUN)
- Packet relay: after TUN creation, IPC connection stays open for bidirectional length-prefixed packet relay between TUN device and daemon
- Auto-cleanup: destroys TUN if daemon disconnects

IPC: Unix socket `/var/run/smurov-helper.sock` (macOS), TCP `127.0.0.1:9091` (Windows).

### Shared packages (`pkg/`)
- `auth/` — HMAC-SHA256 authentication. 41-byte messages: version(1) + timestamp(8) + HMAC(32). 30s clock skew tolerance.
- `proto/` — Wire protocol helpers: auth exchange, message types (TCP=0x01, UDP=0x02), connect message (addr type + host + port), UDP framing, bidirectional relay with byte counting.

### Client (`client/`)
Electron 33 + React 19 + TypeScript + Vite. Custom frameless window. Spawns daemon and helper binaries. Auto-updates via custom updater.

**Hybrid TUN+SOCKS5 mode**: In TUN mode, also starts SOCKS5 tunnel + enables system proxy. Apps (Telegram, Discord, etc.) go through TUN; browsers use SOCKS5 (avoids QUIC issues).

Main process: `src/main/` (daemon lifecycle + log capture in `daemon.ts`, system proxy in `sysproxy.ts`, installed apps detection in `apps.ts`).
Renderer: `src/renderer/` (React UI with `App.tsx`, curated app list in `AppRules.tsx`, mode selector in `ModeSelector.tsx`, notification banner with dismiss in `NotificationBanner.tsx`).

### Config Service (`config/`)
Separate Go microservice (port 8443, `network_mode: host`). SQLite DB. Manages client configuration and push notifications.

- `db/` — Notification CRUD, `device_seen` (first-visit tracking), `notification_deliveries` (delivery tracking), `service_config` (key-value store for proxy_server, config_url, relay_url)
- `api/` — `GET /api/client-config` (public, device key auth via proxy server callback), admin CRUD for notifications + services
- `poller/` — Polls GitHub releases for latest version, auto-creates update notifications

**Notification lifecycle**: Admin creates notification (with `expires_at`, default 7 days) → config service stores in SQLite → client polls `/api/client-config?key=...&v=...` → service filters by: `active=1`, `created_at > device.first_seen_at`, not expired, beta_only gating → deduplicates per type (update/maintenance/migration: latest only; info: unlimited) → returns filtered list → async records delivery in `notification_deliveries`.

**Delivery tracking**: `device_seen` table records first poll timestamp per device key (INSERT OR IGNORE). `notification_deliveries` records which device received which notification. Admin endpoint `GET /api/admin/notifications/{id}/deliveries` returns delivery list. Admin UI joins with proxy server's user/device data on the frontend to group by user.

Proxy server reverse-proxies config endpoints: `/api/client-config`, `/api/admin/notifications`, `/api/admin/notifications/`, `/api/admin/services`. Config service validates device keys by calling back to proxy's `/api/validate-key`.

### Integration tests (`test/`)
Separate Go module with end-to-end tests covering auth and connection flow.

## Deployment

- **Server**: Docker multi-stage build (React UI → Go binary → Alpine). CI deploys to VPS via `.github/workflows/deploy.yml`. Container runs with `--ulimit nofile=32768:32768`.
- **Client**: Tag-triggered release builds macOS PKGs + Windows NSIS exe via `.github/workflows/release.yml`. CI injects version from git tag into `package.json` before building (`v1.31.0-beta.1` → `1.31.0-beta.1`), so `package.json` always stays at the base version. Beta tags (`*-beta.*`) create pre-releases; stable tags create latest releases.
- **Config service**: Docker image built manually on VPS (`docker build -f config/Dockerfile`). Volume: `smurov-config-data:/data`. Container: `smurov-config`.
- **SSL**: `scripts/setup-ssl.sh` manages Let's Encrypt certs for `proxy.smurov.com`.
- **VPS**: Aeza NL (4 CPU, 8 GB RAM, 1 Gbps, Netherlands).

## Protocol Flow

```
                    ┌─ Browsers ──→ System SOCKS5 proxy (:1080) ─┐
Client App ─────────┤                                             ├─→ TLS → Server (:443)
                    └─ Apps ──→ TUN device ──→ Helper relay ──→   │
                                  Daemon (gVisor netstack) ───────┘
                                                                    ↓
                                                              Peek msg type
                                                              0x01 → TCP relay
                                                              0x02 → UDP relay
                                                              HTTP → Admin panel
```

## Key Design Decisions

- **JSON response reading**: `connectAndCreate()` reads helper response byte-by-byte until `\n` instead of using `json.Decoder`, because the decoder's internal buffer captures the start of the binary relay stream (the trailing `\n` becomes `0x0A` = first byte of packet length → framing desync).
- **Protected dialer**: All outgoing connections from daemon use `IP_BOUND_IF` (macOS) / `IP_UNICAST_IF` (Windows) to bind to the physical interface, bypassing TUN routes.
- **No QUIC proxying**: UDP port 443 is silently dropped in TUN mode. Chrome falls back to TCP/HTTPS.
- **Device fingerprinting**: `pkg/machineid/` generates a stable 16-byte hardware fingerprint (IOPlatformUUID on macOS, MachineGuid on Windows). Sent via binary protocol (`MsgTypeMachineID = 0x03`) on every connection. First connection binds fingerprint to device; subsequent connections with a different fingerprint are rejected. The `/api/lock-device` and `/api/unlock-device` HTTP endpoints are no-ops — binding is managed exclusively by the binary protocol.
- **Machine ID rejection**: When the server rejects a machine fingerprint, the daemon stops the tunnel/engine with `lastError = "Device is bound to a different machine"`. The client detects this via polling, clears the stored key, and shows the setup screen (no auto-reconnect).
- **Health loop detectors (D1/D2/D3)**: Both `tunnel.go` and `tun/engine.go` run a `healthLoop()` with three independent detectors for disconnect recovery. **D1** = transport `DoneChan()` closed → run `reconnectTransport()` (up to 20 attempts × 3s). **D2** = periodic `verifyServer()` / `healthCheck()` failing, recovered after a successful check (recovery branch only fires when `failures > 0`). **D3** = stall detector (tunnel: no bytes flowing while activeHosts > 0; engine: consecutive `OpenStream` failures). **Critical**: D3 must NOT only flip status to `Reconnecting` — it must also rebuild the transport (mirror D1). An earlier bug had D3 only setting status, then D2's recovery branch never fired (because `failures` stayed at 0 while `verifyServer` kept succeeding), leaving the tunnel wedged in `Reconnecting` forever until manual disconnect+reconnect.
- **Daemon API CORS**: Public API endpoints (`/connect`, `/status`, `/stats`, `/tunnel/*`, etc.) are wrapped in a `withCORS` middleware in `daemon/internal/api/api.go`. This is required for `make dev` — the Vite renderer loads from `http://localhost:5174` and hits the daemon on `127.0.0.1:9090`, which is cross-origin and blocked without CORS headers. Packaged builds load from `file://` (origin `null`) and the wrapper is a no-op there.
- **Client auto-connect on mount**: `App.tsx` has a mount-only `useEffect` guarded by `autoConnectFired` ref that calls `startReconnect()` when a key is stored. Without this, after a cold client launch the TUN engine starts, but nothing triggers `/connect` → SOCKS5 listener never binds → browsers get "site unavailable". `useDaemon.connect` **throws-via-boolean** (returns `false` on non-ok) so `startReconnect`'s retry loop actually retries on transient failures like 409 from `lockDevice` against a stale server-side lock.
- **Loader → main window handoff order (Windows)**: In `bootMainApp` we MUST call `createWindow()` BEFORE `destroyLoaderWindow()`, not after. `BrowserWindow.destroy()` on the only live window fires `window-all-closed` synchronously in the same tick; on Windows (non-darwin) the handler calls `app.quit()`, and the subsequent `createWindow()` then runs inside an already-quitting process and its window is torn down immediately. macOS is unaffected because `window-all-closed` is a no-op there. This bug shipped silently from 1.27.0 to 1.28.4 — Windows users saw the loader, then nothing. Overlapping the transition (main window alive before loader dies) keeps the live window count above zero across the handoff.
- **Config polling URL must use domain, not IP**: `DEFAULT_CONFIG_URL` in `client/src/main/index.ts` MUST be `https://proxy.smurov.com/...`, not the raw IP. The TLS certificate is issued for `proxy.smurov.com` only — `net.fetch` (Chromium) rejects the connection if hostname doesn't match the certificate SAN. This bug silently broke all notification delivery from the feature's introduction until v1.30.2.
- **Client version injection**: `package.json` always contains the base version (e.g., `1.31.0`). The release workflow injects the full version from the git tag before building. Beta tags (`v1.31.0-beta.1`) get `1.31.0-beta.1` → BETA badge shows via `version.includes("beta")` in `App.tsx`. Never manually add beta suffix to `package.json`.
- **NotificationBanner dismiss**: Uses a single `dismissed_before` ISO timestamp in localStorage (key: `notification-dismissed-before`). Notifications with `created_at <= dismissed_before` are hidden. One value, never grows. New notifications created after the dismiss timestamp appear normally.
- **Main-process diagnostic logger (`--trace`)**: `client/src/main/index.ts` has an opt-in crash logger that writes per-phase boot traces, uncaught exceptions, renderer `render-process-gone`/`did-fail-load`/`preload-error`/`console-message` events and app quit-phase events to `~/Desktop/smurov-crash.log`. Enable with `--trace` on the command line (NOT `--debug` — Electron reserves that for its legacy Node inspector) or `SMUROV_DEBUG=1` env var. Windows gotcha: `requireAdministrator` elevation in `electron-builder.json` **drops environment variables on the elevated child**, so `SMUROV_DEBUG=1` never reaches a packaged Windows build — only `--trace` works there because argv survives UAC elevation.
- **Admin dashboard rate headline is smoothed, not instantaneous**: The tracker's `rateTicker` samples traffic in 1-second windows and writes one `RatePoint` per active device into a 300-slot ring buffer (`server/internal/stats/tracker.go`). If `Rates()` returned the tail point directly as the headline "↓ Download" / "↑ Upload" numbers, the dashboard would flicker to 0 between bursts — real browsing traffic has legitimate 0-byte 1s windows (HTTP keep-alive gaps, video chunk boundaries, every TCP-level pause). `Rates()` therefore averages the last `rateSmoothWindow` (=5) points via `smoothRate()`. The `history` array returned to the UI is still the raw 300 points — only the headline number is smoothed; the line graph renders the unsmoothed samples. Don't "simplify" this back to `last := history[len-1]`. **Known imperfection**: TCP connections that open AND close between two 1-second ticks are never seen by `computeRates` (they're gone from `t.conns` by the time the tick runs), so their bytes still land in the `traffic_stats` DB via `recordTraffic()` in `proxy/tcp.go` but are missing from the rate graph entirely — matters for undercount during browsing with many short HTTPS requests, cumulative traffic totals are unaffected.
- **Broken pre-existing tests in `server/internal/{udp,admin}`**: `session_test.go` and `admin_test.go` / `sync_*_test.go` don't compile in main — `NewSessionManager()` and `NewHandler()` signatures drifted and the tests were never updated. The deploy workflow only runs `pkg/... daemon/... test/...` (not `server/...`), so CI stays green while `cd server && go test ./...` fails locally. Fix the test arg lists if you're working in those packages; `stats/` tests were unbroken in commit 8c740ff as a prerequisite for the rate-smoothing fix.
