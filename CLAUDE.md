# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Proxyness — proxy system with a Go server (VPS in Netherlands), Go daemon (local SOCKS5 + TUN tunnel), privileged helper (TUN device management), and Electron desktop client. Single TLS port (443) multiplexes proxy traffic and HTTP admin panel.

**Landing page**: https://proxyness.smurov.com

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

IPC: Unix socket `/var/run/proxyness-helper.sock` (macOS), TCP `127.0.0.1:9091` (Windows).

### Shared packages (`pkg/`)
- `auth/` — HMAC-SHA256 authentication. 41-byte messages: version(1) + timestamp(8) + HMAC(32). 30s clock skew tolerance.
- `proto/` — Wire protocol helpers: auth exchange, message types (TCP=0x01, UDP=0x02), connect message (addr type + host + port), UDP framing, bidirectional relay with byte counting.

### Client (`client/`)
Electron 33 + React 19 + TypeScript + Vite. Custom frameless window. Spawns daemon and helper binaries. Auto-updates via custom updater.

**Hybrid TUN+SOCKS5 mode**: In TUN mode, also starts SOCKS5 tunnel + enables system proxy. Apps (Telegram, Discord, etc.) go through TUN; browsers use SOCKS5 (avoids QUIC issues).

**Server picker**: Settings → General → Proxy Server. User chooses between Aeza NL (default) and Timeweb NL. Driven by a `SERVERS` const + `serverAddrFor(id)` helper in `App.tsx`; selection persists in `localStorage["proxyness-server"]`. Switching triggers a reconnect through the newly-picked server. Same device key works on both hosts (seeded from Aeza → Timeweb DB copy).

Main process: `src/main/` (daemon lifecycle + log capture in `daemon.ts`, system proxy in `sysproxy.ts`, installed apps detection in `apps.ts`).
Renderer: `src/renderer/` (React UI with `App.tsx`, curated app list in `AppRules.tsx`, mode selector in `ModeSelector.tsx`, notification banner with dismiss in `NotificationBanner.tsx`).

### Browser Extension (`extension/`)
Chrome MV3 extension. Popup (348px) + floating in-page panel (shadow DOM content script) + service worker. Pairs with desktop daemon via token stored in `chrome.storage.local`.

**Visual design**: Shares the desktop client's OKLCH dark palette (`oklch(0.12 0.014 250)` base, amber `oklch(0.78 0.155 75)` accents, green/red status). Typography: Barlow Semi Condensed (headings), Figtree (body), loaded via Google Fonts in popup.html and `@import` in content-script shadow DOM. Approved mockup: `assets/design-extension-final.html`.

**Popup** (`popup/`): Header with ghost icon + "Proxyness" + status dot. Body shows current site domain, status tag (proxied/not-proxied/disabled/discovering), action button. Footer has D-style icon+label buttons (Add site, Hide panel, Unpair). Entrance animations via CSS `@keyframes pn-fade-in` / `pn-slide-up`.

**Floating panel** (`content-script.js`): Shadow DOM widget injected into every page. Uses a 3px colored accent stripe on the left edge (green=proxied, grey=not proxied, amber=scanning, red=blocked/down) + 14px ghost icon for branding. Draggable, position persisted via `chrome.storage.local`. Entrance animation `fp-enter` (fade + slide up).

**Service worker** (`service-worker.js`): Per-tab state tracking, domain discovery (queues subresource hosts, flushes to daemon's `/sites/discover`), block detection (tests failed main-frame loads through tunnel), daemon status polling (5s interval + 30s alarm). Swaps toolbar icon between closed/open eye based on daemon connection status.

**Daemon client** (`lib/daemon-client.js`): Token-aware fetch wrapper for `http://127.0.0.1:9090`. Methods: `match`, `add`, `discover`, `test`, `setEnabled`, `ping`. Auto-clears token on 401.

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

All deploys are tag-triggered. Push to main does NOT deploy anything. Tag conventions:

| Component | Tag pattern | Example | Workflow | Version file |
|-----------|-------------|---------|----------|-------------|
| Server + admin | `server-*` | `server-1.0.0` | `deploy.yml` | `server/VERSION` |
| Landing page | `landing-*` | `landing-1.0.0` | `deploy-landing.yml` | `landing/VERSION` |
| Config service | `config-*` | `config-1.0.0` | `deploy-config.yml` | `config/VERSION` |
| Client apps | `v*` | `v1.36.0` | `release.yml` | `client/package.json` |
| Client (pre-release) | `v*-beta.*` | `v1.36.0-beta.1` | `release.yml` | `client/package.json` |

All workflows also support `workflow_dispatch` for manual trigger from GitHub UI.

- **Server**: Docker multi-stage build (React UI → Go binary → Alpine). Container runs with `--ulimit nofile=32768:32768`. A single `server-X.Y.Z` tag deploys to **both VPSs in parallel** via two independent jobs (`deploy-aeza`, `deploy-timeweb`) — one failing doesn't block the other. Both hosts run the container under the name `proxyness`, pull `ghcr.io/${{ github.repository }}:latest`, and expose 443 TCP + UDP. Container and CLI args match across hosts so the deploy script is near-identical; see below for the per-host quirks that differ.
- **Client**: CI injects version from git tag into `package.json` before building (`v1.31.0-beta.1` → `1.31.0-beta.1`), so `package.json` always stays at the base version. Beta tags create pre-releases; stable tags create latest releases.
- **Config service**: Volume: `proxyness-config-data:/data`. Container: `proxyness-config`. **Runs on Aeza only** — Timeweb's `deploy-timeweb` job intentionally omits the `-config http://172.17.0.1:8443` flag, so notifications/auto-update pings only flow from Aeza. Clients on Timeweb still receive them via the `/api/client-config` polling URL that points at `proxyness.smurov.com` (Aeza) regardless of which VPS they're tunneling through.
- **SSL**: `scripts/setup-ssl.sh` manages Let's Encrypt certs for `proxyness.smurov.com` (Aeza). Timeweb holds its own Let's Encrypt cert for `proxy.smurov.com`. Neither cert is verified client-side (`InsecureSkipVerify: true` in TCP fallback; UDP transport does its own X25519+HMAC crypto and never sees TLS), so a domain/IP mismatch doesn't break connectivity.
- **VPSs**:
  - **Aeza NL** (Amsterdam) — 95.181.162.242. 4 CPU, 8 GB RAM, 1 Gbps. **Bad peering to many EU hosts**: direct `curl` from this VPS to leaseweb DE throttles at 0.55 MB/s (native), which propagates to VPN goodput. CF is peered fine. Volume: `proxyness-data`.
  - **Timeweb NL** (Amsterdam) — 82.97.246.65. 4 CPU AMD EPYC, 8 GB RAM. Much better pan-EU peering: 39-47 MB/s direct to leaseweb/selectel/CF. Also sees ~63 ms RTT from a typical RF client vs ~120 ms to Aeza, purely network routing. Container volume is `smurov-proxy-data` (legacy name from the pre-multi-VPS standalone deploy) — the `deploy-timeweb` job explicitly mounts this volume so the device DB persists across re-deploys. Do not switch it to `proxyness-data`.

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
- **Health loop detectors (D1/D2/D3/D4)**: Both `tunnel.go` and `tun/engine.go` run a `healthLoop()` with independent detectors for disconnect recovery. **D1** = transport `DoneChan()` closed → run `reconnectTransport()` (up to 20 attempts × 3s). **D2** = periodic `verifyServer()` / `healthCheck()` failing, recovered after a successful check (recovery branch only fires when `failures > 0`). **D3** = stall detector (tunnel: no bytes flowing while activeHosts > 0; engine: consecutive `OpenStream` failures). **D4** (engine only) = TUN bridge death — `bridgeInbound` / `bridgeOutbound` signal `bridgeDone` channel on exit → healthLoop stops the engine with `lastError = "TUN bridge lost"`. Without D4 the engine stayed in "active" status after helper crash, silently black-holing all TUN traffic until manual reconnect. **Critical**: D3 must NOT only flip status to `Reconnecting` — it must also rebuild the transport (mirror D1). An earlier bug had D3 only setting status, then D2's recovery branch never fired (because `failures` stayed at 0 while `verifyServer` kept succeeding), leaving the tunnel wedged in `Reconnecting` forever until manual disconnect+reconnect.
- **Slow-poll wait on ENETUNREACH (D1/D3)**: When `reconnectTransport()` exhausts its fast retry budget with `transport.IsNetworkUnreachable(err)` (ENETUNREACH anywhere in the error chain, plus a string-match fallback for `%v`-wrapped chains), the D1/D3 branches in BOTH `tun/engine.go` and `tunnel/tunnel.go` call `waitForNetwork()` instead of `stopLocked()`. `waitForNetwork` is a 15-second slow-poll loop with **no timeout** — it keeps trying one `tryReconnectOnce()` until the OS brings routes back, a non-network error surfaces (then the engine stops with the usual "Connection lost" message), or stop is requested. This exists so an overnight laptop sleep doesn't leave the proxy dead until manual reconnect — the old hard ~60s budget was fine for WiFi blips but not for real sleep. D2 is intentionally NOT patched: its error isn't typically ENETUNREACH (it's "transport dead" from `Alive()`) and D3 always trips first in the sleep scenario anyway. The ENETUNREACH detection lives in `daemon/internal/transport/errors.go` as a shared helper.
- **`reconnectTransport()` returns `error`, not `bool`**: Both engine.go and tunnel.go use `errReconnectStopped` as a package-private sentinel to tell the caller "stopHealth fired, exit the health loop without calling stopLocked" (stopLocked is already being called by whoever signalled stop). The caller pattern is: `err == nil` → success, `errors.Is(err, errReconnectStopped)` → just return, `transport.IsNetworkUnreachable(err)` → enter `waitForNetwork()`, else → stopLocked. Don't replace this back with a `bool` — you lose the ability to distinguish "network down, wait" from "auth rejected, stop" from "user clicked disconnect, exit quietly".
- **Daemon API CORS**: Public API endpoints (`/connect`, `/status`, `/stats`, `/tunnel/*`, etc.) are wrapped in a `withCORS` middleware in `daemon/internal/api/api.go`. This is required for `make dev` — the Vite renderer loads from `http://localhost:5174` and hits the daemon on `127.0.0.1:9090`, which is cross-origin and blocked without CORS headers. Packaged builds load from `file://` (origin `null`) and the wrapper is a no-op there.
- **Client auto-connect on mount**: `App.tsx` has a mount-only `useEffect` guarded by `autoConnectFired` ref that calls `startReconnect()` when a key is stored. Without this, after a cold client launch the TUN engine starts, but nothing triggers `/connect` → SOCKS5 listener never binds → browsers get "site unavailable". `useDaemon.connect` **throws-via-boolean** (returns `false` on non-ok) so `startReconnect`'s retry loop actually retries on transient failures like 409 from `lockDevice` against a stale server-side lock.
- **Loader → main window handoff order (Windows)**: In `bootMainApp` we MUST call `createWindow()` BEFORE `destroyLoaderWindow()`, not after. `BrowserWindow.destroy()` on the only live window fires `window-all-closed` synchronously in the same tick; on Windows (non-darwin) the handler calls `app.quit()`, and the subsequent `createWindow()` then runs inside an already-quitting process and its window is torn down immediately. macOS is unaffected because `window-all-closed` is a no-op there. This bug shipped silently from 1.27.0 to 1.28.4 — Windows users saw the loader, then nothing. Overlapping the transition (main window alive before loader dies) keeps the live window count above zero across the handoff.
- **Config polling URL must use domain, not IP**: `DEFAULT_CONFIG_URL` in `client/src/main/index.ts` MUST be `https://proxyness.smurov.com/...`, not the raw IP. The TLS certificate is issued for `proxyness.smurov.com` only — `net.fetch` (Chromium) rejects the connection if hostname doesn't match the certificate SAN. This bug silently broke all notification delivery from the feature's introduction until v1.30.2.
- **Client version injection**: `package.json` always contains the base version (e.g., `1.31.0`). The release workflow injects the full version from the git tag before building. Beta tags (`v1.31.0-beta.1`) get `1.31.0-beta.1` → BETA badge shows via `version.includes("beta")` in `App.tsx`. Never manually add beta suffix to `package.json`.
- **NotificationBanner dismiss**: Uses a single `dismissed_before` ISO timestamp in localStorage (key: `notification-dismissed-before`). Notifications with `created_at <= dismissed_before` are hidden. One value, never grows. New notifications created after the dismiss timestamp appear normally.
- **Main-process diagnostic logger (`--trace`)**: `client/src/main/index.ts` has an opt-in crash logger that writes per-phase boot traces, uncaught exceptions, renderer `render-process-gone`/`did-fail-load`/`preload-error`/`console-message` events and app quit-phase events to `~/Desktop/proxyness-crash.log`. Enable with `--trace` on the command line (NOT `--debug` — Electron reserves that for its legacy Node inspector) or `PROXYNESS_DEBUG=1` env var. Windows gotcha: `requireAdministrator` elevation in `electron-builder.json` **drops environment variables on the elevated child**, so `PROXYNESS_DEBUG=1` never reaches a packaged Windows build — only `--trace` works there because argv survives UAC elevation.
- **Admin dashboard rate headline is smoothed, not instantaneous**: The tracker's `rateTicker` samples traffic in 1-second windows and writes one `RatePoint` per active device into a 300-slot ring buffer (`server/internal/stats/tracker.go`). If `Rates()` returned the tail point directly as the headline "↓ Download" / "↑ Upload" numbers, the dashboard would flicker to 0 between bursts — real browsing traffic has legitimate 0-byte 1s windows (HTTP keep-alive gaps, video chunk boundaries, every TCP-level pause). `Rates()` therefore averages the last `rateSmoothWindow` (=5) points via `smoothRate()`. The `history` array returned to the UI is still the raw 300 points — only the headline number is smoothed; the line graph renders the unsmoothed samples. Don't "simplify" this back to `last := history[len-1]`. **Known imperfection**: TCP connections that open AND close between two 1-second ticks are never seen by `computeRates` (they're gone from `t.conns` by the time the tick runs), so their bytes still land in the `traffic_stats` DB via `recordTraffic()` in `proxy/tcp.go` but are missing from the rate graph entirely — matters for undercount during browsing with many short HTTPS requests, cumulative traffic totals are unaffected.
- **Broken pre-existing tests in `server/internal/{udp,admin}`**: `session_test.go` and `admin_test.go` / `sync_*_test.go` don't compile in main — `NewSessionManager()` and `NewHandler()` signatures drifted and the tests were never updated. The deploy workflow only runs `pkg/... daemon/... test/...` (not `server/...`), so CI stays green while `cd server && go test ./...` fails locally. Fix the test arg lists if you're working in those packages; `stats/` tests were unbroken in commit 8c740ff as a prerequisite for the rate-smoothing fix.
- **Browsers-only ProxyMode removed, residue kept**: The `"socks5"` ProxyMode (a SOCKS5-only path that bypassed the TUN engine entirely, surfaced as the "Browsers" tab before 1.33.0) was removed from the UI in v1.33.0-beta.1. The `useState<ProxyMode>` state and all `proxyMode === "socks5"` / `proxyMode === "tun"` branches in `client/src/renderer/App.tsx` are **intentionally left in place** — they're dead but harmless, and ripping them out is a separate refactor. Legacy users with `localStorage["proxyness-mode"] === "socks5"` are silently migrated to `"tun"` at module load (before useState runs), so nobody gets stranded in an unreachable mode. Don't let the dead branches confuse you into thinking the two proxy modes still coexist — there's only TUN now, and the SOCKS5 tunnel you see alive in TUN mode is the *embedded* tunnel for PAC/browser fallback, not the standalone mode.
- **Live/idle colour rule for state switches**: Any UI element that switches a system-level **mode or setting** (traffic mode All/Selected, Transport Protocol auto/UDP/TLS, Main ↔ Settings page tabs in the mode bar, Browser sites All/Selected inside `SitesGrid`) lights up its active segment in **amber** (`c.am` fg + `c.amb` bg) only when `isConnected === true`. When the proxy is idle, the active segment uses a muted mid-grey `c.t2` foreground instead. Rationale: when the proxy is off nothing is actually routing traffic, so a brightly lit "active state" visually lies about system status. Amber is reserved for "this is a live setting affecting running traffic *right now*". Per-app brand-coloured tiles in `AppRules` (`AppToggle`, `SiteTile`, `AllBrowsersTile`) keep their brand colours — they communicate per-item state, not mode, and the brand identity is the signal. When adding a new mode/selector switch, follow the `isConnected ? c.am : c.t2` pattern; pass `isConnected` as a prop if crossing a component boundary (e.g., `AppRules` → `SitesGrid`).
- **Traffic-mode switch lives in its own sub-row, not the tab bar**: The "All traffic | Selected" segmented switch in `App.tsx` lives in a dedicated sub-row directly below the status row (indented past the status dot to align with the "Disconnected / Ready to connect" text), NOT in the page-tab row with Main / Settings. Active-state highlight reads `trafficMode === m` directly — **not** gated on `activeTab === "main"` — so the user can see which mode is active from the Settings page too. The status row uses natural height (no fixed `height: 100`) because the sub-row needs to grow the hero zone dynamically. Don't collapse the two back into one flex row: an earlier attempt put the switch inside the left status column and it pushed the dot / title / metrics out of vertical centre.
- **Extension floating panel uses accent stripe, not status dot**: The in-page floating panel identifies proxy state via a 3px colored left-edge stripe (`.fp-accent`) instead of the earlier colored dot. Green = proxied, grey = not proxied / disabled, amber = scanning (discovery mode), red = blocked / daemon down. The ghost silhouette icon (14px, `opacity: 0.4`) sits at the start of the row for brand recognition — without it the panel is an anonymous dark rectangle that could belong to any extension. The popup header also carries the ghost icon (22px, open/closed eye variant matching connection state). Don't remove the ghost icons — they're the only thing tying the panel/popup to the Proxyness brand.
- **Extension fonts load differently in popup vs content-script**: Popup loads Google Fonts via `<link>` tags in `popup.html` (Barlow Semi Condensed + Figtree + Barlow). Content-script loads Figtree via `@import` inside the shadow DOM `<style>` block — `<link>` tags don't work inside shadow DOM. The content-script only needs Figtree (body font); it doesn't use Barlow Semi Condensed because the floating panel has no headings. Don't add `<link>` to shadow DOM or move the popup fonts to `@import` (slower, blocks render).
- **Settings sidebar is a tablist, not a list of divs**: Items in the Settings page sidebar (`General / Extension / Account / Diagnostics`) are real `<button type="button">` elements with a `navRefs` record + `handleNavKey` that responds to `ArrowUp` / `ArrowDown` by cycling `section` + focus with wrap-around (uses `requestAnimationFrame` to focus after the setState commits). Standard WAI-ARIA tablist pattern. If you add a new section: add it to `NAV_SECTIONS`, add its key to `navRefs` init, render it via `navItem`, and the keyboard nav works for free. Don't revert to `<div onClick>` — Tab skips divs, screen readers don't announce them as controls.
- **Server picker is a dynamic const, not a hook**: `App.tsx` declares `const SERVERS = [{ id, label, addr }, ...]` at module scope and a `serverAddrFor(id)` helper. Inside the component, `SERVER` is a plain `const SERVER = serverAddrFor(serverId)` — recomputed every render from the `serverId` useState, not memoized. Any `useCallback` that closes over `SERVER` must list it in its dep array (startReconnect, handleTransportChange, the tray-connect useEffect) so the captured closure sees the user's latest pick. Regular function bodies in the component (handleModeChange, connectWithKey, JSX event handlers) just work because they close over current-render values. Don't hoist SERVER to `useMemo` — it's a 1-line lookup, the memo would add more noise than it saves, and the dep-array pattern above already handles staleness.
- **Aeza ↔ Timeweb DB divergence is accepted, not synced**: Both VPSs run the same proxy container with the same schema (`devices`, `users`, `traffic_stats`), but there is no live replication between their SQLite files. Devices/keys created on one after the initial Aeza→Timeweb seed (done manually via `sqlite3 .backup` + `scp`) don't appear on the other until someone repeats the seed. For the test phase that's fine — the user has the same device key registered on both from the seed. Long-term, per-user binding + traffic accounting would need either periodic export/import or a shared Postgres; don't paper over this with dual-writes in the server binary, they'd have to handle split-brain on network partition. When a user's key works on one VPS but not the other, "DB drift" is the first suspect, not auth/machine-id.
- **Server picker triggers reconnect via explicit disconnect+connect, not a daemon restart**: `handleServerChange` in `App.tsx` saves the new server id to `localStorage["proxyness-server"]`, updates React state, then — if the user is currently connected — calls `tunDisconnect()` → 300 ms sleep → `tunConnect(nextAddr, key)` (or the socks5 equivalent). The 300 ms delay exists so the daemon's SOCKS5 listener fully unbinds before the new `connect` call tries to re-bind; without it the listener occasionally held the port for another ~100 ms and the new `/connect` returned 409 against itself. If the user isn't connected, the picker just persists the choice silently — next manual connect picks it up.
