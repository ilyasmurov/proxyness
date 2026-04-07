package tunnel

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"smurov-proxy/daemon/internal/socks5"
	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/transport"
	"smurov-proxy/pkg/machineid"
	"smurov-proxy/pkg/proto"
)

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
				log.Printf("[tunnel] D2: health check failed (%d/%d): %v", failures, maxHealthFailures, err)
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

func (t *Tunnel) reconnectTransport() bool {
	t.mu.Lock()
	factory := t.transportFactory
	serverAddr := t.serverAddr
	key := t.key
	mid := t.machineID
	if t.transport != nil {
		t.transport.Close()
		t.transport = nil
	}
	t.mu.Unlock()

	if factory == nil {
		return false
	}

	for attempt := 1; attempt <= maxReconnects; attempt++ {
		select {
		case <-t.stopHealth:
			return false
		default:
		}

		if attempt > 1 {
			time.Sleep(reconnectDelay)
		}

		log.Printf("[tunnel] reconnect attempt %d/%d", attempt, maxReconnects)
		tr := factory()
		if err := tr.Connect(serverAddr, key, mid); err != nil {
			log.Printf("[tunnel] reconnect attempt %d failed: %v", attempt, err)
			tr.Close()
			if strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "machine id rejected") {
				return false
			}
			continue
		}

		t.mu.Lock()
		t.transport = tr
		t.mu.Unlock()
		log.Printf("[tunnel] reconnected via %s", tr.Mode())
		return true
	}
	return false
}

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
		log.Printf("[tunnel] machine id rejected")
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
