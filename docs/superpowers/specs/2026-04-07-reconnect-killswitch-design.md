# Reconnect Detection + Kill Switch Design

## Overview

Add a third runtime status `Reconnecting` to the daemon's tunnel and TUN engine, surfaced through `/status` and `/tun/status`, that the client renders as "Reconnecting…" in the StatusBar. While in `Reconnecting`, the daemon hard-blocks all user traffic (kill switch) so apps cannot fall back to the native network and leak around the proxy. The state is entered automatically by three independent detectors and cleared automatically once the transport is verified healthy again.

## Goals

- Detect a broken proxy session within ~5–10 seconds in three independent ways: transport closed, health-check failure, traffic stall.
- Hard-block user traffic during recovery so apps that ban accounts on direct-connect (Telegram, Instagram, etc.) cannot leak.
- Automatically recover into `Connected` once the transport is healthy again — no manual user action required.
- Surface the state in the existing StatusBar UI without inventing new visual elements (the `Reconnecting…` rendering already exists in `client/src/renderer/components/StatusBar.tsx:143`).
- Zero server-side changes — VPS instance must not restart mid-session.

## Non-Goals

- Replacing the existing client-side `startReconnect()` retry loop in `App.tsx:56`. That loop covers a different scenario (full Disconnect → re-Connect cycle) and stays untouched.
- Adding new transports, ping protocols, or server-side keepalive frames.
- Reworking the helper IPC or TUN packet relay. The helper stays oblivious to the daemon's status; routing decisions are made entirely inside the daemon's forwarders.

## Architecture

### State Machine

Both `tunnel.Tunnel` and `tun.Engine` get a third status:

```
Disconnected ──Start()──→ Connected
                              │
                              ↓ (detector trips)
                          Reconnecting ──→ Connected (recovered)
                              │
                              ↓ (recovery exhausted)
                          Disconnected
```

Transitions:

- **Disconnected → Connected** — successful `Start()`. Existing path, unchanged.
- **Connected → Reconnecting** — any of D1/D2/D3 fires (see Detectors below). Triggered by a single serialized method `setReconnecting()` that holds `mu` for the transition and is idempotent (second call within the same state is a no-op).
- **Reconnecting → Connected** — `reconnectTransport()` returned a fresh transport (D1 recovery) OR `verifyServer()` succeeded after a failure (D2 recovery). Single serialized method `setConnected()` which sets `status = Connected`, resets `failures = 0`, and refreshes `meter.lastByteAt = time.Now()` so D3 does not immediately re-trip on the next tick. Note: D3 cannot recover the tunnel by itself — once the kill switch is engaged, no user bytes can flow, so the only way out is via D1 or D2.
- **Reconnecting → Disconnected** — `reconnectTransport()` exhausted its 5 attempts (~15 seconds). Existing behaviour, preserved.
- **\* → Disconnected** — explicit `Stop()` from the client. Uses existing `stopLocked()` which ignores the current status.

Reconnecting is **not** the same as Loading (`Connecting…`). Loading is a fresh user-initiated connect; Reconnecting is recovery from a previously-Connected session. The two are distinct in the UI and in the API.

### Detectors

All three detectors live inside the existing `healthLoop()` (`daemon/internal/tunnel/tunnel.go:283`), which is already the single arbiter of tunnel health. No new goroutines, no new locks — the `select` keeps everything serialized.

#### D1 — Transport closed (instant)

The existing `case <-doneCh:` already fires when the transport's `DoneChan()` closes. Today it jumps straight into `reconnectTransport()`. We change it to:

1. `setReconnecting()` first (engages kill switch).
2. Then `reconnectTransport()` as before.
3. On success → `setConnected()`. On failure → `stopLocked()` → Disconnected.

For the UDP transport this fires within ~50ms of the network breaking. For TLS over TCP it almost never fires until the OS gives up the TCP socket (minutes), so D2 and D3 carry the load there.

#### D2 — Health-check fail (5–15s)

Two changes to the existing periodic `verifyServer()` ticker:

- `healthInterval`: 30s → **5s**.
- The existing `failures` counter still climbs from 0 → maxRetries (3), but the side-effects shift earlier in the count:

| `failures` after tick | Status action |
|---|---|
| 0 → 1 (first fail) | `setReconnecting()` (kill switch on, status visible to user) |
| 1 → 2 (second consecutive fail) | stay in Reconnecting, no action |
| 2 → 3 (third consecutive fail) | exhausted → `stopLocked()` → Disconnected with `lastError = "Server temporarily unavailable, try again later"` |
| any fail → success | `setConnected()`, reset `failures = 0` |

This catches "server is fully unreachable" within ~5 seconds for the user-visible Reconnecting state and within ~15 seconds for full Disconnected.

#### D3 — Stall detector (5s of dead air during active traffic)

New. On every `healthLoop` tick (5s), check:

```
if len(activeHosts) > 0 && time.Since(meter.LastByteAt()) > stallThreshold {
    setReconnecting()
}
```

Where:

- `activeHosts` is the same map fed by `touchHost()` from the prior bug fix — its presence means "the user is actively trying to use the proxy right now".
- `meter.LastByteAt()` is a new method on `dstats.RateMeter`. The meter already counts bytes via `Add(in, out)`; we add `lastByteAt time.Time` next to the existing counters and update it inside `Add` under the existing mutex.
- `stallThreshold = 5 * time.Second`. Rationale: 3s causes false positives during ordinary network jitter (wifi handoff, mobile congestion); 10s lets a banned-on-direct app fire several leaked requests before we react; 5s lines up with two-three TCP retransmits.

The detector deliberately stays silent when `len(activeHosts) == 0` so an idle session does not flip to Reconnecting permanently.

D3 is purely a **trigger**, never a recovery path. Once the kill switch engages, no user bytes can flow (relays are closed and new ones rejected), so `lastByteAt` cannot move forward by itself. Recovery happens through D1 (transport restored) or D2 (`verifyServer()` succeeded). Both `setConnected()` paths refresh `lastByteAt = time.Now()` so the next D3 tick after recovery sees a fresh timestamp and stays silent.

Initial seed: when `Start()` flips to `Connected`, `meter.lastByteAt = time.Now()` so the first tick after start does not falsely trip on a zero timestamp.

### Kill Switch

The contract: while status == `Reconnecting`, **no byte of user traffic crosses our boundary in either direction**.

#### SOCKS5 mode (`tunnel.Tunnel`)

On entering `Reconnecting`:

1. `CloseAllConns()` — already exists in `tunnel.go:138`. Closes every in-flight relay; both goroutines in `countingRelay` unblock and return.

In `handleSOCKS()` (`tunnel.go:391`), add an early-return immediately after the SOCKS5 handshake succeeds:

```go
req, err := socks5.Handshake(conn)
if err != nil { ... return }

if t.GetStatus() == Reconnecting {
    socks5.SendFailure(conn)
    return
}
```

Browser semantics: PAC routes traffic through the local SOCKS5 port, system proxy stays enabled, native dialer is never consulted. The browser sees a SOCKS5 failure response, retries within seconds, gets failure again. No leakage to the native network. Once the daemon flips back to `Connected`, the next retry succeeds and traffic flows.

Listener stays open. Closing the listener risks losing the port number, racing with `Stop()`, and complicating recovery — there's no value in it because the early-return inside `handleSOCKS()` is sufficient.

#### TUN mode (`tun.Engine`)

TUN routes packets through gVisor netstack into per-protocol forwarders, not through SOCKS5. Same intent, different mechanism.

On entering `Reconnecting`:

1. Close every active TCP and UDP stream tracked inside the engine. Each forwarder maintains a registry of in-flight conns; we add `CloseAll()` to each registry and call them from `setReconnecting()`.
2. New TCP SYNs: TCP forwarder, on receiving a new connection request from the gVisor stack, checks `engine.GetStatus()`. If `Reconnecting`, it completes the gVisor handshake just enough to send TCP RST back to the originating app via the netstack API. Apps see "connection refused" instantly.
3. New UDP packets: UDP forwarder drops packets silently — no ICMP, no response. Apps see UDP timeout.

Helper TUN device, system routes, and the helper IPC connection all remain intact during Reconnecting. When status flips back to `Connected`, no route reinstall is needed — packets simply start flowing through the forwarders again.

### Status API

Both `/status` and `/tun/status` extend their `status` enum:

```go
type StatusResponse struct {
    Status string `json:"status"` // "connected" | "disconnected" | "reconnecting"
    Uptime int64  `json:"uptime"`
    Error  string `json:"error,omitempty"`
}
```

No new fields. The string carries everything the client needs.

### Client Integration

#### `useDaemon.ts`

Type widening:

```ts
interface DaemonStatus {
  status: "connected" | "disconnected" | "reconnecting";
  uptime: number;
}
```

The existing transition hook (`prevStatus.current === "connected" && data.status === "disconnected"` → disable sysproxy) is **kept**, but `reconnecting` is treated as "do nothing" — sysproxy stays enabled, the kill switch upstream blocks traffic, and any `disconnected` arriving later (after recovery exhausted) still triggers the cleanup.

#### `App.tsx`

Add a derived flag:

```ts
const daemonReconnecting =
  socksStatus.status === "reconnecting" ||
  tunStatus === "reconnecting";
```

The existing client-side `reconnecting` state (the `startReconnect()` retry loop) is NOT removed. It still handles the "daemon went all the way to Disconnected" case. The two flags are OR-merged into the StatusBar prop:

```tsx
reconnecting={reconnecting || daemonReconnecting}
```

`isConnected` stays driven by `tunStatus === "active"` / `socksStatus.status === "connected"`. During `Reconnecting` it is **false**, which means:

- StatusBar shows "Reconnecting…" + spinner (not "Connected" + globe).
- `useStats(isConnected)` pauses speed graph polling.
- `appInfo.setTrayStatus(false)` updates the tray icon.

#### Disconnect button accessibility

Currently `StatusBar.tsx:266` disables the Disconnect button when `busy = loading || reconnecting`. During the new daemon-reported Reconnecting state, the user must still be able to manually tear the tunnel down. Change the button's `disabled` to `loading` only — Reconnecting keeps the button live.

## Testing

### Unit — `daemon/internal/tunnel/tunnel_test.go`

Extend the existing test file (mocks already in place):

1. **`TestStateTransitionsHappyPath`** — Disconnected → Start → Connected, no detectors fire. Sanity.
2. **`TestStallDetectorTrips`** — short `hostLiveWindow` and `stallThreshold`, manually push `lastByteAt` into the past, populate `activeHosts`, run one health tick → status == Reconnecting.
3. **`TestStallDetectorIgnoresIdle`** — same as above but `activeHosts` empty → status remains Connected. Guards against the "idle session in permanent reconnecting" failure mode.
4. **`TestStallRecovery`** — after Reconnecting from D3, call `touchHost()` + `meter.Add()` to refresh `lastByteAt`, run next tick → status returns to Connected.
5. **`TestHandleSOCKSRejectsDuringReconnecting`** — mock `net.Conn`, set `t.status = Reconnecting` directly (in-package test), call `handleSOCKS` → assert SOCKS5 failure response was written to conn and no transport stream was opened.
6. **`TestSetReconnectingIdempotent`** — wrap `CloseAllConns` with a counter via test seam, call `setReconnecting()` twice → counter == 1.
7. **`TestSetReconnectingClosesActiveConns`** — register a mock conn through `trackConn`, call `setReconnecting()`, assert mock conn was closed.

### Unit — `daemon/internal/tun/`

8. **`TestEngineStateTransitions`** — Disconnected/Connected/Reconnecting transitions in isolation, plus `Stop()` from Reconnecting → Disconnected.
9. **`TestEngineCloseAllStreamsOnReconnect`** — register mock TCP/UDP conns in the forwarder registries, flip engine to Reconnecting, assert all registries were swept.

The TCP forwarder's "RST on new SYN while Reconnecting" behaviour requires injecting raw IP packets into the gVisor stack. That's a complex mock setup — skip in v1, add a TODO, validate via the integration test below and the manual smoke test.

### Integration — `test/`

10. **`TestReconnectingOnServerDeath`** — start a mock TLS server, daemon Connect, then `mockServer.Close()`. Within ~6 seconds `/status` should report `"reconnecting"`. Restart the mock server. Within another ~6 seconds `/status` should report `"connected"`.

### Manual smoke

11. Telegram + airplane mode toggle → StatusBar flips to Reconnecting within 10s, Telegram stops sending, airplane mode off → Reconnecting clears, Telegram catches up.
12. 30-minute idle session with client open → status stays solid Connected, no reconnect flicker.
13. Pull network cable for 60s → Reconnecting for ~15s → Disconnected with "server unavailable" error (existing exhausted-retry path).

## Acceptance Criteria

- Open Instagram, kill the network → UI shows "Reconnecting…" within 10 seconds, browser requests start failing (no native fallback).
- Restore the network → UI returns to "Connected" within 10 seconds, traffic resumes.
- Idle session (no apps actively using the proxy) for 30 minutes → status stable, no false reconnect events.
- Server fully unreachable for 60+ seconds → ~15 seconds in Reconnecting, then Disconnected with the existing "server unavailable" error.
- Disconnect button is responsive even while in Reconnecting state.

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| D2 fires too aggressively on flaky cellular and disrupts a working session | 5s interval + need at least one `verifyServer()` failure that's followed by recovery. The combined cost of one false positive is one TLS handshake per 5s — measured cost; not a real CPU concern. |
| D3 false positive during a slow CDN burst (large image, no client traffic for 5s) | Stall threshold tuned to 5s based on TCP retransmit cadence; D3 only fires when `activeHosts > 0`, which already implies the user is actively trying to do something. Over-eager false positives are acceptable per user requirement ("better wrong-positive than missed"). |
| Kill switch traps the user in Reconnecting if the network never recovers | Existing `reconnectTransport()` exhausted-retries path still leads to Disconnected with an error message. User can then click Connect manually. |
| Two reconnect flags (client-side `reconnecting` and daemon-reported `daemonReconnecting`) create double rendering | They're OR-merged into a single prop. State machine in StatusBar already handles a single boolean; adding two sources is just `||`. |
| `setReconnecting()` racing with `Stop()` | Both methods take `t.mu`. Stop wins because it sets `status = Disconnected` and the next health tick exits via `<-t.stopHealth`. |
