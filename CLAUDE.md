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

**Go workspace** (`go.work`) with 5 modules: `server`, `daemon`, `helper`, `pkg`, `test`.

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
Renderer: `src/renderer/` (React UI with `App.tsx`, curated app list in `AppRules.tsx`, mode selector in `ModeSelector.tsx`).

### Integration tests (`test/`)
Separate Go module with end-to-end tests covering auth and connection flow.

## Deployment

- **Server**: Docker multi-stage build (React UI → Go binary → Alpine). CI deploys to VPS via `.github/workflows/deploy.yml`. Container runs with `--ulimit nofile=32768:32768`.
- **Client**: Tag-triggered release builds macOS PKGs + Windows NSIS exe via `.github/workflows/release.yml`.
- **SSL**: `scripts/setup-ssl.sh` manages Let's Encrypt certs for `proxy.smurov.com`.
- **VPS**: Timeweb Cloud NL-80 (4 CPU, 8 GB RAM, 1 Gbps, Netherlands).

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
