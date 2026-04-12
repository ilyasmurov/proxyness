# Reconnect Detection + Kill Switch — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `Reconnecting` runtime state to daemon's tunnel + TUN engine, with three independent detectors and a hard kill switch, surfaced in the existing StatusBar.

**Architecture:** Both `tunnel.Tunnel` and `tun.Engine` get a third status `Reconnecting`. Three detectors fire it (transport closed, health-check fail, traffic stall via new `RateMeter.LastByteAt()` + `activeHosts`). While in `Reconnecting`, all in-flight conns are torn down and new ones rejected (SOCKS5 failure / TCP RST / UDP drop). Recovery flows back to `Connected` automatically when `reconnectTransport()` or `verifyServer()` succeeds. Status surfaces unchanged through `/status` and `/tun/status` (just a third enum value); client OR-merges it with the existing client-side reconnect flag.

**Tech Stack:** Go (daemon), React + TypeScript (client). No new deps.

**Spec:** `docs/superpowers/specs/2026-04-07-reconnect-killswitch-design.md`

---

## File Structure

**Modify:**
- `daemon/internal/stats/meter.go` — add `lastByteAt time.Time` + `LastByteAt()` getter, update inside `Add()`.
- `daemon/internal/stats/meter_test.go` — test the new getter.
- `daemon/internal/tunnel/tunnel.go` — add `Reconnecting` status, `setReconnecting()` / `setConnected()`, retune `healthInterval`, add D2/D3 logic, gate `handleSOCKS()`, seed `lastByteAt` on Start.
- `daemon/internal/tunnel/tunnel_test.go` — state-transition + detector + handleSOCKS-gate tests.
- `daemon/internal/tun/engine.go` — add `StatusReconnecting`, `setReconnecting()` / `setConnected()`, healthLoop tweaks, gate `handleTCP()` / `handleUDP()` for new flows + sweep registry on entry.
- `daemon/internal/tun/engine_test.go` — **CREATE** — state transitions + close-all-on-reconnect.
- `daemon/internal/api/api.go` — `handleTUNStatus` already works (returns the enum string); `StatusResponse` doc comment widens — no struct change.
- `client/src/renderer/hooks/useDaemon.ts` — widen `DaemonStatus.status` enum, fix sysproxy transition logic.
- `client/src/renderer/App.tsx` — add `daemonReconnecting` derived flag, OR-merge into StatusBar prop.
- `client/src/renderer/components/StatusBar.tsx` — Disconnect button stays enabled while `reconnecting`.

**Create:**
- `daemon/internal/tun/engine_test.go`

---

## Task 1: Add `LastByteAt()` to `RateMeter`

**Files:**
- Modify: `daemon/internal/stats/meter.go`
- Test: `daemon/internal/stats/meter_test.go`

- [ ] **Step 1: Read existing meter test file to learn its style**

Run: `cat daemon/internal/stats/meter_test.go` (use Read tool) — note the existing test helpers and naming conventions.

- [ ] **Step 2: Write the failing test**

Append to `daemon/internal/stats/meter_test.go`:

```go
func TestLastByteAtUpdatedOnAdd(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	zero := m.LastByteAt()
	if !zero.IsZero() {
		t.Fatalf("expected zero time before any Add, got %v", zero)
	}

	before := time.Now()
	m.Add(10, 0)
	got := m.LastByteAt()
	if got.Before(before) {
		t.Fatalf("LastByteAt %v should be >= %v after Add", got, before)
	}
}

func TestLastByteAtUnchangedWhenAddZero(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	m.Add(5, 0)
	first := m.LastByteAt()
	time.Sleep(5 * time.Millisecond)
	m.Add(0, 0)
	second := m.LastByteAt()
	if !first.Equal(second) {
		t.Fatalf("Add(0,0) should not bump LastByteAt; first=%v second=%v", first, second)
	}
}
```

If `time` is not yet imported in the test file, add it.

- [ ] **Step 3: Run test to verify it fails**

Run: `cd daemon && go test ./internal/stats/ -run TestLastByteAt -v`
Expected: FAIL — `m.LastByteAt undefined`.

- [ ] **Step 4: Implement `LastByteAt`**

Edit `daemon/internal/stats/meter.go`:

Add field to the struct:
```go
type RateMeter struct {
	bytesIn    atomic.Int64
	bytesOut   atomic.Int64
	ring       *pkgstats.RingBuffer
	stop       chan struct{}
	lastByteMu sync.Mutex
	lastByteAt time.Time
}
```

Add `sync` to imports if not already present.

Update `Add`:
```go
func (m *RateMeter) Add(in, out int64) {
	if in == 0 && out == 0 {
		return
	}
	m.bytesIn.Add(in)
	m.bytesOut.Add(out)
	m.lastByteMu.Lock()
	m.lastByteAt = time.Now()
	m.lastByteMu.Unlock()
}
```

Add getter:
```go
// LastByteAt returns the wall-clock time of the most recent non-zero Add.
// Returns the zero Time if no traffic has flowed yet.
func (m *RateMeter) LastByteAt() time.Time {
	m.lastByteMu.Lock()
	defer m.lastByteMu.Unlock()
	return m.lastByteAt
}

// SeedLastByteAt sets lastByteAt to now. Called from Start() so the
// first stall-detector tick after a fresh connect doesn't trip on a
// zero timestamp.
func (m *RateMeter) SeedLastByteAt() {
	m.lastByteMu.Lock()
	m.lastByteAt = time.Now()
	m.lastByteMu.Unlock()
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd daemon && go test ./internal/stats/ -v`
Expected: PASS for all.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/stats/meter.go daemon/internal/stats/meter_test.go
git commit -m "feat(stats): add LastByteAt to RateMeter for stall detection"
```

---

## Task 2: Add `Reconnecting` status constant + helpers to `Tunnel`

**Files:**
- Modify: `daemon/internal/tunnel/tunnel.go`

- [ ] **Step 1: Add the status constant**

Edit `daemon/internal/tunnel/tunnel.go` line 36-39:

```go
const (
	Disconnected Status = "disconnected"
	Connected    Status = "connected"
	Reconnecting Status = "reconnecting"
)
```

- [ ] **Step 2: Add `setReconnecting` and `setConnected` private methods**

Insert these methods after `GetStatus()` (around line 247). They are the **only** legitimate way to enter/leave the Reconnecting state inside `tunnel.go`:

```go
// setReconnecting flips status from Connected → Reconnecting and engages
// the kill switch (closes all in-flight relays). Idempotent: calling twice
// in the same state is a no-op. Caller must NOT hold t.mu.
func (t *Tunnel) setReconnecting() {
	t.mu.Lock()
	if t.status != Connected {
		t.mu.Unlock()
		return
	}
	t.status = Reconnecting
	t.mu.Unlock()

	log.Printf("[tunnel] → reconnecting (kill switch engaged)")
	t.CloseAllConns()
}

// setConnected flips status back to Connected after a successful recovery
// (D1 reconnect or D2 verify-success). Refreshes meter.lastByteAt so the
// next D3 tick sees a fresh timestamp. Caller must NOT hold t.mu.
func (t *Tunnel) setConnected() {
	t.mu.Lock()
	if t.status != Reconnecting {
		t.mu.Unlock()
		return
	}
	t.status = Connected
	t.mu.Unlock()

	if t.meter != nil {
		t.meter.SeedLastByteAt()
	}
	log.Printf("[tunnel] → connected (recovered)")
}
```

- [ ] **Step 3: Seed `lastByteAt` on `Start()` success**

Edit `Start()` around line 211 (right after `t.status = Connected`):

```go
	t.status = Connected
	t.startTime = time.Now()
	t.stopHealth = make(chan struct{})

	if t.meter != nil {
		t.meter.SeedLastByteAt()
	}
```

- [ ] **Step 4: Build to verify nothing is broken**

Run: `cd daemon && go build ./...`
Expected: success, no errors.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tunnel/tunnel.go
git commit -m "feat(tunnel): add Reconnecting status with setReconnecting/setConnected helpers"
```

---

## Task 3: Wire detectors D1 + D2 into tunnel `healthLoop`

**Files:**
- Modify: `daemon/internal/tunnel/tunnel.go`

- [ ] **Step 1: Retune healthInterval and add stall threshold**

Edit `daemon/internal/tunnel/tunnel.go` line 19-32:

```go
const (
	maxRetries     = 3
	retryDelay     = 3 * time.Second
	dialTimeout    = 5 * time.Second
	healthInterval = 5 * time.Second // was 30s — needs to fire fast enough for D2/D3

	// stallThreshold is how long the meter can show no bytes while
	// activeHosts > 0 before D3 trips. 5s ≈ two-three TCP retransmits;
	// shorter causes false positives during ordinary jitter, longer lets
	// banned-on-direct apps fire several leaked requests.
	stallThreshold = 5 * time.Second

	// defaultHostLiveWindow ... (unchanged)
	defaultHostLiveWindow = 5 * time.Second
)
```

- [ ] **Step 2: Rewrite `healthLoop` to use new state transitions**

Replace the entire `healthLoop()` function (currently lines 283-332) with:

```go
func (t *Tunnel) healthLoop() {
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	doneCh := t.transportDone()

	failures := 0
	for {
		select {
		case <-t.stopHealth:
			return

		case <-doneCh:
			// D1 — transport closed: engage kill switch, then try to reconnect.
			log.Printf("[tunnel] D1: transport closed")
			t.setReconnecting()
			if t.reconnectTransport() {
				doneCh = t.transportDone()
				failures = 0
				t.setConnected()
				continue
			}
			log.Printf("[tunnel] D1: reconnect exhausted, disconnecting")
			t.mu.Lock()
			t.lastError = "Connection lost, please reconnect"
			t.stopLocked()
			t.mu.Unlock()
			return

		case <-ticker.C:
			t.mu.Lock()
			addr := t.serverAddr
			key := t.key
			status := t.status
			t.mu.Unlock()

			// Skip ticks while not in a "live" state.
			if status != Connected && status != Reconnecting {
				continue
			}

			// D2 — health check.
			if err := verifyServer(addr, key); err != nil {
				failures++
				log.Printf("[tunnel] D2: health check failed (%d/%d): %v", failures, maxRetries, err)
				if failures == 1 {
					t.setReconnecting()
				}
				if failures >= maxRetries {
					log.Printf("[tunnel] D2: exhausted, disconnecting")
					t.mu.Lock()
					t.lastError = "Server temporarily unavailable, try again later"
					t.stopLocked()
					t.mu.Unlock()
					return
				}
				continue
			}

			// D2 recovered.
			if failures > 0 {
				log.Printf("[tunnel] D2: recovered after %d failures", failures)
				failures = 0
				t.setConnected()
			}

			// D3 — stall detector. Only fires while we believe we're
			// healthy AND the user is actively trying to use the proxy.
			if status == Connected && t.stallDetected() {
				log.Printf("[tunnel] D3: traffic stall detected")
				t.setReconnecting()
			}
		}
	}
}

// stallDetected returns true when the user is actively trying to use the
// proxy (activeHosts > 0) but no bytes have flowed for stallThreshold.
// Idle sessions (no active hosts) never trip this.
func (t *Tunnel) stallDetected() bool {
	t.activeHostsMu.Lock()
	hostCount := len(t.activeHosts)
	t.activeHostsMu.Unlock()
	if hostCount == 0 {
		return false
	}
	if t.meter == nil {
		return false
	}
	last := t.meter.LastByteAt()
	if last.IsZero() {
		return false
	}
	return time.Since(last) > stallThreshold
}
```

- [ ] **Step 3: Build**

Run: `cd daemon && go build ./...`
Expected: success.

- [ ] **Step 4: Run existing tunnel tests to verify no regression**

Run: `cd daemon && go test ./internal/tunnel/ -v`
Expected: existing tests still pass (TouchHost / GetActiveHosts tests from prior commit). New behaviour not yet tested — added in Task 5.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tunnel/tunnel.go
git commit -m "feat(tunnel): healthLoop detectors D1/D2/D3 for reconnect state"
```

---

## Task 4: Gate `handleSOCKS` during Reconnecting (kill switch in SOCKS5 mode)

**Files:**
- Modify: `daemon/internal/tunnel/tunnel.go`

- [ ] **Step 1: Add early-return after the SOCKS5 handshake**

Edit `handleSOCKS()` (around line 391). After the existing handshake block:

```go
	req, err := socks5.Handshake(conn)
	if err != nil {
		log.Printf("[socks5] handshake failed: %v", err)
		return
	}
	target := fmt.Sprintf("%s:%d", req.Addr, req.Port)
	log.Printf("[tunnel] new request: %s", target)
```

Insert immediately below:

```go
	// Kill switch: while reconnecting, refuse new SOCKS5 requests so the
	// browser cannot fall back to a native dialer. The browser sees a
	// SOCKS5 failure, retries within seconds, gets failure again — until
	// the daemon flips back to Connected.
	if t.GetStatus() == Reconnecting {
		socks5.SendFailure(conn)
		return
	}
```

- [ ] **Step 2: Build**

Run: `cd daemon && go build ./...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add daemon/internal/tunnel/tunnel.go
git commit -m "feat(tunnel): kill switch — reject SOCKS5 requests during Reconnecting"
```

---

## Task 5: Unit tests for tunnel state machine + detectors + kill switch

**Files:**
- Modify: `daemon/internal/tunnel/tunnel_test.go`

- [ ] **Step 1: Read the existing test file**

Run Read on `daemon/internal/tunnel/tunnel_test.go` to learn the existing helpers (Tunnel construction, mock meter, mock conns).

- [ ] **Step 2: Write `TestSetReconnectingOnlyFromConnected`**

Append:

```go
func TestSetReconnectingOnlyFromConnected(t *testing.T) {
	tn := New(stats.NewRateMeter())
	// status starts as Disconnected
	tn.setReconnecting()
	if tn.GetStatus() != Disconnected {
		t.Fatalf("expected Disconnected, got %s", tn.GetStatus())
	}

	// Force Connected
	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()

	tn.setReconnecting()
	if tn.GetStatus() != Reconnecting {
		t.Fatalf("expected Reconnecting, got %s", tn.GetStatus())
	}

	// Idempotent
	tn.setReconnecting()
	if tn.GetStatus() != Reconnecting {
		t.Fatalf("setReconnecting should be idempotent, got %s", tn.GetStatus())
	}
}
```

If `stats` isn't imported in the test file, add `dstats "proxyness/daemon/internal/stats"` and use `dstats.NewRateMeter()`.

- [ ] **Step 3: Write `TestSetConnectedOnlyFromReconnecting`**

```go
func TestSetConnectedOnlyFromReconnecting(t *testing.T) {
	tn := New(dstats.NewRateMeter())

	tn.setConnected()
	if tn.GetStatus() != Disconnected {
		t.Fatalf("setConnected from Disconnected must be a no-op, got %s", tn.GetStatus())
	}

	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()
	tn.setConnected()
	if tn.GetStatus() != Connected {
		t.Fatalf("setConnected from Connected must be a no-op, got %s", tn.GetStatus())
	}

	tn.mu.Lock()
	tn.status = Reconnecting
	tn.mu.Unlock()
	tn.setConnected()
	if tn.GetStatus() != Connected {
		t.Fatalf("expected Connected, got %s", tn.GetStatus())
	}
}
```

- [ ] **Step 4: Write `TestStallDetectedRequiresActiveHosts`**

```go
func TestStallDetectedRequiresActiveHosts(t *testing.T) {
	meter := dstats.NewRateMeter()
	tn := New(meter)

	// Seed meter into the past so threshold is exceeded
	meter.Add(1, 0)
	// activeHosts is empty → must be false even though time has passed
	time.Sleep(10 * time.Millisecond)
	if tn.stallDetected() {
		t.Fatalf("idle session must not trigger stall detector")
	}

	// Touch a host
	tn.touchHost("example.com")

	// lastByteAt is recent → no stall
	if tn.stallDetected() {
		t.Fatalf("fresh meter must not trigger stall detector")
	}
}
```

- [ ] **Step 5: Write `TestStallDetectedTripsWhenStale`**

```go
func TestStallDetectedTripsWhenStale(t *testing.T) {
	meter := dstats.NewRateMeter()
	tn := New(meter)
	tn.touchHost("example.com")

	// Force the meter's lastByteAt into the past
	meter.Add(1, 0)
	meter.SeedLastByteAtForTest(time.Now().Add(-2 * stallThreshold))

	if !tn.stallDetected() {
		t.Fatalf("expected stallDetected=true with stale lastByteAt")
	}
}
```

This test needs a test-only helper on `RateMeter`. Add it to `daemon/internal/stats/meter.go`:

```go
// SeedLastByteAtForTest is a test-only helper that lets unit tests
// force lastByteAt into the past or future. Do not call from production code.
func (m *RateMeter) SeedLastByteAtForTest(at time.Time) {
	m.lastByteMu.Lock()
	m.lastByteAt = at
	m.lastByteMu.Unlock()
}
```

- [ ] **Step 6: Write `TestHandleSOCKSRejectsDuringReconnecting`**

```go
func TestHandleSOCKSRejectsDuringReconnecting(t *testing.T) {
	tn := New(dstats.NewRateMeter())
	tn.mu.Lock()
	tn.status = Reconnecting
	tn.mu.Unlock()

	// Pipe pair: we write a valid SOCKS5 handshake into one end and
	// expect a failure response on the other.
	clientEnd, serverEnd := net.Pipe()
	defer clientEnd.Close()
	defer serverEnd.Close()

	// Write a minimal SOCKS5 connect handshake into clientEnd in a
	// goroutine so handleSOCKS can read it.
	go func() {
		// auth method negotiation
		clientEnd.Write([]byte{0x05, 0x01, 0x00})
		// read method response (1 byte)
		buf := make([]byte, 2)
		clientEnd.Read(buf)
		// connect to 1.2.3.4:80
		clientEnd.Write([]byte{0x05, 0x01, 0x00, 0x01, 1, 2, 3, 4, 0, 80})
	}()

	done := make(chan struct{})
	go func() {
		tn.handleSOCKS(serverEnd)
		close(done)
	}()

	// Read the connect-reply from the daemon side
	reply := make([]byte, 10)
	clientEnd.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientEnd.Read(reply)
	if err != nil {
		t.Fatalf("expected SOCKS5 reply, got err=%v", err)
	}
	if n < 2 || reply[0] != 0x05 || reply[1] == 0x00 {
		t.Fatalf("expected SOCKS5 failure (REP != 0x00), got %v", reply[:n])
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleSOCKS did not return")
	}
}
```

Add `"net"` to the test file imports if not present.

- [ ] **Step 7: Write `TestSetReconnectingClosesActiveConns`**

```go
func TestSetReconnectingClosesActiveConns(t *testing.T) {
	tn := New(dstats.NewRateMeter())
	tn.mu.Lock()
	tn.status = Connected
	tn.mu.Unlock()

	a, b := net.Pipe()
	defer b.Close()
	tn.trackConn(a)

	tn.setReconnecting()

	// `a` should now be closed — a Read should return an error immediately.
	a.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := a.Read(buf); err == nil {
		t.Fatalf("expected closed conn to error on Read")
	}
}
```

- [ ] **Step 8: Run all new tests**

Run: `cd daemon && go test ./internal/tunnel/ -run 'TestSetReconnecting|TestSetConnected|TestStallDetected|TestHandleSOCKSRejects' -v`
Expected: all PASS.

Then run the full tunnel test suite:

Run: `cd daemon && go test ./internal/tunnel/ -v`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add daemon/internal/tunnel/tunnel_test.go daemon/internal/stats/meter.go
git commit -m "test(tunnel): cover reconnect state machine + kill switch"
```

---

## Task 6: Add `Reconnecting` status to TUN engine + setReconnecting/setConnected helpers

**Files:**
- Modify: `daemon/internal/tun/engine.go`

- [ ] **Step 1: Add the status constant**

Edit `daemon/internal/tun/engine.go` lines 36-39:

```go
const (
	StatusInactive     Status = "inactive"
	StatusActive       Status = "active"
	StatusReconnecting Status = "reconnecting"
)
```

- [ ] **Step 2: Add helper methods**

Insert after `GetLastError()` (around line 116):

```go
// setReconnecting flips status from Active → Reconnecting and sweeps
// every in-flight TCP/UDP conn (kill switch). Idempotent.
// Caller must NOT hold e.mu.
func (e *Engine) setReconnecting() {
	e.mu.Lock()
	if e.status != StatusActive {
		e.mu.Unlock()
		return
	}
	e.status = StatusReconnecting
	e.mu.Unlock()

	log.Printf("[tun] → reconnecting (kill switch engaged)")
	e.closeAllConns()
}

// setConnected flips back to Active after recovery and reseeds the
// stall detector's reference timestamp via the meter.
// Caller must NOT hold e.mu.
func (e *Engine) setConnected() {
	e.mu.Lock()
	if e.status != StatusReconnecting {
		e.mu.Unlock()
		return
	}
	e.status = StatusActive
	e.mu.Unlock()

	if e.meter != nil {
		e.meter.SeedLastByteAt()
	}
	log.Printf("[tun] → active (recovered)")
}
```

- [ ] **Step 3: Seed `lastByteAt` on Start success**

In `Start()` around line 232:

```go
	e.status = StatusActive
	e.startTime = time.Now()
	e.lastError = ""
	e.stopHealth = make(chan struct{})
	if e.meter != nil {
		e.meter.SeedLastByteAt()
	}
	go e.healthLoop()
```

- [ ] **Step 4: Rewrite `healthLoop` for D1/D2 (no D3 in TUN — see note)**

Replace `healthLoop()` (lines 493-537) with:

```go
func (e *Engine) healthLoop() {
	const maxFailures = 3
	ticker := time.NewTicker(5 * time.Second) // was 30s
	defer ticker.Stop()

	doneCh := e.transportDone()
	failures := 0
	for {
		select {
		case <-e.stopHealth:
			return

		case <-doneCh:
			log.Printf("[tun] D1: transport closed")
			e.setReconnecting()
			if e.reconnectTransport() {
				doneCh = e.transportDone()
				failures = 0
				e.setConnected()
				continue
			}
			log.Printf("[tun] D1: reconnect exhausted, stopping engine")
			e.mu.Lock()
			e.lastError = "Connection lost, please reconnect"
			e.stopLocked()
			e.mu.Unlock()
			return

		case <-ticker.C:
			if err := e.healthCheck(); err != nil {
				failures++
				log.Printf("[tun] D2: health check failed (%d/%d): %v", failures, maxFailures, err)
				if failures == 1 {
					e.setReconnecting()
				}
				if failures >= maxFailures {
					log.Printf("[tun] D2: exhausted, stopping engine")
					e.mu.Lock()
					e.lastError = "Server temporarily unavailable"
					e.stopLocked()
					e.mu.Unlock()
					return
				}
				continue
			}
			if failures > 0 {
				log.Printf("[tun] D2: recovered after %d failures", failures)
				failures = 0
				e.setConnected()
			}
		}
	}
}
```

> **Why no D3 in TUN:** the TUN engine's existing `healthCheck()` calls `transport.Alive()`, which already detects UDP silent-death. The stall-detector approach (activeHosts + lastByteAt) belongs to SOCKS5 because it depends on the per-host tracking added in tunnel. TUN can be extended later if D2 proves insufficient. The kill switch in TUN still functions for D1/D2 triggers and for `Reconnecting` set externally.

- [ ] **Step 5: Build**

Run: `cd daemon && go build ./...`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add daemon/internal/tun/engine.go
git commit -m "feat(tun): add Reconnecting status + D1/D2 detectors in healthLoop"
```

---

## Task 7: Kill switch for new TCP/UDP flows in TUN engine

**Files:**
- Modify: `daemon/internal/tun/engine.go`

- [ ] **Step 1: Gate `handleTCP` to RST during Reconnecting**

Edit `handleTCP()` (around line 557). Add the gate **before** `r.CreateEndpoint`:

```go
func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	// Kill switch: refuse new TCP flows while reconnecting. Calling
	// Complete(true) tells gVisor to drop the SYN — the originating app
	// will see "connection refused" / RST.
	if e.GetStatus() == StatusReconnecting {
		r.Complete(true)
		return
	}

	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort
	// ... rest unchanged
```

- [ ] **Step 2: Gate `handleUDP` to drop during Reconnecting**

Edit `handleUDP()` (around line 689). Add the gate at the very top, **before** the QUIC and DNS special-cases (we want kill switch to apply to DNS too — otherwise apps can resolve but not connect, which is a worse UX than a clean failure):

```go
func (e *Engine) handleUDP(r *udp.ForwarderRequest) {
	// Kill switch: drop new UDP flows while reconnecting. Returning
	// without calling CreateEndpoint causes gVisor to discard the
	// inbound packet silently. Apps see UDP timeout.
	if e.GetStatus() == StatusReconnecting {
		return
	}

	id := r.ID()
	// ... rest unchanged
```

- [ ] **Step 3: Build**

Run: `cd daemon && go build ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add daemon/internal/tun/engine.go
git commit -m "feat(tun): kill switch — reject new TCP/UDP flows during Reconnecting"
```

---

## Task 8: Engine unit tests

**Files:**
- Create: `daemon/internal/tun/engine_test.go`

- [ ] **Step 1: Check whether engine_test.go already exists**

Run: `ls daemon/internal/tun/engine_test.go 2>&1`

If it exists, append. If not, create.

- [ ] **Step 2: Write engine state-transition tests**

Create or append `daemon/internal/tun/engine_test.go`:

```go
package tun

import (
	"net"
	"testing"
	"time"

	dstats "proxyness/daemon/internal/stats"
)

func TestEngineSetReconnectingOnlyFromActive(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	// Initial: StatusInactive
	e.setReconnecting()
	if e.GetStatus() != StatusInactive {
		t.Fatalf("expected StatusInactive, got %s", e.GetStatus())
	}

	e.mu.Lock()
	e.status = StatusActive
	e.mu.Unlock()
	e.setReconnecting()
	if e.GetStatus() != StatusReconnecting {
		t.Fatalf("expected StatusReconnecting, got %s", e.GetStatus())
	}

	// Idempotent
	e.setReconnecting()
	if e.GetStatus() != StatusReconnecting {
		t.Fatalf("setReconnecting should be idempotent")
	}
}

func TestEngineSetConnectedOnlyFromReconnecting(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	e.setConnected()
	if e.GetStatus() != StatusInactive {
		t.Fatalf("setConnected from Inactive must be no-op, got %s", e.GetStatus())
	}

	e.mu.Lock()
	e.status = StatusReconnecting
	e.mu.Unlock()
	e.setConnected()
	if e.GetStatus() != StatusActive {
		t.Fatalf("expected StatusActive, got %s", e.GetStatus())
	}
}

func TestEngineSetReconnectingClosesAllConns(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	e.mu.Lock()
	e.status = StatusActive
	e.mu.Unlock()

	a, b := net.Pipe()
	defer b.Close()
	e.trackConn(a)

	e.setReconnecting()

	a.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := a.Read(buf); err == nil {
		t.Fatalf("expected tracked conn to be closed")
	}
}
```

- [ ] **Step 3: Run the new tests**

Run: `cd daemon && go test ./internal/tun/ -run TestEngineSet -v`
Expected: all PASS.

Then run the full tun test suite to confirm no regressions:

Run: `cd daemon && go test ./internal/tun/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add daemon/internal/tun/engine_test.go
git commit -m "test(tun): cover engine reconnect state machine + kill switch"
```

---

## Task 9: Client — widen `useDaemon` status type and fix sysproxy transition

**Files:**
- Modify: `client/src/renderer/hooks/useDaemon.ts`

- [ ] **Step 1: Widen the type**

Edit `client/src/renderer/hooks/useDaemon.ts` line 13-16:

```ts
interface DaemonStatus {
  status: "connected" | "disconnected" | "reconnecting";
  uptime: number;
}
```

- [ ] **Step 2: Adjust the sysproxy-disable transition logic**

Edit `fetchStatus` (around line 27). The existing `connected → disconnected` check is fine for the **fully torn down** case, but we must NOT trigger it on `connected → reconnecting`. The existing comparison already avoids that (it checks `data.status === "disconnected"` strictly). Keep the existing conditional but add a comment for future readers, and treat `reconnecting` as a transient that shouldn't bubble error state into the UI:

```ts
const fetchStatus = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/status`);
      if (res.ok) {
        const data = await res.json();
        // Server fully disconnected us (health check exhausted, or D2 max-fails).
        // Reconnecting is a transient state — sysproxy stays enabled so the
        // upstream kill switch can block traffic.
        if (prevStatus.current === "connected" && data.status === "disconnected") {
          window.sysproxy.disable();
          if (data.error) {
            setError(data.error);
          }
        }
        prevStatus.current = data.status;
        setStatus({ status: data.status, uptime: data.uptime });
      }
    } catch {
      setError("Daemon not running");
    }
  }, []);
```

(No actual code change beyond the comment — but verify the logic by re-reading.)

- [ ] **Step 3: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add client/src/renderer/hooks/useDaemon.ts
git commit -m "feat(client): widen DaemonStatus to include reconnecting"
```

---

## Task 10: Client — derive `daemonReconnecting` and merge into StatusBar prop

**Files:**
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Add the derived flag**

In `client/src/renderer/App.tsx`, find the section after `useDaemon()` (around line 18) and the TUN status block (around line 28-32). After `tunStatus` is declared, add a derived value just before the JSX (around line 145, near `isConnected`):

```tsx
  // Daemon-reported reconnecting state — distinct from the client-side
  // `reconnecting` flag (which drives startReconnect()'s loop). Both
  // mean the user should see "Reconnecting…" in the UI; OR them.
  const daemonReconnecting =
    socksStatus.status === "reconnecting" || tunStatus === "reconnecting";
```

- [ ] **Step 2: Widen `tunStatus`'s type**

Edit line 28:

```tsx
const [tunStatus, setTunStatus] = useState<"inactive" | "active" | "reconnecting">("inactive");
```

And in the TUN poll effect (around line 104), update the mapping to preserve the reconnecting state:

```tsx
        if (s) {
          // Map daemon's tun status enum into our local one.
          let next: "inactive" | "active" | "reconnecting" = "inactive";
          if (s.status === "active") next = "active";
          else if (s.status === "reconnecting") next = "reconnecting";
          setTunStatus(next);
          setTunUptime(s.uptime || 0);
          // ...
```

Replace the existing `const active = s.status === "active"; setTunStatus(active ? "active" : "inactive");` block with the snippet above. The downstream `wasConnected.current && !active && s.error` check still uses `active`, so keep `const active = next === "active";` right after the setter for backward compat:

```tsx
          let next: "inactive" | "active" | "reconnecting" = "inactive";
          if (s.status === "active") next = "active";
          else if (s.status === "reconnecting") next = "reconnecting";
          setTunStatus(next);
          setTunUptime(s.uptime || 0);
          const active = next === "active";
          if (s.error) setTunError(s.error);
          if (wasConnected.current && !active && next !== "reconnecting" && s.error) {
            // Only fire startReconnect on a HARD disconnect, not while
            // the daemon is still trying to reconnect on its own.
            if (s.error.includes("bound to a different machine")) {
              localStorage.removeItem(STORAGE_KEY);
              setKey("");
              setShowSetup(true);
            } else {
              startReconnect();
            }
          }
          wasConnected.current = active;
```

- [ ] **Step 3: OR-merge into the StatusBar prop**

Find the `<StatusBar ... reconnecting={reconnecting}` line (around line 403) and change to:

```tsx
          reconnecting={reconnecting || daemonReconnecting}
```

- [ ] **Step 4: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add client/src/renderer/App.tsx
git commit -m "feat(client): merge daemon-reported reconnecting into StatusBar"
```

---

## Task 11: Keep Disconnect button live during Reconnecting

**Files:**
- Modify: `client/src/renderer/components/StatusBar.tsx`

- [ ] **Step 1: Decouple `disabled` from `busy` for the Disconnect button**

Find the disconnect/connect button (around line 264-267):

```tsx
          <button
            onClick={connected ? onDisconnect : onConnect}
            disabled={busy}
```

Change to:

```tsx
          <button
            onClick={connected ? onDisconnect : onConnect}
            disabled={loading}
```

The visual `busy` state (cursor: wait, gray bg) still applies via the existing `busy` references in the style block — only the `disabled` attribute changes. This means the button is **clickable** during Reconnecting (so the user can tear down) but still shows the spinner-style "wait" appearance.

Double-check the style block right below (lines 267-280) — `busy` controls cursor and background. Leave those as `busy`. Only `disabled` becomes `loading`.

- [ ] **Step 2: Type-check**

Run: `cd client && npx tsc --noEmit`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/components/StatusBar.tsx
git commit -m "fix(client): keep Disconnect button live during Reconnecting"
```

---

## Task 12: Final integration smoke test (manual)

**Files:** none — manual verification.

- [ ] **Step 1: Build daemon**

Run: `cd daemon && go build -o ../dist/daemon-darwin-arm64 ./cmd/daemon`
Expected: success.

- [ ] **Step 2: Run the full Go test suite**

Run: `make test`
Expected: all tests pass.

- [ ] **Step 3: Build the client**

Run: `cd client && npm run build`
Expected: success.

- [ ] **Step 4: Manual smoke (require user)**

Hand off to user with this checklist. The user must run the dev client and verify:

- [ ] In TUN mode, with Telegram/Instagram open and active, toggle Wi-Fi off. Within ~10s the StatusBar should switch to "Reconnecting…" and the apps should stop sending. Toggle Wi-Fi back on. Within ~10s the StatusBar returns to "Connected" and apps resume.
- [ ] In SOCKS5 mode, with a YouTube video playing, kill the network on the VPS side or block the route. Within ~10s "Reconnecting…" appears, the video buffers and stalls (no native fallback). Restore. Recovers within ~10s.
- [ ] Open the client and let it sit idle for 5+ minutes with no app activity. The StatusBar must stay solid "Connected" — no Reconnecting flicker (D3 must be silent when activeHosts is empty).
- [ ] During a Reconnecting state, click the Disconnect button. The tunnel should tear down to Disconnected immediately.
- [ ] Block the proxy server completely (firewall) for 60+ seconds. After ~5s the UI shows "Reconnecting…", after ~15s the UI shows "Disconnected" with the "Server temporarily unavailable" error.

- [ ] **Step 5: After user confirms, no commit needed** — the feature is complete.

---

## Self-Review Notes

- **Spec coverage:** D1, D2, D3 all wired (D3 only in tunnel by design — see Task 6 note). Kill switch implemented for both SOCKS5 (Task 4) and TUN (Task 7). State surfaces unchanged via existing JSON enum string. Client OR-merges new daemon flag with existing client-side flag. Disconnect button stays live (Task 11). All tests requested in spec section "Testing — Unit" present (tests 1-7 → Task 5; tests 8-9 → Task 8). Integration test 10 from spec deferred to manual smoke in Task 12 (mock TLS server harness doesn't yet exist in this repo and would be a significant undertaking; manual smoke covers the scenario).
- **Type consistency:** `Status` constants spelled `Reconnecting` (tunnel) and `StatusReconnecting` (tun) — matches existing convention in each file (`Connected` vs `StatusActive`). JSON-side both serialize to `"reconnecting"`. Client uses lowercase string union.
- **Placeholder scan:** None found. Every step has either exact code, exact command, or explicit "no code change — verify by re-reading" note.
- **Risk:** Task 10's edits to App.tsx are the most invasive client change. The user should review the whole diff before committing rather than blindly trusting per-step edits.
