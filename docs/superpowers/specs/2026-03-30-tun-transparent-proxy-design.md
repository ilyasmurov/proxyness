# TUN Transparent Proxy — Design Spec

## Overview

Replace system SOCKS5 proxy (which apps like Telegram/Discord ignore) with a TUN-based transparent proxy that captures all IP traffic. Uses gVisor netstack for userspace packet processing. SOCKS5 remains as a fallback mode.

Platforms: macOS + Windows.

## Architecture

```
App (any) --> IP packet --> TUN device (utun / wintun)
                                |
                      gVisor netstack (userspace)
                      parses packets -> TCP/UDP connections
                                |
                      +---------------------+
                      |  TUN proxy engine   |
                      |  split tunneling    |
                      |  PID -> app lookup  |
                      +--------+------------+
                               |
               proxy path:     |      bypass path:
               TLS tunnel -----+----> direct internet
                  |
            server :443
                  |
            dial target -> relay
```

## 1. Server — UDP Relay

Extend `pkg/proto/` wire protocol:

- **Existing** `0x01` (connect) — TCP relay, unchanged.
- **New** `0x02` (UDP associate) — opens UDP relay session.

### UDP framing inside TLS

Each UDP datagram is wrapped in a frame:

```
[2 bytes: payload length][addr_type + addr + port][payload]
```

- addr_type/addr/port use same encoding as TCP connect messages (0x01 IPv4, 0x03 domain, 0x04 IPv6).
- Server unwraps, sends UDP datagram to target, wraps response back.
- One TLS connection per UDP session (bound to client src addr:port).
- Inactivity timeout: 60 seconds.
- Auth and traffic tracking — same as TCP.

### Server handler changes (`server/cmd/main.go`)

After auth + device lookup, read message type byte:
- `0x01` — existing TCP flow (ReadConnect -> dial TCP -> relay).
- `0x02` — new UDP flow (ReadConnect for bind addr -> UDP listen -> frame relay).

## 2. Privileged Helper

Separate binary that runs with elevated privileges. Does two things only:
1. Creates TUN device, passes fd/handle to daemon.
2. Configures system route table to direct traffic into TUN.

### macOS

- Binary: `smurov-helper`, installed to `/Library/PrivilegedHelperTools/`.
- Registered as launchd service (`com.smurov.proxy.helper.plist`).
- One-time password prompt via `SMAppService`.
- IPC: Unix socket `/var/run/smurov-helper.sock`.
- TUN device: macOS native `utun` (via `syscall`).

### Windows

- Installed as Windows Service (`SmurovProxyHelper`).
- Registered during NSIS installation (UAC prompt already happens).
- IPC: Named pipe `\\.\pipe\smurov-helper`.
- TUN device: `wintun.dll` (WireGuard library, ~400KB, bundled in extraResources).

## 3. Daemon — TUN Engine

New package: `daemon/internal/tun/`.

### Files

- `device.go` — receives TUN fd from helper, creates gVisor netstack stack on top of it.
- `engine.go` — main loop:
  1. gVisor netstack surfaces TCP/UDP connections.
  2. For each connection: lookup PID by source port (OS API).
  3. Resolve PID to app name/path.
  4. Check rules -> proxy or bypass.
  5. Proxy: TLS tunnel to server (TCP connect or UDP associate).
  6. Bypass: direct connection to internet.
- `rules.go` — stores split tunneling rules, two modes:
  - **Proxy all, except...** — listed apps go direct.
  - **Proxy only...** — listed apps go through proxy, rest direct.
- `procinfo_darwin.go` — macOS PID lookup via `proc_pidinfo` / `libproc`.
- `procinfo_windows.go` — Windows PID lookup via `GetExtendedTcpTable` / `GetExtendedUdpTable`.

### API extensions (`daemon/internal/api/`)

New endpoints (existing SOCKS5 endpoints unchanged):

| Method | Path | Description |
|--------|------|-------------|
| POST | `/tun/start` | Request helper to create TUN, start engine |
| POST | `/tun/stop` | Stop engine, request helper to remove TUN |
| GET | `/tun/status` | TUN state (active/inactive) |
| GET | `/tun/apps` | Running apps with network activity |
| POST | `/tun/rules` | Update split tunneling rules |
| GET | `/tun/rules` | Current rules |

### DNS handling

- DNS queries (UDP port 53) intercepted in gVisor netstack.
- Proxied apps: DNS forwarded through TLS tunnel (server resolves via system DNS). Prevents DNS leak.
- Bypass apps: DNS goes direct.
- No special server-side code — DNS is regular UDP traffic through UDP relay.

## 4. Client UI

### Main screen — mode selector

Below the Connect button, add a selector:
- **Full (TUN)** — all traffic through proxy (default).
- **Browser only (SOCKS5)** — current behavior.

Connect logic:
- TUN mode: `POST /tun/start {server, key, rules}`.
- SOCKS5 mode: `POST /connect {server, key}` + `enableSystemProxy()`.
- Disconnect: `/tun/stop` or `/disconnect` + `disableSystemProxy()`.

### Apps screen — split tunneling

- Toggle between modes: "Proxy all except..." / "Proxy only..."
- List of apps with on/off toggles.
- App list from `GET /tun/apps` (daemon reports apps with network activity).
- Manual add option (select .app / .exe).
- Rules saved locally and sent to daemon via `POST /tun/rules`.

## 5. Build & Distribution

### Makefile

New target: `build-helper` — builds helper binary for macOS (arm64/amd64) and Windows (amd64).

### electron-builder changes

- `extraResources`: add `helper-*` binary and `wintun.dll` (Windows only).
- NSIS script: register/unregister `SmurovProxyHelper` Windows Service on install/uninstall.
- macOS: postinstall script installs helper to `/Library/PrivilegedHelperTools/` and loads launchd plist.

### CI (release.yml)

- Add `build-helper` step before `build-client`.
- Helper binaries placed in `client/resources/` alongside daemon binaries.

## 6. Dependencies

New Go dependencies for `daemon/`:
- `gvisor.dev/gvisor` — userspace network stack (TCP/UDP from TUN packets).
- `golang.zx2c4.com/wintun` — Windows TUN device (thin wrapper around wintun.dll).

No new dependencies for `server/` or `pkg/` (UDP relay uses stdlib `net`).

## 7. Backward Compatibility

- SOCKS5 mode fully preserved — no changes to existing tunnel/socks5/api code.
- Server protocol extended (new message type 0x02) — old daemons continue working (they only send 0x01).
- Helper is optional — if not installed, TUN mode unavailable, SOCKS5 still works.
