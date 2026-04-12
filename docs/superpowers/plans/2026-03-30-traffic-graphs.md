# Traffic Graphs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add real-time traffic speed graphs to the Electron client (sparkline above connect button) and admin panel (per-device cards with line charts).

**Architecture:** Daemon counts bytes locally via RateMeter for the client (1 sec polling). Server extends its existing Tracker with per-device ring buffers for the admin panel (3 sec polling). Both use a shared RingBuffer type from `pkg/stats/`. No database changes — all rate data lives in memory (5 min window).

**Tech Stack:** Go (atomic counters, ring buffers), React/TypeScript (SVG sparkline in client, Recharts LineChart in admin UI).

**Spec:** `docs/superpowers/specs/2026-03-30-traffic-graphs-design.md`

**Go module paths:** `proxyness/pkg`, `proxyness/daemon`, `proxyness/server`

---

## File Structure

### New files
- `pkg/stats/ringbuffer.go` — Shared RingBuffer + RatePoint types
- `pkg/stats/ringbuffer_test.go` — Unit tests
- `daemon/internal/stats/meter.go` — RateMeter (atomic counters + ring buffer + ticker)
- `daemon/internal/stats/meter_test.go` — Unit tests
- `client/src/renderer/hooks/useStats.ts` — Polling hook for daemon /stats
- `client/src/renderer/components/SpeedGraph.tsx` — SVG sparkline component

### Modified files
- `server/internal/stats/tracker.go` — Add per-device ring buffers + rate ticker
- `server/internal/stats/tracker_test.go` — New test (or extend existing)
- `server/internal/admin/admin.go` — Add `GET /admin/api/stats/rate` endpoint
- `daemon/internal/tunnel/tunnel.go` — Accept meter, switch `Relay` → `CountingRelay`
- `daemon/internal/tun/engine.go` — Accept meter, add byte counting in proxy paths
- `daemon/internal/api/api.go` — Accept meter, add `GET /stats` endpoint
- `daemon/cmd/main.go` — Create meter, pass to tunnel/engine/api
- `client/src/renderer/App.tsx` — Insert SpeedGraph above connect button
- `server/admin-ui/src/lib/api.ts` — Add `DeviceRate` interface + `rate()` method
- `server/admin-ui/src/lib/format.ts` — Add `formatSpeed()` helper
- `server/admin-ui/src/pages/Dashboard.tsx` — Replace active connections table with device cards

---

### Task 1: Shared RingBuffer (`pkg/stats/`)

**Files:**
- Create: `pkg/stats/ringbuffer.go`
- Create: `pkg/stats/ringbuffer_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pkg/stats/ringbuffer_test.go
package stats

import "testing"

func TestRingBufferAddAndSlice(t *testing.T) {
	rb := NewRingBuffer()
	rb.Add(RatePoint{Timestamp: 1, BytesIn: 100, BytesOut: 50})
	rb.Add(RatePoint{Timestamp: 2, BytesIn: 200, BytesOut: 80})

	s := rb.Slice()
	if len(s) != 2 {
		t.Fatalf("expected 2 points, got %d", len(s))
	}
	if s[0].Timestamp != 1 || s[1].Timestamp != 2 {
		t.Fatalf("wrong order: %v", s)
	}
	if s[0].BytesIn != 100 || s[1].BytesIn != 200 {
		t.Fatalf("wrong values: %v", s)
	}
}

func TestRingBufferWraparound(t *testing.T) {
	rb := NewRingBuffer()
	// Fill beyond capacity (300)
	for i := int64(0); i < 310; i++ {
		rb.Add(RatePoint{Timestamp: i, BytesIn: i * 10, BytesOut: i})
	}

	s := rb.Slice()
	if len(s) != 300 {
		t.Fatalf("expected 300 points, got %d", len(s))
	}
	// Oldest should be 10 (0-9 evicted)
	if s[0].Timestamp != 10 {
		t.Fatalf("expected oldest timestamp 10, got %d", s[0].Timestamp)
	}
	// Newest should be 309
	if s[299].Timestamp != 309 {
		t.Fatalf("expected newest timestamp 309, got %d", s[299].Timestamp)
	}
}

func TestRingBufferEmpty(t *testing.T) {
	rb := NewRingBuffer()
	s := rb.Slice()
	if len(s) != 0 {
		t.Fatalf("expected empty slice, got %d", len(s))
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `cd pkg && go test ./stats/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement RingBuffer**

```go
// pkg/stats/ringbuffer.go
package stats

import "sync"

// RatePoint is a single rate measurement (bytes per second).
type RatePoint struct {
	Timestamp int64 `json:"t"`
	BytesIn   int64 `json:"down"`
	BytesOut  int64 `json:"up"`
}

const RingSize = 300

// RingBuffer is a fixed-size circular buffer of RatePoints.
type RingBuffer struct {
	mu    sync.RWMutex
	buf   [RingSize]RatePoint
	write int
	count int
}

func NewRingBuffer() *RingBuffer {
	return &RingBuffer{}
}

func (r *RingBuffer) Add(p RatePoint) {
	r.mu.Lock()
	r.buf[r.write] = p
	r.write = (r.write + 1) % RingSize
	if r.count < RingSize {
		r.count++
	}
	r.mu.Unlock()
}

// Slice returns a copy of all points, oldest first.
func (r *RingBuffer) Slice() []RatePoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil
	}
	result := make([]RatePoint, r.count)
	if r.count < RingSize {
		copy(result, r.buf[:r.count])
	} else {
		n := copy(result, r.buf[r.write:])
		copy(result[n:], r.buf[:r.write])
	}
	return result
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `cd pkg && go test ./stats/ -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/stats/
git commit -m "feat: add shared RingBuffer for rate tracking (pkg/stats)"
```

---

### Task 2: Daemon RateMeter (`daemon/internal/stats/`)

**Files:**
- Create: `daemon/internal/stats/meter.go`
- Create: `daemon/internal/stats/meter_test.go`

- [ ] **Step 1: Write failing tests**

```go
// daemon/internal/stats/meter_test.go
package stats

import (
	"testing"
	"time"
)

func TestRateMeterAdd(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	m.Add(1000, 500)
	m.Add(2000, 300)

	// Wait for at least one tick
	time.Sleep(1200 * time.Millisecond)

	snap := m.Snapshot()
	if snap.Download != 3000 {
		t.Fatalf("expected download 3000, got %d", snap.Download)
	}
	if snap.Upload != 800 {
		t.Fatalf("expected upload 800, got %d", snap.Upload)
	}
	if len(snap.History) != 1 {
		t.Fatalf("expected 1 history point, got %d", len(snap.History))
	}
}

func TestRateMeterMultipleTicks(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	m.Add(1000, 100)
	time.Sleep(1200 * time.Millisecond)

	m.Add(2000, 200)
	time.Sleep(1200 * time.Millisecond)

	snap := m.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("expected 2 history points, got %d", len(snap.History))
	}
	// Latest tick should have the second batch
	if snap.Download != 2000 {
		t.Fatalf("expected download 2000, got %d", snap.Download)
	}
}

func TestRateMeterSnapshotWhenEmpty(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	snap := m.Snapshot()
	if snap.Download != 0 || snap.Upload != 0 {
		t.Fatalf("expected zeros, got %d/%d", snap.Download, snap.Upload)
	}
	if len(snap.History) != 0 {
		t.Fatalf("expected empty history, got %d", len(snap.History))
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `cd daemon && go test ./internal/stats/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement RateMeter**

```go
// daemon/internal/stats/meter.go
package stats

import (
	"sync/atomic"
	"time"

	pkgstats "proxyness/pkg/stats"
)

type RateMeter struct {
	bytesIn  atomic.Int64
	bytesOut atomic.Int64
	ring     *pkgstats.RingBuffer
	stop     chan struct{}
}

func NewRateMeter() *RateMeter {
	m := &RateMeter{
		ring: pkgstats.NewRingBuffer(),
		stop: make(chan struct{}),
	}
	go m.tick()
	return m
}

func (m *RateMeter) Add(in, out int64) {
	m.bytesIn.Add(in)
	m.bytesOut.Add(out)
}

func (m *RateMeter) tick() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			in := m.bytesIn.Swap(0)
			out := m.bytesOut.Swap(0)
			m.ring.Add(pkgstats.RatePoint{
				Timestamp: time.Now().Unix(),
				BytesIn:   in,
				BytesOut:  out,
			})
		case <-m.stop:
			return
		}
	}
}

type Snapshot struct {
	Download int64               `json:"download"`
	Upload   int64               `json:"upload"`
	History  []pkgstats.RatePoint `json:"history"`
}

func (m *RateMeter) Snapshot() Snapshot {
	history := m.ring.Slice()
	var dl, ul int64
	if len(history) > 0 {
		last := history[len(history)-1]
		dl = last.BytesIn
		ul = last.BytesOut
	}
	return Snapshot{
		Download: dl,
		Upload:   ul,
		History:  history,
	}
}

func (m *RateMeter) Stop() {
	close(m.stop)
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `cd daemon && go test ./internal/stats/ -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/stats/
git commit -m "feat: add RateMeter for daemon-side speed tracking"
```

---

### Task 3: Server Tracker Rate Extension

**Files:**
- Modify: `server/internal/stats/tracker.go`
- Create: `server/internal/stats/tracker_rate_test.go`

- [ ] **Step 1: Write failing tests**

```go
// server/internal/stats/tracker_rate_test.go
package stats

import (
	"testing"
	"time"
)

func TestTrackerRates(t *testing.T) {
	tr := New()
	defer tr.Stop()

	// Add two connections for same device
	id1 := tr.Add(1, "MacBook", "ilya")
	id2 := tr.Add(1, "MacBook", "ilya")

	// Simulate traffic
	tr.AddBytes(id1, 1000, 100)
	tr.AddBytes(id2, 2000, 200)

	// Wait for tick to compute rates
	time.Sleep(1200 * time.Millisecond)

	rates := tr.Rates()
	if len(rates) != 1 {
		t.Fatalf("expected 1 device rate, got %d", len(rates))
	}
	r := rates[0]
	if r.DeviceID != 1 {
		t.Fatalf("expected device_id 1, got %d", r.DeviceID)
	}
	if r.Download != 3000 {
		t.Fatalf("expected download 3000, got %d", r.Download)
	}
	if r.Upload != 300 {
		t.Fatalf("expected upload 300, got %d", r.Upload)
	}
	if r.Connections != 2 {
		t.Fatalf("expected 2 connections, got %d", r.Connections)
	}
}

func TestTrackerRatesAfterRemove(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id := tr.Add(1, "MacBook", "ilya")
	tr.AddBytes(id, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	// Remove connection
	tr.Remove(id)

	rates := tr.Rates()
	if len(rates) != 0 {
		t.Fatalf("expected 0 device rates after remove, got %d", len(rates))
	}
}

func TestTrackerRatesMultipleDevices(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id1 := tr.Add(1, "MacBook", "ilya")
	id2 := tr.Add(2, "iPhone", "ilya")

	tr.AddBytes(id1, 5000, 500)
	tr.AddBytes(id2, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	rates := tr.Rates()
	if len(rates) != 2 {
		t.Fatalf("expected 2 device rates, got %d", len(rates))
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

Run: `cd server && go test ./internal/stats/ -run TestTrackerRate -v`
Expected: FAIL — `Stop()` and `Rates()` methods don't exist

- [ ] **Step 3: Implement rate tracking in tracker.go**

Add these imports to `tracker.go`:

```go
import (
	"sync"
	"sync/atomic"
	"time"

	pkgstats "proxyness/pkg/stats"
)
```

Add fields to `Tracker` struct:

```go
type Tracker struct {
	mu     sync.RWMutex
	conns  map[int64]*ConnInfo
	nextID int64

	bufMu         sync.RWMutex
	deviceBuffers map[int]*pkgstats.RingBuffer
	stop          chan struct{}
}
```

Update `New()`:

```go
func New() *Tracker {
	t := &Tracker{
		conns:         make(map[int64]*ConnInfo),
		deviceBuffers: make(map[int]*pkgstats.RingBuffer),
		stop:          make(chan struct{}),
	}
	go t.rateTicker()
	return t
}
```

Add `Stop()` method:

```go
func (t *Tracker) Stop() {
	close(t.stop)
}
```

Add rate ticker and Rates method:

```go
func (t *Tracker) rateTicker() {
	prev := make(map[int64][2]int64) // connID -> [prevBytesIn, prevBytesOut]
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.computeRates(prev)
		case <-t.stop:
			return
		}
	}
}

func (t *Tracker) computeRates(prev map[int64][2]int64) {
	type delta struct{ in, out int64 }
	deltas := make(map[int]delta)
	seen := make(map[int64]bool)

	t.mu.RLock()
	for id, c := range t.conns {
		seen[id] = true
		curIn := atomic.LoadInt64(&c.BytesIn)
		curOut := atomic.LoadInt64(&c.BytesOut)
		p := prev[id]
		d := deltas[c.DeviceID]
		d.in += curIn - p[0]
		d.out += curOut - p[1]
		deltas[c.DeviceID] = d
		prev[id] = [2]int64{curIn, curOut}
	}
	t.mu.RUnlock()

	for id := range prev {
		if !seen[id] {
			delete(prev, id)
		}
	}

	now := time.Now().Unix()
	t.bufMu.Lock()
	for deviceID, d := range deltas {
		buf := t.deviceBuffers[deviceID]
		if buf == nil {
			buf = pkgstats.NewRingBuffer()
			t.deviceBuffers[deviceID] = buf
		}
		buf.Add(pkgstats.RatePoint{Timestamp: now, BytesIn: d.in, BytesOut: d.out})
	}
	t.bufMu.Unlock()
}

type DeviceRate struct {
	DeviceID    int                  `json:"device_id"`
	DeviceName  string               `json:"device_name"`
	UserName    string               `json:"user_name"`
	Download    int64                `json:"download"`
	Upload      int64                `json:"upload"`
	TotalBytes  int64                `json:"total_bytes"`
	Connections int                  `json:"connections"`
	History     []pkgstats.RatePoint `json:"history"`
}

func (t *Tracker) Rates() []DeviceRate {
	type devInfo struct {
		name       string
		userName   string
		totalBytes int64
		connCount  int
	}
	devices := make(map[int]*devInfo)

	t.mu.RLock()
	for _, c := range t.conns {
		d := devices[c.DeviceID]
		if d == nil {
			d = &devInfo{name: c.DeviceName, userName: c.UserName}
			devices[c.DeviceID] = d
		}
		d.totalBytes += atomic.LoadInt64(&c.BytesIn) + atomic.LoadInt64(&c.BytesOut)
		d.connCount++
	}
	t.mu.RUnlock()

	t.bufMu.RLock()
	defer t.bufMu.RUnlock()

	result := make([]DeviceRate, 0, len(devices))
	for id, info := range devices {
		dr := DeviceRate{
			DeviceID:    id,
			DeviceName:  info.name,
			UserName:    info.userName,
			TotalBytes:  info.totalBytes,
			Connections: info.connCount,
			History:     []pkgstats.RatePoint{},
		}
		if buf := t.deviceBuffers[id]; buf != nil {
			dr.History = buf.Slice()
			if len(dr.History) > 0 {
				last := dr.History[len(dr.History)-1]
				dr.Download = last.BytesIn
				dr.Upload = last.BytesOut
			}
		}
		result = append(result, dr)
	}
	return result
}
```

Update `Remove()` — after deleting the connection from the map, clean up device buffer if no more connections for that device:

```go
func (t *Tracker) Remove(id int64) *ConnInfo {
	t.mu.Lock()
	c, ok := t.conns[id]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	delete(t.conns, id)

	// Check if any connections remain for this device
	deviceID := c.DeviceID
	hasMore := false
	for _, other := range t.conns {
		if other.DeviceID == deviceID {
			hasMore = true
			break
		}
	}
	t.mu.Unlock()

	if !hasMore {
		t.bufMu.Lock()
		delete(t.deviceBuffers, deviceID)
		t.bufMu.Unlock()
	}

	return &ConnInfo{
		DeviceID:   c.DeviceID,
		DeviceName: c.DeviceName,
		UserName:   c.UserName,
		StartedAt:  c.StartedAt,
		BytesIn:    atomic.LoadInt64(&c.BytesIn),
		BytesOut:   atomic.LoadInt64(&c.BytesOut),
	}
}
```

- [ ] **Step 4: Run tests — verify they pass**

Run: `cd server && go test ./internal/stats/ -v`
Expected: PASS (new rate tests + existing tests)

- [ ] **Step 5: Commit**

```bash
git add server/internal/stats/
git commit -m "feat: add per-device rate tracking to server Tracker"
```

---

### Task 4: Daemon Wiring (tunnel + TUN + API + main)

**Files:**
- Modify: `daemon/internal/tunnel/tunnel.go`
- Modify: `daemon/internal/tun/engine.go`
- Modify: `daemon/internal/api/api.go`
- Modify: `daemon/cmd/main.go`

- [ ] **Step 1: Add meter to Tunnel**

In `daemon/internal/tunnel/tunnel.go`:

Add import:
```go
import dstats "proxyness/daemon/internal/stats"
```

Add field to `Tunnel` struct:
```go
type Tunnel struct {
	// ... existing fields ...
	meter *dstats.RateMeter
}
```

Update constructor (find `func New...` or where Tunnel is created) to accept meter:
```go
func New(meter *dstats.RateMeter) *Tunnel {
	return &Tunnel{
		// ... existing fields ...
		meter: meter,
	}
}
```

In `handleSOCKS()`, replace the `proto.Relay` call (line ~242):
```go
// Before:
proto.Relay(conn, tlsConn)

// After:
proto.CountingRelay(conn, tlsConn, func(in, out int64) {
	t.meter.Add(in, out)
})
```

- [ ] **Step 2: Add meter to TUN Engine**

In `daemon/internal/tun/engine.go`:

Add import:
```go
import dstats "proxyness/daemon/internal/stats"
```

Add field to `Engine` struct:
```go
type Engine struct {
	// ... existing fields ...
	meter *dstats.RateMeter
}
```

Update `NewEngine` to accept meter:
```go
func NewEngine(meter *dstats.RateMeter) *Engine {
	return &Engine{
		// ... existing fields ...
		meter: meter,
	}
}
```

In `proxyTCP()`, replace `proto.Relay` (line ~358):
```go
// Before:
proto.Relay(local, tlsConn)

// After:
proto.CountingRelay(local, tlsConn, func(in, out int64) {
	e.meter.Add(in, out)
})
```

In `proxyUDP()`, add byte counting where frames are written/read:
- When writing payload to server (local→remote direction, around lines 434-447), add after the write:
  ```go
  e.meter.Add(0, int64(len(payload)))
  ```
- When reading payload from server (remote→local direction, around lines 449-460), add after the read:
  ```go
  e.meter.Add(int64(n), 0)
  ```

Note: `bypassTCP()` and `bypassUDP()` are NOT counted per spec — only proxied traffic.

- [ ] **Step 3: Add meter to API server + /stats endpoint**

In `daemon/internal/api/api.go`:

Add import:
```go
import dstats "proxyness/daemon/internal/stats"
```

Add field to `Server` struct:
```go
type Server struct {
	tunnel     *tunnel.Tunnel
	tunEngine  *tun.Engine
	meter      *dstats.RateMeter
	listenAddr string
}
```

Update `New()`:
```go
func New(tunnel *tunnel.Tunnel, tunEngine *tun.Engine, meter *dstats.RateMeter, listenAddr string) *Server {
	s := &Server{
		tunnel:     tunnel,
		tunEngine:  tunEngine,
		meter:      meter,
		listenAddr: listenAddr,
	}
	// ... rest of init ...
	return s
}
```

Register the route (in the mux setup, alongside existing routes):
```go
mux.HandleFunc("GET /stats", s.handleStats)
```

Add handler:
```go
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.meter.Snapshot())
}
```

- [ ] **Step 4: Wire meter in main.go**

In `daemon/cmd/main.go`:

Add import:
```go
import dstats "proxyness/daemon/internal/stats"
```

Create meter and pass to all components:
```go
// Before:
tnl := tunnel.New()
tunEngine := tun.NewEngine()
srv := api.New(tnl, tunEngine, *listenAddr)

// After:
meter := dstats.NewRateMeter()
tnl := tunnel.New(meter)
tunEngine := tun.NewEngine(meter)
srv := api.New(tnl, tunEngine, meter, *listenAddr)
```

- [ ] **Step 5: Run existing daemon tests**

Run: `cd daemon && go test ./... -v`
Expected: PASS (all existing tests still pass, new code compiles)

- [ ] **Step 6: Commit**

```bash
git add daemon/
git commit -m "feat: wire RateMeter into daemon tunnel, TUN engine, and API"
```

---

### Task 5: Server `/admin/api/stats/rate` Endpoint

**Files:**
- Modify: `server/internal/admin/admin.go`

- [ ] **Step 1: Register route**

In `admin.go`, add to the route registration block (alongside existing stats routes):
```go
mux.HandleFunc("GET /admin/api/stats/rate", h.auth(h.statsRate))
```

- [ ] **Step 2: Add handler**

```go
func (h *Handler) statsRate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.tracker.Rates())
}
```

- [ ] **Step 3: Run server tests**

Run: `cd server && go test ./... -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add server/internal/admin/admin.go
git commit -m "feat: add /admin/api/stats/rate endpoint for real-time device speeds"
```

---

### Task 6: Client SpeedGraph

**Files:**
- Create: `client/src/renderer/hooks/useStats.ts`
- Create: `client/src/renderer/components/SpeedGraph.tsx`
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Create useStats hook**

```typescript
// client/src/renderer/hooks/useStats.ts
import { useState, useEffect, useCallback } from "react";

interface RatePoint {
  t: number;
  down: number;
  up: number;
}

export interface Stats {
  download: number;
  upload: number;
  history: RatePoint[];
}

const emptyStats: Stats = { download: 0, upload: 0, history: [] };

export function useStats(connected: boolean): Stats {
  const [stats, setStats] = useState<Stats>(emptyStats);

  const fetchStats = useCallback(async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/stats");
      const data = await res.json();
      setStats(data);
    } catch {
      // ignore fetch errors
    }
  }, []);

  useEffect(() => {
    if (!connected) {
      setStats(emptyStats);
      return;
    }
    fetchStats();
    const interval = setInterval(fetchStats, 1000);
    return () => clearInterval(interval);
  }, [connected, fetchStats]);

  return stats;
}
```

- [ ] **Step 2: Create SpeedGraph component**

```tsx
// client/src/renderer/components/SpeedGraph.tsx
import React from "react";

interface RatePoint {
  t: number;
  down: number;
  up: number;
}

interface Props {
  download: number;
  upload: number;
  history: RatePoint[];
}

function formatSpeed(bps: number): string {
  if (bps < 1024) return `${bps} B/s`;
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
}

export function SpeedGraph({ download, upload, history }: Props) {
  const width = 200;
  const height = 30;

  const maxVal = Math.max(1, ...history.map((p) => Math.max(p.down, p.up)));

  const toPoints = (getter: (p: RatePoint) => number): string => {
    if (history.length < 2) return "";
    const step = width / (history.length - 1);
    return history
      .map((p, i) => `${i * step},${height - (getter(p) / maxVal) * (height - 2)}`)
      .join(" ");
  };

  return (
    <div
      style={{
        background: "#12122a",
        borderRadius: 6,
        padding: 8,
        marginBottom: 10,
      }}
    >
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          fontSize: 9,
          color: "#888",
          marginBottom: 4,
        }}
      >
        <span style={{ color: "#4ade80" }}>↓ {formatSpeed(download)}</span>
        <span style={{ color: "#60a5fa" }}>↑ {formatSpeed(upload)}</span>
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} style={{ width: "100%", height: 28 }}>
        {history.length > 1 && (
          <>
            <polyline
              points={toPoints((p) => p.down)}
              fill="none"
              stroke="#4ade80"
              strokeWidth="1.5"
            />
            <polyline
              points={toPoints((p) => p.up)}
              fill="none"
              stroke="#60a5fa"
              strokeWidth="1.5"
            />
          </>
        )}
      </svg>
    </div>
  );
}
```

- [ ] **Step 3: Integrate SpeedGraph in App.tsx**

Add imports at the top of `App.tsx`:
```typescript
import { useStats } from "./hooks/useStats";
import { SpeedGraph } from "./components/SpeedGraph";
```

Add the hook call near the other hooks (e.g. after useDaemon):
```typescript
const stats = useStats(isConnected);
```

Where `isConnected` is the boolean derived from the current connection status (SOCKS or TUN connected).

Insert `SpeedGraph` between the `StatusBar` and the connect/mode area (between line ~189 and line ~191):
```tsx
{isConnected && (
  <SpeedGraph
    download={stats.download}
    upload={stats.upload}
    history={stats.history}
  />
)}
```

- [ ] **Step 4: Verify client builds**

Run: `cd client && npm run build`
Expected: Build succeeds with no type errors

- [ ] **Step 5: Commit**

```bash
git add client/src/renderer/hooks/useStats.ts client/src/renderer/components/SpeedGraph.tsx client/src/renderer/App.tsx
git commit -m "feat: add speed graph sparkline to client UI"
```

---

### Task 7: Admin UI Device Cards

**Files:**
- Modify: `server/admin-ui/src/lib/api.ts`
- Modify: `server/admin-ui/src/lib/format.ts`
- Modify: `server/admin-ui/src/pages/Dashboard.tsx`

- [ ] **Step 1: Add DeviceRate type and rate() to api.ts**

Append to `server/admin-ui/src/lib/api.ts`:

```typescript
export interface DeviceRate {
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
  return get("/admin/api/stats/rate");
}
```

- [ ] **Step 2: Add formatSpeed to format.ts**

Append to `server/admin-ui/src/lib/format.ts`:

```typescript
export function formatSpeed(bytesPerSec: number): string {
  if (bytesPerSec < 1024) return `${bytesPerSec} B/s`;
  if (bytesPerSec < 1024 * 1024) return `${(bytesPerSec / 1024).toFixed(1)} KB/s`;
  if (bytesPerSec < 1024 * 1024 * 1024) return `${(bytesPerSec / (1024 * 1024)).toFixed(1)} MB/s`;
  return `${(bytesPerSec / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
}
```

- [ ] **Step 3: Update Dashboard.tsx with device cards**

Replace the active connections table section in `Dashboard.tsx` with device rate cards.

Add imports:
```typescript
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, Tooltip } from "recharts";
import * as api from "../lib/api";
import { formatBytes, formatSpeed } from "../lib/format";
import type { DeviceRate } from "../lib/api";
```

Add state for rates:
```typescript
const [rates, setRates] = useState<DeviceRate[]>([]);
```

Add polling (inside the existing useEffect or a new one):
```typescript
useEffect(() => {
  const load = () => api.rate().then(setRates).catch(() => {});
  load();
  const interval = setInterval(load, 3000);
  return () => clearInterval(interval);
}, []);
```

Replace the active connections table with device cards:
```tsx
<h3>Active Devices</h3>
{rates.length === 0 ? (
  <p style={{ color: "#888" }}>No active connections</p>
) : (
  <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
    {rates
      .sort((a, b) => b.download + b.upload - (a.download + a.upload))
      .map((device) => (
        <div
          key={device.device_id}
          style={{
            border: "1px solid #e5e7eb",
            borderRadius: 8,
            padding: 16,
          }}
        >
          <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 8 }}>
            <div>
              <strong>{device.device_name}</strong>
              <span style={{ color: "#888", marginLeft: 8, fontSize: 13 }}>
                {device.user_name}
              </span>
            </div>
            <span style={{ color: "#888", fontSize: 13 }}>
              {formatBytes(device.total_bytes)} total · {device.connections} conn
            </span>
          </div>
          <div style={{ display: "flex", gap: 16, marginBottom: 8, fontSize: 14 }}>
            <span style={{ color: "#16a34a" }}>↓ {formatSpeed(device.download)}</span>
            <span style={{ color: "#2563eb" }}>↑ {formatSpeed(device.upload)}</span>
          </div>
          <ResponsiveContainer width="100%" height={80}>
            <LineChart data={device.history}>
              <XAxis dataKey="t" hide />
              <YAxis hide />
              <Tooltip
                formatter={(value: number, name: string) =>
                  [formatSpeed(value), name === "down" ? "Download" : "Upload"]
                }
                labelFormatter={() => ""}
              />
              <Line
                type="monotone"
                dataKey="down"
                stroke="#16a34a"
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
              <Line
                type="monotone"
                dataKey="up"
                stroke="#2563eb"
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ))}
  </div>
)}
```

- [ ] **Step 4: Verify admin UI builds**

Run: `cd server/admin-ui && npm run build`
Expected: Build succeeds

- [ ] **Step 5: Commit**

```bash
git add server/admin-ui/src/
git commit -m "feat: add real-time device speed cards to admin dashboard"
```

---

### Task 8: Integration Smoke Test

- [ ] **Step 1: Run all Go tests**

Run: `make test`
Expected: All tests pass across pkg, daemon, server, test modules

- [ ] **Step 2: Build all binaries**

Run: `make build-server && make build-daemon`
Expected: Both build successfully

- [ ] **Step 3: Build client**

Run: `make build-client`
Expected: Electron app builds with bundled daemon

- [ ] **Step 4: Final commit if any fixups needed**

```bash
git add -A
git commit -m "fix: address integration issues from traffic graphs"
```

Only if there were fixups. Skip if everything passed clean.
