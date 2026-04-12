# Per-App TLS Toggle

Optional TLS wrapping per app in TUN mode. Apps like Telegram and Discord already encrypt their own traffic (MTProto / HTTPS), so TLS wrapping by the proxy is redundant double encryption that adds latency.

## Architecture

### Server: Pre-TLS Mux on Port 443

Replace `tls.Listen` with `net.Listen`. Peek first byte to route:

- `0x16` (TLS ClientHello) -> TLS handshake -> existing mux (proxy + admin panel)
- `0x01` (proxy protocol version byte = start of HMAC auth) -> raw TCP proxy relay

Implementation in `server/internal/mux/mux.go`:
- New `PreTLSMux` wraps existing `ListenerMux`
- Accepts raw `net.Conn`, peeks first byte
- If TLS: does `tls.Server()` handshake, passes to existing `ListenerMux`
- If raw: passes directly to proxy handler (skipping HTTP routing — raw connections are always proxy traffic)

Server `cmd/main.go` changes: `tls.Listen` -> `net.Listen`, pass `*tls.Config` to `PreTLSMux` for on-demand TLS wrapping.

### Daemon: Per-App TLS Flag in Rules

Extend `Rules` struct in `daemon/internal/tun/rules.go`:

```go
type Rules struct {
    mu        sync.RWMutex
    mode      Mode
    apps      map[string]bool
    noTLSApps map[string]bool  // apps that connect without TLS
}

type rulesJSON struct {
    Mode      Mode     `json:"mode"`
    Apps      []string `json:"apps"`
    NoTLSApps []string `json:"no_tls_apps"`
}
```

New method: `Rules.ShouldUseTLS(appPath string) bool` — returns false if app is in `noTLSApps`.

In `engine.go`, `proxyTCP()` and `proxyUDP()`:
- If `ShouldUseTLS` returns true: current flow (TLS + HMAC)
- If false: `protectedDial("tcp", e.serverAddr)` -> HMAC auth -> relay (no TLS handshake)

### Client UI: TLS Toggle Per App

In `AppRules.tsx`, add a toggle next to each app in "Selected apps" mode.

State:
```typescript
noTLS: Set<string>  // app IDs with TLS disabled
```

Persisted in `localStorage` under `"proxyness-no-tls"`.

When sending rules to daemon, map enabled app IDs with `noTLS` flag to `no_tls_apps` paths:

```typescript
{
  mode: "proxy_only",
  apps: [...allEnabledPaths],
  no_tls_apps: [...pathsOfNoTLSApps]
}
```

Default: TLS ON for all apps (empty `no_tls_apps`).

## Files to Change

| File | Change |
|------|--------|
| `server/cmd/main.go` | `tls.Listen` -> `net.Listen`, pass TLS config to mux |
| `server/internal/mux/mux.go` | Add `PreTLSMux` that peeks first byte for TLS vs raw |
| `daemon/internal/tun/rules.go` | Add `noTLSApps` field, `ShouldUseTLS()` method |
| `daemon/internal/tun/engine.go` | Branch `proxyTCP`/`proxyUDP` on `ShouldUseTLS` |
| `client/src/renderer/components/AppRules.tsx` | TLS toggle per app, `noTLS` state, localStorage |
| `client/src/main/index.ts` | Pass `no_tls_apps` through IPC to daemon |

## Constraints

- SOCKS5 tunnel always uses TLS (browsers, no per-app control there)
- Raw TCP connections skip HTTP mux on server — they are always proxy traffic
- HMAC auth is always required regardless of TLS
- Default is TLS ON for all apps
- Toggle only visible in "Selected apps" mode
