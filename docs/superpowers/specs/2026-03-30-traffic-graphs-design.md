# Traffic Graphs — Design Spec

Real-time traffic speed graphs in the Electron client and admin panel for diagnosing bandwidth issues.

## Problem

User in Russia connecting through VPS in Netherlands (1 Gbps). Experiencing video lag on YouTube/streaming. No visibility into current bandwidth — only cumulative totals in admin panel. Need real-time speed metrics to determine if the issue is bandwidth, server load, or network latency.

## Design Decisions

- **Ring buffer in memory** — no SQLite for rate data. 300 points (5 min at 1/sec). Lost on restart, acceptable for short-term diagnostics.
- **Dual-source metrics** — daemon counts locally for client (zero latency), server counts for admin panel (all devices).
- **Polling, not WebSocket** — matches existing architecture. Client polls daemon at 1 sec, admin polls server at 3 sec.
- **Client placement** — sparkline graph above connect button (variant A).
- **Admin placement** — device cards with line charts replacing the active connections table (variant B).

## 1. Shared Types: `pkg/stats/`

**New package `pkg/stats/`** — shared between daemon and server.

- `RatePoint`: `{Timestamp int64, BytesIn int64, BytesOut int64}` — bytes/sec for that second.
- `RingBuffer`: fixed-size `[300]RatePoint` array, write index, count. Methods: `Add(point)`, `Slice() []RatePoint` (returns copy, oldest first).

## 2. Daemon: Rate Meter

### New package `daemon/internal/stats/`

**RateMeter struct:**
- Atomic `bytesIn`, `bytesOut` int64 — accumulated since last tick.
- `*stats.RingBuffer` from `pkg/stats/`.
- Background goroutine (1 sec ticker): reads and resets atomic counters, appends to ring buffer.
- `Add(in, out int64)` — atomically adds to counters. Called from relay callbacks.
- `Snapshot() RateSnapshot` — returns current rate (latest point) + history slice (copy of ring buffer contents).
- `Stop()` — stops the ticker goroutine.

### Integration points

**SOCKS5 tunnel** (`daemon/internal/tunnel/tunnel.go`):
- Pass `RateMeter` to tunnel. In the relay callback (currently just `proto.Relay`), switch to `proto.CountingRelay` with `onBytes` calling `meter.Add(in, out)`.

**TUN engine** (`daemon/internal/tun/engine.go`):
- Pass `RateMeter` to engine. In TCP/UDP forwarders, call `meter.Add()` when bytes pass through the proxy path.
- Bypassed traffic (direct dial) is NOT counted — only proxied traffic matters for speed diagnostics.

### New endpoint

`GET /stats` on daemon control API (`:9090`):

```json
{
  "download": 3200000,
  "upload": 180000,
  "history": [
    {"t": 1706000100, "down": 2800000, "up": 150000},
    {"t": 1706000101, "down": 3200000, "up": 180000}
  ]
}
```

Returns `{"download": 0, "upload": 0, "history": []}` when disconnected.

## 3. Server: Per-Device Rate Tracking

### Changes to `server/internal/stats/tracker.go`

**Per-connection delta tracking:**
- Add `prevBytesIn`, `prevBytesOut` int64 fields to `ConnInfo` — snapshot of last tick's values.
- 1 sec ticker goroutine in Tracker: iterates active connections, computes delta from prev values, aggregates deltas by DeviceID.

**Per-device ring buffer:**
- `deviceBuffers map[int64]*RingBuffer` in Tracker (mutex-protected).
- Buffer created on first `Add()` for a device.
- Buffer removed when device has 0 active connections (last `Remove()` for that device).
- Uses `*stats.RingBuffer` from `pkg/stats/`.

### New endpoint

`GET /admin/api/stats/rate` (Basic Auth protected):

```json
[
  {
    "device_id": 1,
    "device_name": "MacBook Pro",
    "user_name": "ilya",
    "download": 3200000,
    "upload": 180000,
    "total_bytes": 1500000000,  // sum of BytesIn+BytesOut across all active connections for this device
    "connections": 3,
    "history": [
      {"t": 1706000100, "down": 2800000, "up": 150000},
      {"t": 1706000101, "down": 3200000, "up": 180000}
    ]
  }
]
```

Only devices with active connections are included.

## 4. Client: Speed Graph

### New hook `useStats()` (`client/src/renderer/hooks/useStats.ts`)

- Polls `GET http://localhost:9090/stats` every 1 second.
- Only active when daemon status is `connected`.
- Returns `{download: number, upload: number, history: Array<{t: number, down: number, up: number}>}`.
- Clears data on disconnect.

### New component `SpeedGraph` (`client/src/renderer/components/SpeedGraph.tsx`)

- Pure SVG sparkline, no external chart library.
- Two polylines: download (green `#4ade80`), upload (blue `#60a5fa`).
- Above graph: current speed text — `↓ 2.4 MB/s` and `↑ 340 KB/s`.
- Auto-scales Y axis to max value in visible history.
- Renders only when connected; hidden when disconnected.
- Dark background matching client theme (`#12122a` area inside `#1a1a2e` window).

### Integration in `App.tsx`

- SpeedGraph placed between header area and connect button.
- Appears with CSS transition on connect, disappears on disconnect.
- Uses data from `useStats()` hook.

## 5. Admin UI: Device Cards

### Changes to `Dashboard.tsx`

**New polling:**
- `api.rate()` every 3 seconds, parallel to existing `overview()` polling.

**Active Devices section — card layout:**
- Each device with active connections rendered as a card.
- Card contents:
  - Header: device name (left), total bytes (right).
  - Current speed: `↓ 3.2 MB/s` / `↑ 180 KB/s`.
  - Recharts `LineChart`: two `Line` components (download green, upload blue), last 5 minutes.
  - Active connections count.
- Cards sorted by total current speed (download + upload) descending.
- When no active devices: "No active connections" message.

### New API method in `api.ts`

```typescript
interface DeviceRate {
  device_id: number;
  device_name: string;
  user_name: string;
  download: number;
  upload: number;
  total_bytes: number;
  connections: number;
  history: Array<{ t: number; down: number; up: number }>;
}

export async function rate(): Promise<DeviceRate[]> {
  return get('/admin/api/stats/rate');
}
```

## 6. Data Flow Summary

```
Client App
  ├─ SOCKS5 relay ──→ daemon RateMeter.Add()
  └─ TUN packets ──→ daemon RateMeter.Add()
                          │
                     1 sec ticker → ring buffer (300 points)
                          │
                     GET /stats (polling 1 sec)
                          │
                     SpeedGraph component (SVG sparkline)

Server (:443)
  ├─ TCP relay ──→ Tracker.AddBytes() (existing)
  └─ UDP relay ──→ Tracker.AddBytes() (existing)
                          │
                     1 sec ticker → per-device ring buffer (300 points)
                          │
                     GET /admin/api/stats/rate (polling 3 sec)
                          │
                     Dashboard device cards (Recharts LineChart)
```

## What Does NOT Change

- Existing `CountingRelay` logic in `pkg/proto/`.
- Hourly SQLite aggregation in `server/internal/db/`.
- All existing admin API endpoints (`/overview`, `/active`, `/traffic`, `/traffic/{id}/daily`).
- UserDetail page with daily bar charts.

## Estimated Scope

| Area | Files | Effort |
|------|-------|--------|
| `pkg/stats/` | New package, ~50 LOC | RingBuffer + RatePoint |
| `daemon/internal/stats/` | New package, ~60 LOC | RateMeter (uses pkg/stats) |
| `daemon/internal/tunnel/`, `tun/` | Modify relay integration | Add `meter.Add()` calls |
| `daemon/internal/api/` | Add `/stats` endpoint | ~20 LOC |
| `server/internal/stats/` | Extend tracker | Ring buffer + ticker, ~80 LOC |
| `server/internal/admin/` | Add `/rate` endpoint | ~30 LOC |
| `client/src/renderer/hooks/` | `useStats.ts` | ~30 LOC |
| `client/src/renderer/components/` | `SpeedGraph.tsx` | ~60 LOC |
| `client/src/renderer/App.tsx` | Integration | ~10 LOC |
| `server/admin-ui/src/lib/api.ts` | Add `rate()` | ~15 LOC |
| `server/admin-ui/src/pages/Dashboard.tsx` | Device cards | ~80 LOC |
