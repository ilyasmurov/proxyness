package tunnel

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"proxyness/daemon/internal/socks5"
	dstats "proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/transport"
	"proxyness/pkg/machineid"
	"proxyness/pkg/proto"
)

const (
	maxRetries     = 3
	retryDelay     = 3 * time.Second
	dialTimeout    = 5 * time.Second
	healthInterval = 5 * time.Second // was 30s — needs to fire fast enough for D2/D3

	// stallThreshold is how long the meter can show no bytes while
	// activeHosts > 0 before D3 trips. D1 catches transport drops, D2
	// catches server failures — D3 is the last resort for "transport
	// looks alive but data stopped flowing". 30s is generous enough to
	// avoid false positives during idle browsing or brief lulls (new
	// SOCKS5 connects refresh activeHosts without flowing bytes yet)
	// while still catching genuinely stuck transports within a minute.
	stallThreshold = 30 * time.Second

	// defaultHostLiveWindow is how long a host stays "live" in
	// GetActiveHosts after the last byte flowed through its SOCKS5
	// relay. Browsers keep HTTP/2 connections idle in pools long after
	// a tab is closed, so a counter-based approach left LIVE indicators
	// stuck on. The window is short enough that the UI fades within a
	// few poll cycles, long enough to ride out brief lulls in traffic.
	defaultHostLiveWindow = 5 * time.Second
)

type Status string

const (
	Disconnected Status = "disconnected"
	Connected    Status = "connected"
	Reconnecting Status = "reconnecting"
)

// TransportFactory creates a new transport instance for (re)connection.
type TransportFactory func() transport.Transport

type Tunnel struct {
	mu               sync.Mutex
	status           Status
	serverAddr       string
	key              string
	listener         net.Listener
	startTime        time.Time
	stopHealth       chan struct{}
	lastError        string
	meter            *dstats.RateMeter
	transport        transport.Transport
	transportFactory TransportFactory
	machineID        [16]byte

	// Optional: called by waitForNetwork on each slow-poll tick to re-
	// install physical routes via the helper. Wired in daemon main to
	// engine.RefreshRoutes. Nil in tests — skipped silently in that case.
	refreshRoutesFn func() error

	connsMu sync.Mutex
	conns   map[uint64]net.Conn
	connSeq uint64

	// Active-site tracking: host → time of the last byte we relayed for
	// it. Fed from handleSOCKS (initial touch) and the relay byte
	// callback (refresh on every byte), filtered through hostLiveWindow
	// in GetActiveHosts so the UI LIVE indicators reflect *traffic*, not
	// merely an open TCP connection.
	activeHostsMu  sync.Mutex
	activeHosts    map[string]time.Time
	hostLiveWindow time.Duration
}

func New(meter *dstats.RateMeter) *Tunnel {
	return &Tunnel{
		status:         Disconnected,
		meter:          meter,
		conns:          make(map[uint64]net.Conn),
		activeHosts:    make(map[string]time.Time),
		hostLiveWindow: defaultHostLiveWindow,
	}
}

// touchHost records the moment we last saw activity for a SOCKS5
// destination host. Called once when the request arrives (so the tile
// lights up immediately) and again from the relay byte callback on every
// chunk that flows. GetActiveHosts treats hosts older than hostLiveWindow
// as stale, so the LIVE indicator fades shortly after traffic stops even
// if the underlying TCP connection lingers in the browser's HTTP/2 pool.
func (t *Tunnel) touchHost(host string) {
	if host == "" {
		return
	}
	t.activeHostsMu.Lock()
	t.activeHosts[host] = time.Now()
	t.activeHostsMu.Unlock()
}

// GetActiveHosts returns a snapshot of every host that saw traffic within
// the last hostLiveWindow. Stale entries are deleted from the underlying
// map during the sweep so it does not grow unbounded over a long session.
// Exposed via /tunnel/active-hosts for the UI LIVE indicators.
func (t *Tunnel) GetActiveHosts() []string {
	cutoff := time.Now().Add(-t.hostLiveWindow)
	t.activeHostsMu.Lock()
	defer t.activeHostsMu.Unlock()
	out := make([]string, 0, len(t.activeHosts))
	for h, last := range t.activeHosts {
		if last.Before(cutoff) {
			delete(t.activeHosts, h)
			continue
		}
		out = append(out, h)
	}
	return out
}

func (t *Tunnel) SetTransport(tr transport.Transport) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.transport = tr
}

func (t *Tunnel) SetTransportFactory(factory TransportFactory, machineID [16]byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.transportFactory = factory
	t.machineID = machineID
}

// SetRouteRefresher installs a callback invoked by waitForNetwork on
// each slow-poll tick. The daemon wires this to the tun engine's
// RefreshRoutes so ENETUNREACH stalls that a plain socket retry can't
// clear get a chance to unstick the kernel's neighbor cache via the
// helper. Safe to leave unset (tests).
func (t *Tunnel) SetRouteRefresher(fn func() error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshRoutesFn = fn
}

func (t *Tunnel) trackConn(c net.Conn) uint64 {
	t.connsMu.Lock()
	defer t.connsMu.Unlock()
	t.connSeq++
	id := t.connSeq
	t.conns[id] = c
	return id
}

func (t *Tunnel) untrackConn(id uint64) {
	t.connsMu.Lock()
	defer t.connsMu.Unlock()
	delete(t.conns, id)
}

// CloseAllConns closes all active SOCKS5 relay connections,
// forcing browsers to reconnect and re-evaluate the PAC file.
func (t *Tunnel) CloseAllConns() {
	t.connsMu.Lock()
	snapshot := make(map[uint64]net.Conn, len(t.conns))
	for k, v := range t.conns {
		snapshot[k] = v
	}
	t.connsMu.Unlock()

	for _, c := range snapshot {
		c.Close()
	}
	if len(snapshot) > 0 {
		log.Printf("[tunnel] closed %d connections after PAC update", len(snapshot))
	}
}

func (t *Tunnel) Start(listenAddr, serverAddr, key string) error {
	t.mu.Lock()
	if t.status != Disconnected {
		t.mu.Unlock()
		return fmt.Errorf("tunnel already %s", t.status)
	}
	t.lastError = ""
	tr := t.transport
	t.mu.Unlock()

	// If no transport is set, fall back to verifyServer for backward compat
	if tr == nil {
		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			if attempt > 1 {
				log.Printf("[tunnel] retry %d/%d in %s...", attempt, maxRetries, retryDelay)
				time.Sleep(retryDelay)
			}
			log.Printf("[tunnel] verifying connection to %s (attempt %d/%d)...", serverAddr, attempt, maxRetries)
			lastErr = verifyServer(serverAddr, key)
			if lastErr == nil {
				break
			}
			if strings.Contains(lastErr.Error(), "invalid key") {
				return lastErr
			}
			log.Printf("[tunnel] attempt %d failed: %v", attempt, lastErr)
		}
		if lastErr != nil {
			return fmt.Errorf("server temporarily unavailable, try again later")
		}
		log.Printf("[tunnel] key verified OK")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.status != Disconnected {
		return fmt.Errorf("tunnel already %s", t.status)
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	t.listener = ln
	t.serverAddr = serverAddr
	t.key = key
	t.status = Connected
	t.startTime = time.Now()
	t.stopHealth = make(chan struct{})

	if t.meter != nil {
		t.meter.SeedLastByteAt()
	}

	go t.acceptLoop(ln)
	go t.healthLoop()
	return nil
}

func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastError = ""
	t.stopLocked()
}

func (t *Tunnel) stopLocked() {
	if t.listener != nil {
		t.listener.Close()
		t.listener = nil
	}
	if t.stopHealth != nil {
		close(t.stopHealth)
		t.stopHealth = nil
	}
	if t.transport != nil {
		t.transport.Close()
		t.transport = nil
	}
	t.status = Disconnected
}

func (t *Tunnel) GetStatus() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

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

func (t *Tunnel) LastError() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastError
}

func (t *Tunnel) Uptime() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status != Connected {
		return 0
	}
	return int64(time.Since(t.startTime).Seconds())
}

// transportDone returns a channel that closes when the transport dies, or nil.
func (t *Tunnel) transportDone() <-chan struct{} {
	t.mu.Lock()
	tr := t.transport
	t.mu.Unlock()
	type doner interface {
		DoneChan() <-chan struct{}
	}
	if d, ok := tr.(doner); ok {
		return d.DoneChan()
	}
	return nil
}

const (
	reconnectDelay = 3 * time.Second
	// 20 × 3s ≈ 60s reconnect window — long enough to ride out a wifi
	// flap, short enough that the user gives up manually.
	maxReconnects = 20
	// 12 × 5s = 60s D2 budget, matches reconnectTransport.
	maxHealthFailures = 12
)

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
			err := t.reconnectTransport()
			if err == nil {
				doneCh = t.transportDone()
				failures = 0
				t.setConnected()
				continue
			}
			if errors.Is(err, errReconnectStopped) {
				return
			}
			// Same slow-poll wait as engine.go: ENETUNREACH means the OS
			// has no route to the server (WiFi drop / laptop sleep), not
			// that the server is actually down — wait for recovery
			// instead of killing the tunnel.
			if transport.IsNetworkUnreachable(err) {
				if waitErr := t.waitForNetwork(); waitErr == nil {
					doneCh = t.transportDone()
					failures = 0
					t.setConnected()
					continue
				} else if errors.Is(waitErr, errReconnectStopped) {
					return
				}
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
			tr := t.transport
			t.mu.Unlock()

			// Skip ticks while not in a "live" state.
			if status != Connected && status != Reconnecting {
				continue
			}

			// D2 — health check. Prefer the active transport's Alive()
			// signal over a fresh TLS dial when we have one: TSPU regularly
			// blips TCP to the server for ~1s at a time while the UDP
			// transport happily keeps streaming, and the old verifyServer
			// path engaged the kill switch on every blip — closing every
			// active SOCKS5 relay (YouTube, Chrome, etc.) and showing the
			// user a "Reconnecting..." toast for a glitch the proxy is
			// actually riding through. engine.go already took this approach
			// in healthCheck; the SOCKS5 tunnel was left on the old logic.
			// Fall back to verifyServer when no transport is set (legacy
			// SOCKS5-only path).
			var healthErr error
			if tr != nil {
				if a, ok := tr.(interface{ Alive() bool }); ok && !a.Alive() {
					healthErr = fmt.Errorf("transport dead")
				}
			} else {
				healthErr = verifyServer(addr, key)
			}
			if healthErr != nil {
				failures++
				log.Printf("[tunnel] D2: health check failed (%d/%d): %v", failures, maxHealthFailures, healthErr)
				if failures == 1 {
					t.setReconnecting()
				}
				if failures >= maxHealthFailures {
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
			// Previously this only flipped status to Reconnecting without
			// replacing the transport — and since verifyServer keeps
			// succeeding (server is fine, it's just our transport stuck),
			// failures stays at 0 and the recovery branch above never
			// fires, leaving the tunnel wedged in Reconnecting forever
			// (user had to manually disconnect+connect). Now we mirror
			// the D1 branch: actually rebuild the transport.
			if status == Connected && t.stallDetected() {
				log.Printf("[tunnel] D3: traffic stall detected")
				t.setReconnecting()

				// If Auto chose UDP but data stalled, try TLS fallback first.
				if fell := t.tryFallbackToTLS(); fell {
					doneCh = t.transportDone()
					failures = 0
					t.setConnected()
					continue
				}

				err := t.reconnectTransport()
				if err == nil {
					doneCh = t.transportDone()
					failures = 0
					t.setConnected()
					continue
				}
				if errors.Is(err, errReconnectStopped) {
					return
				}
				if transport.IsNetworkUnreachable(err) {
					if waitErr := t.waitForNetwork(); waitErr == nil {
						doneCh = t.transportDone()
						failures = 0
						t.setConnected()
						continue
					} else if errors.Is(waitErr, errReconnectStopped) {
						return
					}
				}
				log.Printf("[tunnel] D3: reconnect exhausted, disconnecting")
				t.mu.Lock()
				t.lastError = "Connection stalled, please reconnect"
				t.stopLocked()
				t.mu.Unlock()
				return
			}
		}
	}
}

// stallDetected returns true when the user is actively trying to use the
// proxy (activeHosts > 0) but no bytes have flowed for stallThreshold.
// Idle sessions (no active hosts) never trip this.
func (t *Tunnel) stallDetected() bool {
	// Use GetActiveHosts (which sweeps stale entries) instead of raw
	// map length. Without the sweep, hosts linger in the map long after
	// their hostLiveWindow expires; D3 sees "active hosts + stale meter"
	// and fires during normal idle browsing (user loaded a page and is
	// reading it — no bytes flowing, but stale map entries make it look
	// like active usage). GetActiveHosts deletes entries older than
	// hostLiveWindow, so truly idle sessions return 0 and never trip D3.
	if len(t.GetActiveHosts()) == 0 {
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

// errReconnectStopped signals that stopHealth fired during reconnect —
// the caller should exit the health loop without calling stopLocked
// (already being called by whoever signalled stop).
var errReconnectStopped = errors.New("reconnect stopped")

// tryFallbackToTLS checks if the current transport is AutoTransport running
// over UDP. If so, it calls FallbackToTLS to switch to TLS without a full
// reconnect cycle. Returns true if the fallback succeeded.
func (t *Tunnel) tryFallbackToTLS() bool {
	t.mu.Lock()
	tr := t.transport
	serverAddr := t.serverAddr
	key := t.key
	mid := t.machineID
	t.mu.Unlock()

	auto, ok := tr.(*transport.AutoTransport)
	if !ok || auto.Mode() != transport.ModeUDP {
		return false
	}

	log.Printf("[tunnel] D3: Auto+UDP detected, attempting TLS fallback")
	if err := auto.FallbackToTLS(serverAddr, key, mid); err != nil {
		log.Printf("[tunnel] D3: TLS fallback failed: %v", err)
		return false
	}
	log.Printf("[tunnel] D3: fell back to TLS successfully")
	return true
}

// tryReconnectOnce performs a single transport Connect attempt and
// publishes the new transport on success. On failure the fresh transport
// is closed and t.transport is left as the caller set it.
func (t *Tunnel) tryReconnectOnce() error {
	t.mu.Lock()
	factory := t.transportFactory
	serverAddr := t.serverAddr
	key := t.key
	mid := t.machineID
	t.mu.Unlock()

	if factory == nil {
		return errors.New("no transport factory")
	}

	tr := factory()
	if err := tr.Connect(serverAddr, key, mid); err != nil {
		tr.Close()
		return err
	}

	t.mu.Lock()
	t.transport = tr
	t.mu.Unlock()
	log.Printf("[tunnel] reconnected via %s", tr.Mode())
	return nil
}

// reconnectTransport runs the fast retry budget. Returns nil on success,
// errReconnectStopped on stop, or the last attempt's error on exhaustion
// / unrecoverable failure (auth or machine-id rejection).
//
// Mid-budget RefreshRoutes mirrors the engine.go fix: on darwin the
// socket retry loop can spin the full 60s against a stale ifscope
// neighbor cache before waitForNetwork takes over. Refreshing routes
// through the helper every few consecutive ENETUNREACH gives the
// typical WiFi-flap case a ~10s recovery instead of 60s.
func (t *Tunnel) reconnectTransport() error {
	t.mu.Lock()
	if t.transport != nil {
		t.transport.Close()
		t.transport = nil
	}
	refresh := t.refreshRoutesFn
	t.mu.Unlock()

	var lastErr error
	consecutiveUnreach := 0
	nextRefreshAt := fastRetryFirstRefreshAt
	for attempt := 1; attempt <= maxReconnects; attempt++ {
		select {
		case <-t.stopHealth:
			return errReconnectStopped
		default:
		}

		if attempt > 1 {
			time.Sleep(reconnectDelay)
		}

		if refresh != nil && consecutiveUnreach >= nextRefreshAt {
			log.Printf("[tunnel] reconnect: %d consecutive ENETUNREACH, refreshing routes via helper", consecutiveUnreach)
			if err := refresh(); err != nil {
				log.Printf("[tunnel] reconnect: route refresh failed: %v", err)
			} else {
				log.Printf("[tunnel] reconnect: routes refreshed")
			}
			nextRefreshAt = consecutiveUnreach + fastRetryRefreshEvery
		}

		log.Printf("[tunnel] reconnect attempt %d/%d", attempt, maxReconnects)
		err := t.tryReconnectOnce()
		if err == nil {
			return nil
		}
		log.Printf("[tunnel] reconnect attempt %d failed: %v", attempt, err)
		lastErr = err
		if strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "machine id rejected") {
			return err
		}
		if transport.IsNetworkUnreachable(err) {
			consecutiveUnreach++
		} else {
			consecutiveUnreach = 0
			nextRefreshAt = fastRetryFirstRefreshAt
		}
	}
	return lastErr
}

// fastRetryFirstRefreshAt / fastRetryRefreshEvery mirror the same
// constants in tun/engine.go — kept in both packages to avoid a shared
// helper module for two ints. See the engine.go comment for rationale.
const (
	fastRetryFirstRefreshAt = 2
	fastRetryRefreshEvery   = 5
)

// slowPollSchedule — see tun/engine.go for the full rationale. Same ramp
// (3,5,7,10,15,20,30,30 = 120s) is used here so both health loops behave
// identically and the tunnel-only code path can't drift from the engine one.
var slowPollSchedule = []time.Duration{
	3 * time.Second,
	5 * time.Second,
	7 * time.Second,
	10 * time.Second,
	15 * time.Second,
	20 * time.Second,
	30 * time.Second,
	30 * time.Second,
}

// waitForNetwork slow-polls for recovery after reconnectTransport
// exhausted with ENETUNREACH. See engine.go waitForNetwork — same
// rationale, same budget. Without a cap we spin forever when ifscope
// routes were silently flushed and only a helper-driven full restart
// can reinstall them.
func (t *Tunnel) waitForNetwork() error {
	log.Printf("[tunnel] network unreachable — entering slow-poll wait (schedule %v, %d attempts)", slowPollSchedule, len(slowPollSchedule))
	transport.LogNetworkState("[tunnel]")

	for attempt, delay := range slowPollSchedule {
		timer := time.NewTimer(delay)
		select {
		case <-t.stopHealth:
			timer.Stop()
			return errReconnectStopped
		case <-timer.C:
			transport.LogNetworkDiagnostics("[tunnel]", t.serverAddr)
			t.mu.Lock()
			refresh := t.refreshRoutesFn
			t.mu.Unlock()
			if refresh != nil {
				if err := refresh(); err != nil {
					log.Printf("[tunnel] slow-poll: route refresh failed: %v", err)
				} else {
					log.Printf("[tunnel] slow-poll: routes refreshed via helper")
				}
			}
			err := t.tryReconnectOnce()
			if err == nil {
				log.Printf("[tunnel] network recovered on attempt %d (after %s), transport re-established", attempt+1, delay)
				return nil
			}
			if transport.IsNetworkUnreachable(err) {
				continue
			}
			log.Printf("[tunnel] slow-poll wait: non-network error, giving up: %v", err)
			return err
		}
	}
	log.Printf("[tunnel] slow-poll wait: %d attempts exhausted, triggering full restart via client", len(slowPollSchedule))
	return errSlowPollBudgetExhausted
}

// errSlowPollBudgetExhausted mirrors the engine.go sentinel — see there.
var errSlowPollBudgetExhausted = errors.New("slow-poll budget exhausted")

func (t *Tunnel) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go t.handleSOCKS(conn)
	}
}

func (t *Tunnel) handleSOCKS(conn net.Conn) {
	connID := t.trackConn(conn)
	defer func() {
		t.untrackConn(connID)
		conn.Close()
	}()

	req, err := socks5.Handshake(conn)
	if err != nil {
		log.Printf("[socks5] handshake failed: %v", err)
		return
	}
	target := fmt.Sprintf("%s:%d", req.Addr, req.Port)
	log.Printf("[tunnel] new request: %s", target)

	// Kill switch: while reconnecting, refuse new SOCKS5 requests so the
	// browser cannot fall back to a native dialer. The browser sees a
	// SOCKS5 failure, retries within seconds, gets failure again — until
	// the daemon flips back to Connected.
	if t.GetStatus() == Reconnecting {
		socks5.SendFailure(conn)
		return
	}

	// Light up the matching browser tile immediately so the LIVE
	// indicator reacts before the first relayed byte. The relay
	// callback below keeps refreshing the timestamp while traffic
	// flows; once it stops, hostLiveWindow lets it fade out.
	t.touchHost(req.Addr)

	t.mu.Lock()
	tr := t.transport
	t.mu.Unlock()

	if tr != nil {
		t.handleSOCKSTransport(conn, tr, req.Addr, req.Port, target)
	} else {
		t.handleSOCKSLegacy(conn, req.Addr, req.Port, target)
	}
}

func (t *Tunnel) handleSOCKSTransport(conn net.Conn, tr transport.Transport, addr string, port uint16, target string) {
	stream, err := tr.OpenStream(0x01, addr, port)
	if err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] open stream failed for %s: %v", target, err)
		if strings.Contains(err.Error(), "machine id rejected") {
			// Could be a stale transport after server restart (the old
			// TLS connection appears alive but the server doesn't recognise
			// our session). Rebuild the transport first — if the fresh
			// connection also rejects the machine ID, it's a genuine
			// device binding conflict and we stop for real.
			log.Printf("[tunnel] machine id rejected — rebuilding transport")
			t.setReconnecting()
			if t.reconnectTransport() == nil {
				t.setConnected()
				return // recovered — caller will retry via new SOCKS5 request
			}
			log.Printf("[tunnel] DEVICE BINDING CONFLICT: fresh transport also rejected — server says this key is bound to a different machine fingerprint. Stopping tunnel.")
			t.mu.Lock()
			t.lastError = "Device is bound to a different machine"
			t.stopLocked()
			t.mu.Unlock()
		}
		return
	}
	defer stream.Close()

	log.Printf("[tunnel] connected: %s", target)
	socks5.SendSuccess(conn)
	countingRelay(conn, stream, func(in, out int64) {
		t.meter.Add(in, out)
		if in > 0 || out > 0 {
			t.touchHost(addr)
		}
	})
}

func (t *Tunnel) handleSOCKSLegacy(conn net.Conn, addr string, port uint16, target string) {
	tlsConn, err := tls.Dial("tcp", t.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] tls dial %s failed: %v", t.serverAddr, err)
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, t.key); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] auth write failed: %v", err)
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] auth rejected (ok=%v, err=%v)", ok, err)
		return
	}

	fp := machineid.Fingerprint()
	if err := proto.WriteMachineID(tlsConn, fp); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] machine id write failed: %v", err)
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		log.Printf("[tunnel/legacy] DEVICE BINDING CONFLICT: server rejected machine fingerprint (ok=%v err=%v) — stopping tunnel", ok, err)
		t.mu.Lock()
		t.lastError = "Device is bound to a different machine"
		t.stopLocked()
		t.mu.Unlock()
		return
	}

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] msg type write failed: %v", err)
		return
	}

	if err := proto.WriteConnect(tlsConn, addr, port); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] connect write failed for %s: %v", target, err)
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] connect rejected for %s (ok=%v, err=%v)", target, ok, err)
		return
	}

	log.Printf("[tunnel] connected: %s", target)
	socks5.SendSuccess(conn)
	proto.CountingRelay(conn, tlsConn, func(in, out int64) {
		t.meter.Add(in, out)
		if in > 0 || out > 0 {
			t.touchHost(addr)
		}
	})
}

// countingRelay copies data bidirectionally between a net.Conn and a
// transport.Stream with idle timeout, calling onBytes with (download, upload)
// byte counts. When either side finishes, both conn and stream are closed
// to unblock the other goroutine.
func countingRelay(conn net.Conn, stream transport.Stream, onBytes func(in, out int64)) {
	const idleTimeout = 2 * time.Minute

	errc := make(chan error, 2)

	// conn → stream (upload)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			conn.SetReadDeadline(time.Now().Add(idleTimeout))
			n, err := conn.Read(buf)
			if n > 0 {
				if _, werr := stream.Write(buf[:n]); werr != nil {
					errc <- werr
					return
				}
				if onBytes != nil {
					onBytes(0, int64(n))
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	// stream → conn (download)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				if _, werr := conn.Write(buf[:n]); werr != nil {
					errc <- werr
					return
				}
				if onBytes != nil {
					onBytes(int64(n), 0)
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	<-errc
	conn.Close()
	stream.Close()
	<-errc
}

func verifyServer(serverAddr, key string) error {
	tlsConn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: dialTimeout},
		"tcp", serverAddr,
		&tls.Config{InsecureSkipVerify: true},
	)
	if err != nil {
		return fmt.Errorf("server unreachable: %v", err)
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, key); err != nil {
		return fmt.Errorf("auth failed: %v", err)
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil {
		return fmt.Errorf("auth failed: %v", err)
	}
	if !ok {
		return fmt.Errorf("invalid key")
	}
	return nil
}
