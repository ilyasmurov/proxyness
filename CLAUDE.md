# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

SmurovProxy — proxy system with a Go server (VPS), Go daemon (local SOCKS5 tunnel), and Electron desktop client. Single TLS port (443) multiplexes proxy traffic and HTTP admin panel.

## Build & Test Commands

```bash
# Run all Go tests (pkg, daemon, server, test)
make test

# Build server (Linux amd64, output: dist/proxy-server)
make build-server

# Build daemon (macOS arm64/amd64 + Windows, output: dist/daemon-*)
make build-daemon

# Build Electron client (bundles daemon into resources, builds DMG/exe)
make build-client

# Run Electron in dev mode
make dev

# Run a single Go test
cd server && go test ./internal/db/ -run TestDeviceCRUD -v

# Clean build artifacts
make clean
```

## Architecture

**Go workspace** (`go.work`) with 4 modules: `server`, `daemon`, `pkg`, `test`.

### Server (`server/`)
TLS listener on port 443. `ListenerMux` peeks first byte to route:
- `0x01` → proxy protocol handler (HMAC auth → device lookup → bidirectional relay with traffic counting)
- HTTP → admin API (Basic Auth, REST endpoints for users/devices/traffic stats)

Admin UI (`server/admin-ui/`) is a React/Vite app compiled and embedded into the server binary via Go embed.

Key internals: `mux/` (connection routing), `db/` (SQLite — users, devices, traffic_stats), `admin/` (HTTP API + stats), `stats/` (atomic connection tracker).

### Daemon (`daemon/`)
Local SOCKS5 server (default `127.0.0.1:1080`) that tunnels each connection via TLS to the server. Also exposes an HTTP control API (default `127.0.0.1:9090`) with `/connect`, `/disconnect`, `/status`, `/health`.

Key internals: `tunnel/` (SOCKS5 listener + TLS relay), `socks5/` (protocol handshake), `api/` (control endpoints).

### Shared packages (`pkg/`)
- `auth/` — HMAC-SHA256 authentication. 41-byte messages: version(1) + timestamp(8) + HMAC(32). 30s clock skew tolerance.
- `proto/` — Wire protocol helpers: auth exchange, connect message (addr type + host + port), bidirectional relay with byte counting.

### Client (`client/`)
Electron 33 + React 19 + TypeScript + Vite. Spawns the daemon binary, manages system SOCKS proxy settings, provides key input and connection status UI. Auto-updates via electron-updater.

Main process: `src/main/` (daemon lifecycle in `daemon.ts`, system proxy in `sysproxy.ts`).
Renderer: `src/renderer/` (React UI with `App.tsx`).

### Integration tests (`test/`)
Separate Go module with end-to-end tests covering auth and connection flow.

## Deployment

- **Server**: Docker multi-stage build (React UI → Go binary → Alpine). CI deploys to VPS via `.github/workflows/deploy.yml`.
- **Client**: Tag-triggered release builds macOS DMGs via `.github/workflows/release.yml`.
- **SSL**: `scripts/setup-ssl.sh` manages Let's Encrypt certs.

## Protocol Flow

```
Client App → Daemon (SOCKS5 :1080) → TLS → Server (:443)
                                              ↓
                                        Peek first byte
                                        0x01 → Proxy relay
                                        HTTP → Admin panel
```
