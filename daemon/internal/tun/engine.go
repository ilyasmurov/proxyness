package tun

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	dstats "proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/transport"
	"proxyness/pkg/machineid"
	"proxyness/pkg/proto"
)

type Status string

const (
	StatusInactive     Status = "inactive"
	StatusActive       Status = "active"
	StatusReconnecting Status = "reconnecting"
)

type Engine struct {
	mu           sync.Mutex
	status       Status
	serverAddr   string
	key          string
	rules        *Rules
	procInfo     ProcessInfo
	stack        *stack.Stack
	helperAddr   string
	helperConn   net.Conn
	endpoint     *channel.Endpoint
	bridgeCancel context.CancelFunc
	bridgeDone   chan struct{} // signalled by bridge goroutines on exit; healthLoop D4 watches it
	selfPath      string // daemon's own path — always bypassed to prevent loops
	rawUDP        *RawUDPHandler
	helperWriteMu sync.Mutex
	meter         *dstats.RateMeter
	transport        transport.Transport
	transportFactory func() transport.Transport
	machineID        [16]byte
	startTime        time.Time
	lastError        string
	stopHealth       chan struct{}

	connsMu sync.Mutex
	conns   map[uint64]trackedConn
	connSeq uint64

	// streamOpenFailures counts consecutive OpenStream errors from the
	// active transport. Reset to 0 on any successful OpenStream. The
	// healthLoop reads it each tick and forces a reconnect when the
	// counter crosses streamFailureThreshold — this catches the case
	// where transport.Alive() falsely reports "healthy" while every
	// real stream dial fails (network went away under TUN routes).
	streamOpenFailures atomic.Int32
}

// streamFailureThreshold is how many consecutive OpenStream failures
// must pile up before the healthLoop treats the transport as dead.
// 3 matches the existing D2 failure budget and lines up with the 5s
// tick — in practice this fires within ~5s of network death because
// apps re-dial aggressively.
const streamFailureThreshold = 3

func NewEngine(meter *dstats.RateMeter) *Engine {
	selfPath, _ := os.Executable()
	return &Engine{
		status:   StatusInactive,
		rules:    NewRules(),
		selfPath: selfPath,
		meter:    meter,
		conns:    make(map[uint64]trackedConn),
	}
}

func (e *Engine) SetTransport(tr transport.Transport) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transport = tr
}

func (e *Engine) SetTransportFactory(factory func() transport.Transport, machineID [16]byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.transportFactory = factory
	e.machineID = machineID
}

func (e *Engine) GetStatus() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) GetRules() *Rules {
	return e.rules
}

func (e *Engine) GetUptime() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status != StatusActive {
		return 0
	}
	return int64(time.Since(e.startTime).Seconds())
}

func (e *Engine) GetLastError() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastError
}

// setReconnecting flips status from Active → Reconnecting and sweeps
// every in-flight TCP/UDP conn (kill switch). Idempotent: calling twice
// in the same state is a no-op. Caller must NOT hold e.mu.
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

type trackedConn struct {
	conn    net.Conn
	appPath string // empty for DNS/unresolved connections
}

// UpdateRules applies new rules and closes only connections whose routing
// policy actually changed (e.g. an app was toggled on/off). Connections
// belonging to unchanged apps stay alive.
func (e *Engine) UpdateRules(data []byte) error {
	// Snapshot current ShouldProxy results before applying new rules.
	e.connsMu.Lock()
	type verdict struct {
		tc       trackedConn
		oldProxy bool
	}
	snap := make(map[uint64]verdict, len(e.conns))
	for id, tc := range e.conns {
		old := true
		if tc.appPath != "" {
			old = e.rules.ShouldProxy(tc.appPath)
		}
		snap[id] = verdict{tc, old}
	}
	e.connsMu.Unlock()

	if err := e.rules.FromJSON(data); err != nil {
		return err
	}

	// Close only connections whose routing changed.
	var closed int
	for _, v := range snap {
		if v.tc.appPath == "" {
			continue // DNS or unknown — no app to re-evaluate
		}
		newProxy := e.rules.ShouldProxy(v.tc.appPath)
		if newProxy != v.oldProxy {
			v.tc.conn.Close()
			closed++
		}
	}
	if closed > 0 {
		log.Printf("[tun] closed %d connections after rules update (kept %d)", closed, len(snap)-closed)
	}
	return nil
}

func (e *Engine) trackConn(c net.Conn, appPath string) uint64 {
	e.connsMu.Lock()
	defer e.connsMu.Unlock()
	e.connSeq++
	id := e.connSeq
	e.conns[id] = trackedConn{conn: c, appPath: appPath}
	return id
}

func (e *Engine) untrackConn(id uint64) {
	e.connsMu.Lock()
	defer e.connsMu.Unlock()
	delete(e.conns, id)
}

func (e *Engine) closeAllConns() {
	e.connsMu.Lock()
	snapshot := make([]net.Conn, 0, len(e.conns))
	for _, tc := range e.conns {
		snapshot = append(snapshot, tc.conn)
	}
	e.connsMu.Unlock()

	for _, c := range snapshot {
		c.Close()
	}
	if len(snapshot) > 0 {
		log.Printf("[tun] closed %d connections", len(snapshot))
	}
}

type StartRequest struct {
	ServerAddr string `json:"server"`
	Key        string `json:"key"`
	HelperAddr string `json:"helper_addr"`
}

func (e *Engine) Start(req StartRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status == StatusActive {
		// Log the goroutine stack so we can see who's hitting Start while
		// the engine is already up. Restart-storms (multiple /tun/start
		// HTTP calls in flight from a racy client retry loop) corrupt
		// gVisor netstack and burn CPU rebuilding NAT tables; if this
		// fires repeatedly, look at the trace to find the runaway caller.
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		log.Printf("[tun] already active, restarting...\n%s", buf[:n])
		e.stopLocked()
	}

	// Cache physical interface before TUN routes are added
	CachePhysicalInterface()

	// Connect to helper and create TUN — keep connection open for packet relay
	helperConn, err := e.connectAndCreate(req)
	if err != nil {
		return fmt.Errorf("helper create: %w", err)
	}

	s, ep, err := newStack(1500)
	if err != nil {
		helperConn.Close()
		return fmt.Errorf("create stack: %w", err)
	}

	e.stack = s
	e.endpoint = ep
	e.helperConn = helperConn
	e.serverAddr = req.ServerAddr
	e.key = req.Key
	e.helperAddr = req.HelperAddr
	e.procInfo = newProcessInfo()

	// buf = [2-byte BE length | IPv4+UDP packet], prebuilt by the NAT
	// readLoop in a pooled buffer. One Write avoids the per-packet lenBuf
	// allocation and the second syscall the pre-1.36.2 path cost. buf is
	// only valid until this callback returns — do not retain it.
	nat := NewNATTable(func(buf []byte) {
		e.helperWriteMu.Lock()
		defer e.helperWriteMu.Unlock()
		e.mu.Lock()
		conn := e.helperConn
		e.mu.Unlock()
		if conn != nil {
			conn.Write(buf)
		}
	})
	e.rawUDP = &RawUDPHandler{
		nat:      nat,
		rules:    e.rules,
		procInfo: e.procInfo,
		selfPath: e.selfPath,
	}

	tcpFwd := tcp.NewForwarder(s, 0, 2048, func(r *tcp.ForwarderRequest) {
		e.handleTCP(r)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		e.handleUDP(r)
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	// Start bridge: helper IPC ↔ gVisor channel endpoint
	ctx, cancel := context.WithCancel(context.Background())
	e.bridgeCancel = cancel
	e.bridgeDone = make(chan struct{}, 2)
	go e.bridgeInbound(helperConn, ep, e.bridgeDone)
	go e.bridgeOutbound(ctx, helperConn, ep, e.bridgeDone)

	e.status = StatusActive
	e.startTime = time.Now()
	e.lastError = ""
	e.stopHealth = make(chan struct{})
	if e.meter != nil {
		e.meter.SeedLastByteAt()
	}
	go e.healthLoop()

	log.Printf("[tun] engine started, server=%s", req.ServerAddr)
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopLocked()
}

func (e *Engine) stopLocked() error {
	if e.status == StatusInactive {
		return nil
	}

	// Log a goroutine stack trace so we can diagnose spurious stops. The
	// bridge-close / "use of closed network connection" errors in user
	// reports don't have any preceding D1/D2/D3/machine-id log entries,
	// which means something outside the expected paths is hitting us and
	// we can't tell who without the trace. Single-frame (`runtime.Stack(..,
	// false)`) keeps the output compact.
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, false)
	log.Printf("[tun] stopLocked called:\n%s", buf[:n])

	if e.stopHealth != nil {
		close(e.stopHealth)
		e.stopHealth = nil
	}

	// Cancel bridge goroutines
	if e.bridgeCancel != nil {
		e.bridgeCancel()
		e.bridgeCancel = nil
	}
	e.bridgeDone = nil

	if e.rawUDP != nil && e.rawUDP.nat != nil {
		e.rawUDP.nat.Close()
		e.rawUDP = nil
	}

	if e.stack != nil {
		e.stack.Close()
		e.stack = nil
	}

	// Close relay connection — helper will auto-cleanup TUN device
	if e.helperConn != nil {
		e.helperConn.Close()
		e.helperConn = nil
	}

	if e.transport != nil {
		e.transport.Close()
		e.transport = nil
	}

	e.endpoint = nil
	e.status = StatusInactive
	ClearPhysicalInterfaceCache()
	log.Printf("[tun] engine stopped")
	return nil
}

// connectAndCreate connects to helper, sends "create" with server address,
// reads the JSON response, and returns the connection positioned at the
// start of the relay stream.
//
// We avoid json.Decoder because it buffers ahead (including the trailing \n
// from json.Encoder), which desynchronizes the binary relay framing.
// Instead we read one byte at a time until \n, then json.Unmarshal.
func (e *Engine) connectAndCreate(req StartRequest) (net.Conn, error) {
	conn, err := dialHelper(req.HelperAddr)
	if err != nil {
		return nil, err
	}

	helperReq := map[string]string{
		"action":      "create",
		"server_addr": req.ServerAddr,
	}
	if err := json.NewEncoder(conn).Encode(helperReq); err != nil {
		conn.Close()
		return nil, err
	}

	// Read response line byte-by-byte until \n (json.Encoder appends \n).
	// This ensures conn is positioned exactly at the relay stream start.
	var respBuf []byte
	oneByte := make([]byte, 1)
	for {
		if _, err := conn.Read(oneByte); err != nil {
			conn.Close()
			return nil, fmt.Errorf("read response: %w", err)
		}
		if oneByte[0] == '\n' {
			break
		}
		respBuf = append(respBuf, oneByte[0])
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if !resp.OK {
		conn.Close()
		return nil, fmt.Errorf("helper: %s", resp.Error)
	}

	return conn, nil
}

// RefreshRoutes asks the helper to re-install the server-host, DNS,
// and ifscope bypass routes without destroying the TUN device. Used by
// waitForNetwork to unstick the kernel's ARP/neighbor cache when
// sendto() returns ENETUNREACH despite routes being present in
// netstat — a macOS quirk after physical-interface flaps (Docker
// vmnetd re-creating bridged interfaces, USB-ethernet plug/unplug,
// brief wifi loss). A plain socket reconnect can't recover because
// the blackhole is at the neighbor layer, not the transport. The
// helper's `route delete + route add -host <server> <gw>` forces a
// fresh neighbor resolution for the gateway, which is exactly what
// the user's manual "disconnect → reconnect" achieved. Dialing a
// fresh helper connection rather than piggybacking on helperConn is
// intentional: helperConn is in packet-relay mode and sending JSON
// on it would corrupt the framing.
func (e *Engine) RefreshRoutes() error {
	e.mu.Lock()
	addr := e.helperAddr
	e.mu.Unlock()
	if addr == "" {
		return errors.New("helper not connected")
	}
	conn, err := dialHelper(addr)
	if err != nil {
		return fmt.Errorf("dial helper: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := map[string]string{"action": "refresh_routes"}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("write request: %w", err)
	}
	var respBuf []byte
	oneByte := make([]byte, 1)
	for {
		if _, err := conn.Read(oneByte); err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		if oneByte[0] == '\n' {
			break
		}
		respBuf = append(respBuf, oneByte[0])
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBuf, &resp); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("helper: %s", resp.Error)
	}
	return nil
}

// bridgeInbound reads framed IP packets from helper and injects into gVisor stack.
//
// Allocations: gVisor's buffer.MakeWithData → NewViewWithData *copies* the
// payload into its own pooled chunk, so the caller's slice is dead the
// instant InjectInbound returns. Pre-1.28.9 we did `data := make([]byte,
// pktLen)` per packet, generating one fresh slice per inbound packet for
// the GC to reap. On Windows this dominated daemon CPU even at idle —
// pprof showed ~70% in runtime.gcDrain. Now we reuse a single pktBuf
// across iterations, growing it on demand. bridgeInbound runs as a single
// goroutine per engine so no synchronization is needed. RawUDPHandler.Handle
// must also be safe to call against a slice that gets reused on the next
// iteration — confirmed: it copies anything it needs to keep.
func (e *Engine) bridgeInbound(r io.Reader, ep *channel.Endpoint, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	log.Printf("[tun] bridgeInbound started")
	lenBuf := make([]byte, 2)
	pktBuf := make([]byte, 2048) // 1500 MTU + headroom; grows if needed
	var count int64
	var ipv4Count, ipv6Count int64
	for {
		if _, err := io.ReadFull(r, lenBuf); err != nil {
			log.Printf("[tun] bridgeInbound closed (after %d packets): %v", count, err)
			return
		}
		pktLen := int(binary.BigEndian.Uint16(lenBuf))
		if pktLen == 0 {
			continue
		}

		if cap(pktBuf) < pktLen {
			pktBuf = make([]byte, pktLen)
		}
		data := pktBuf[:pktLen]
		if _, err := io.ReadFull(r, data); err != nil {
			log.Printf("[tun] bridgeInbound read error (after %d packets): %v", count, err)
			return
		}

		var proto tcpip.NetworkProtocolNumber
		ipVer := data[0] >> 4
		if ipVer == 4 {
			proto = header.IPv4ProtocolNumber
			ipv4Count++
		} else {
			proto = header.IPv6ProtocolNumber
			ipv6Count++
		}

		count++
		if count == 1 {
			log.Printf("[tun] bridgeInbound first packet: %d bytes, IPv%d", pktLen, ipVer)
		}
		if count%10000 == 0 {
			log.Printf("[tun] bridgeInbound injected %d packets (IPv4=%d, IPv6=%d)", count, ipv4Count, ipv6Count)
		}

		// Raw UDP bypass: handle before gVisor injection
		if e.rawUDP != nil && e.rawUDP.Handle(data) {
			continue
		}

		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{
			Payload: buffer.MakeWithData(data),
		})
		ep.InjectInbound(proto, pkt)
		pkt.DecRef()
	}
}

// bridgeOutbound reads outgoing packets from gVisor stack and sends to helper.
//
// Allocations: gVisor's buffer.Flatten() always does `make([]byte, 0, size)`
// internally. Pre-1.28.10 we called it per packet, generating a fresh slice
// every outbound packet just like bridgeInbound was doing on the inbound
// side. Now we reuse a single growing frame buffer (length prefix + payload)
// across iterations and copy via ReadAt straight into it. Single goroutine
// per engine — no synchronization needed for the buffer itself; the helper
// write is still serialized via helperWriteMu against bridgeInbound.
func (e *Engine) bridgeOutbound(ctx context.Context, conn net.Conn, ep *channel.Endpoint, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	log.Printf("[tun] bridgeOutbound started")
	frame := make([]byte, 2+2048) // 2-byte length prefix + 1500 MTU + headroom
	var count int64
	for {
		pkt := ep.ReadContext(ctx)
		if pkt == nil {
			log.Printf("[tun] bridgeOutbound closed (after %d packets)", count)
			return
		}
		count++
		if count == 1 {
			log.Printf("[tun] bridgeOutbound first packet out")
		}

		buf := pkt.ToBuffer()
		size := int(buf.Size())
		if cap(frame) < 2+size {
			frame = make([]byte, 2+size)
		}
		frame = frame[:2+size]
		binary.BigEndian.PutUint16(frame[:2], uint16(size))
		// gVisor's Buffer.ReadAt follows the io.ReaderAt convention: it
		// returns io.EOF when the read reaches the end of the buffer,
		// even on a *successful* full read. We only care that we got
		// every byte we asked for. Treating io.EOF as fatal here was
		// the 1.28.10 regression — bridgeOutbound silently exited
		// after the first packet (no "closed" log because we returned
		// inside the if), helper saw daemon→TUN=0 forever, and apps
		// got nothing back through the tunnel.
		n, err := buf.ReadAt(frame[2:], 0)
		if n != size {
			pkt.DecRef()
			log.Printf("[tun] bridgeOutbound short read %d/%d: %v", n, size, err)
			return
		}
		pkt.DecRef()

		e.helperWriteMu.Lock()
		_, err1 := conn.Write(frame)
		e.helperWriteMu.Unlock()
		if err1 != nil {
			log.Printf("[tun] bridgeOutbound write error after %d packets: %v", count, err1)
			return
		}
	}
}

func (e *Engine) transportDone() <-chan struct{} {
	e.mu.Lock()
	tr := e.transport
	e.mu.Unlock()
	type doner interface {
		DoneChan() <-chan struct{}
	}
	if d, ok := tr.(doner); ok {
		return d.DoneChan()
	}
	return nil
}

// errReconnectStopped signals that stopHealth fired during a reconnect
// attempt — the caller should exit the health loop without calling
// stopLocked (which is already being called by whoever signalled stop).
var errReconnectStopped = errors.New("reconnect stopped")

// tryFallbackToTLS checks if the current transport is AutoTransport running
// over UDP. If so, it calls FallbackToTLS to switch to TLS without a full
// reconnect cycle. Returns true if the fallback succeeded.
func (e *Engine) tryFallbackToTLS() bool {
	e.mu.Lock()
	tr := e.transport
	serverAddr := e.serverAddr
	key := e.key
	mid := e.machineID
	e.mu.Unlock()

	auto, ok := tr.(*transport.AutoTransport)
	if !ok || auto.Mode() != transport.ModeUDP {
		return false
	}

	log.Printf("[tun] D3: Auto+UDP detected, attempting TLS fallback")
	if err := auto.FallbackToTLS(serverAddr, key, mid); err != nil {
		log.Printf("[tun] D3: TLS fallback failed: %v", err)
		return false
	}
	log.Printf("[tun] D3: fell back to TLS successfully")
	return true
}

// tryReconnectOnce performs a single transport Connect attempt and, on
// success, publishes the new transport to e.transport. On failure the
// fresh transport is closed and the engine's transport field is left as
// the caller set it (typically nil, after reconnectTransport cleared it).
func (e *Engine) tryReconnectOnce() error {
	e.mu.Lock()
	factory := e.transportFactory
	serverAddr := e.serverAddr
	key := e.key
	mid := e.machineID
	e.mu.Unlock()

	if factory == nil {
		return errors.New("no transport factory")
	}

	tr := factory()
	if err := tr.Connect(serverAddr, key, mid); err != nil {
		tr.Close()
		return err
	}

	e.mu.Lock()
	e.transport = tr
	e.mu.Unlock()
	log.Printf("[tun] reconnected via %s", tr.Mode())
	return nil
}

// reconnectTransport runs the fast retry budget (maxReconnects ×
// reconnectDelay). Returns nil on success, errReconnectStopped if stop
// was requested, or the last attempt's error on exhaustion or
// unrecoverable failure (auth/machine-id rejection).
func (e *Engine) reconnectTransport() error {
	e.mu.Lock()
	if e.transport != nil {
		e.transport.Close()
		e.transport = nil
	}
	e.mu.Unlock()

	// 20 × 3s ≈ 60s total reconnect window — long enough to ride out a
	// typical wifi flap, short enough that the user notices manually
	// rather than staring at "Reconnecting…" forever for a dead server.
	// Longer outages (laptop sleep, extended WiFi drop) are caught by
	// the slow-poll wait in the D1/D3 branches of healthLoop.
	const maxReconnects = 20
	const reconnectDelay = 3 * time.Second

	var lastErr error
	for attempt := 1; attempt <= maxReconnects; attempt++ {
		select {
		case <-e.stopHealth:
			return errReconnectStopped
		default:
		}

		if attempt > 1 {
			time.Sleep(reconnectDelay)
		}

		log.Printf("[tun] reconnect attempt %d/%d", attempt, maxReconnects)
		err := e.tryReconnectOnce()
		if err == nil {
			return nil
		}
		log.Printf("[tun] reconnect attempt %d failed: %v", attempt, err)
		lastErr = err
		if strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "machine id rejected") {
			return err
		}
	}
	return lastErr
}

// slowPollSchedule is the per-attempt wait before the next reconnect try in
// waitForNetwork. The ramp starts tight (3s) because a real incident on
// 2026-04-15 was caused by Docker Desktop's vmnetd re-creating a virtual
// ethN interface: configd fires "network changed" ×4 in a second, the ARP
// cache for the physical gateway gets invalidated, and recovery typically
// happens within ~10-20s. A flat 15s interval meant the user's first chance
// to auto-recover was *after* 15s had already passed, during which manual
// reconnect races the loop. The ramp keeps total budget at 120s (8 attempts)
// so the "full restart via client" fallback semantics are unchanged.
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
// exhausted with ENETUNREACH. Returns nil on recovery, errReconnectStopped
// on stop, a wrapped non-network error if the server error surfaces, or
// errSlowPollBudgetExhausted once slowPollSchedule is consumed — the caller
// then falls through to stopLocked, which the client's status poll picks up
// and turns into a fresh engine.Start via startReconnect. That rebuild hits
// connectAndCreate in engine.go, which re-runs the helper's createTUN and
// reinstalls the ifscope bypass and server host routes from scratch. Without
// a budget we spin forever when the routes were flushed by some OS event
// (observed once on macOS: 6+ minutes of silent ENETUNREACH, manual
// disconnect+reconnect was the only way out).
//
// On each tick LogNetworkDiagnostics dumps the ARP cache and the kernel's
// resolved route to the server so the next incident carries a full record
// of how the OS's view of the network evolved during recovery.
func (e *Engine) waitForNetwork() error {
	log.Printf("[tun] network unreachable — entering slow-poll wait (schedule %v, %d attempts)", slowPollSchedule, len(slowPollSchedule))
	transport.LogNetworkState("[tun]")

	for attempt, delay := range slowPollSchedule {
		timer := time.NewTimer(delay)
		select {
		case <-e.stopHealth:
			timer.Stop()
			return errReconnectStopped
		case <-timer.C:
			transport.LogNetworkDiagnostics("[tun]", e.serverAddr)
			if err := e.RefreshRoutes(); err != nil {
				log.Printf("[tun] slow-poll: route refresh failed: %v", err)
			} else {
				log.Printf("[tun] slow-poll: routes refreshed via helper")
			}
			err := e.tryReconnectOnce()
			if err == nil {
				log.Printf("[tun] network recovered on attempt %d (after %s), transport re-established", attempt+1, delay)
				return nil
			}
			if transport.IsNetworkUnreachable(err) {
				// Still no routes, keep waiting silently.
				continue
			}
			log.Printf("[tun] slow-poll wait: non-network error, giving up: %v", err)
			return err
		}
	}
	log.Printf("[tun] slow-poll wait: %d attempts exhausted, triggering full engine restart via client", len(slowPollSchedule))
	return errSlowPollBudgetExhausted
}

// errSlowPollBudgetExhausted is returned by waitForNetwork when slowPollSchedule
// is consumed and every attempt produced ENETUNREACH. Signals the caller to
// stop the engine so the client restarts it and the helper gets a chance to
// re-install routes.
var errSlowPollBudgetExhausted = errors.New("slow-poll budget exhausted")

func (e *Engine) healthLoop() {
	// 12 × 5s = 60s — matches the reconnectTransport budget above so
	// both detectors give up at the same point.
	const maxFailures = 12
	ticker := time.NewTicker(5 * time.Second) // was 30s — needed for fast D2 detection
	defer ticker.Stop()

	doneCh := e.transportDone()
	bridgeDone := e.bridgeDone
	failures := 0
	for {
		select {
		case <-e.stopHealth:
			return

		case <-bridgeDone:
			// D4 — TUN bridge lost: helper process died or TUN device
			// was destroyed. The bridge goroutines are the only path
			// between the TUN device and gVisor; without them packets
			// can't flow regardless of transport health. Stop the
			// engine so the client sees the error and auto-reconnects.
			log.Printf("[tun] D4: TUN bridge lost (helper disconnected)")
			e.mu.Lock()
			e.lastError = "TUN bridge lost, please reconnect"
			e.stopLocked()
			e.mu.Unlock()
			return

		case <-doneCh:
			// D1 — transport closed: engage kill switch, then try to reconnect.
			log.Printf("[tun] D1: transport closed")
			e.setReconnecting()
			err := e.reconnectTransport()
			if err == nil {
				doneCh = e.transportDone()
				failures = 0
				e.setConnected()
				continue
			}
			if errors.Is(err, errReconnectStopped) {
				return
			}
			// Fast retries exhausted. If the root cause is "network is
			// unreachable" (laptop sleep, WiFi gone) we enter slow-poll
			// wait instead of killing the engine — otherwise an overnight
			// sleep leaves the proxy dead until manual reconnect.
			if transport.IsNetworkUnreachable(err) {
				if waitErr := e.waitForNetwork(); waitErr == nil {
					doneCh = e.transportDone()
					failures = 0
					e.setConnected()
					continue
				} else if errors.Is(waitErr, errReconnectStopped) {
					return
				}
			}
			log.Printf("[tun] D1: reconnect exhausted, stopping engine")
			e.mu.Lock()
			e.lastError = "Connection lost, please reconnect"
			e.stopLocked()
			e.mu.Unlock()
			return

		case <-ticker.C:
			// D3 — stream-failure detector. transport.Alive() is too
			// optimistic for TLS (reports "alive" even when the OS has
			// dropped all routes), so we also watch the OpenStream
			// failure counter. Apps retry aggressively, so once the
			// network is down the counter races past the threshold in
			// ~1s. When it trips, rebuild the transport directly
			// rather than relying on Close() → DoneChan → D1 chain —
			// that chain was broken before 1.29.1 because TLS transport
			// didn't implement DoneChan, so a TLS-based transport that
			// D3 closed never signalled D1 and the engine sat in
			// Reconnecting until D2 exhausted and stopped the engine.
			// Now we mirror D1's own reconnect path directly.
			if e.streamOpenFailures.Load() >= streamFailureThreshold {
				log.Printf("[tun] D3: %d consecutive stream failures, forcing transport rebuild",
					e.streamOpenFailures.Load())
				e.streamOpenFailures.Store(0)
				e.setReconnecting()

				// If Auto chose UDP but streams keep failing, the UDP data
				// path is likely blocked (TSPU/ISP). Try falling back to TLS
				// within the same AutoTransport before doing a full rebuild.
				if fell := e.tryFallbackToTLS(); fell {
					doneCh = e.transportDone()
					failures = 0
					e.setConnected()
					continue
				}

				err := e.reconnectTransport()
				if err == nil {
					doneCh = e.transportDone()
					failures = 0
					e.setConnected()
					continue
				}
				if errors.Is(err, errReconnectStopped) {
					return
				}
				if transport.IsNetworkUnreachable(err) {
					if waitErr := e.waitForNetwork(); waitErr == nil {
						doneCh = e.transportDone()
						failures = 0
						e.setConnected()
						continue
					} else if errors.Is(waitErr, errReconnectStopped) {
						return
					}
				}
				log.Printf("[tun] D3: reconnect exhausted, stopping engine")
				e.mu.Lock()
				e.lastError = "Connection lost, please reconnect"
				e.stopLocked()
				e.mu.Unlock()
				return
			}
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

// healthCheck validates the ACTIVE transport rather than dialing a fresh TCP
// connection to the server. The old implementation would happily report
// "healthy" when UDP was silently dead but TCP/TLS to the server still worked
// (common after macOS sleep/wake), creating a blind spot. Checking Alive()
// on the real transport avoids that false positive.
func (e *Engine) healthCheck() error {
	e.mu.Lock()
	tr := e.transport
	e.mu.Unlock()
	if tr == nil {
		return fmt.Errorf("no transport")
	}
	if a, ok := tr.(interface{ Alive() bool }); ok && !a.Alive() {
		return fmt.Errorf("transport dead")
	}
	return nil
}

func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	// Kill switch: refuse new TCP flows while reconnecting. Calling
	// Complete(true) tells gVisor to send a RST — the originating app
	// will see "connection refused".
	if e.GetStatus() == StatusReconnecting {
		r.Complete(true)
		return
	}

	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	var appPath string
	shouldProxy := true
	if e.rules.NeedProcessLookup() {
		appPath, _ = e.procInfo.FindProcess("tcp", srcPort)
		shouldProxy = !e.isSelf(appPath) && e.rules.ShouldProxy(appPath)
	}

	if appPath != "" {
		log.Printf("[tun] TCP %s:%d from %s (proxy=%v)", dstAddr, dstPort, appPath, shouldProxy)
	}

	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		r.Complete(true)
		return
	}
	r.Complete(false)

	conn := gonet.NewTCPConn(&wq, ep)
	connID := e.trackConn(conn, appPath)
	defer func() {
		e.untrackConn(connID)
		conn.Close()
	}()

	if shouldProxy {
		e.proxyTCP(conn, dstAddr, dstPort, appPath)
	} else {
		e.bypassTCP(conn, dstAddr, dstPort)
	}
}

func (e *Engine) proxyTCP(local net.Conn, dstAddr string, dstPort uint16, appPath string) {
	e.mu.Lock()
	tr := e.transport
	e.mu.Unlock()

	if tr != nil {
		e.proxyTCPTransport(local, tr, dstAddr, dstPort, appPath)
	} else {
		e.proxyTCPLegacy(local, dstAddr, dstPort)
	}
}

func (e *Engine) proxyTCPTransport(local net.Conn, tr transport.Transport, dstAddr string, dstPort uint16, appPath string) {
	stream, err := tr.OpenStream(0x01, dstAddr, dstPort)
	if err != nil {
		// Only count transport-level failures (timeout, closed) toward D3.
		// "connect rejected" means the server successfully received our request
		// and the destination refused — the transport is healthy.
		if !strings.Contains(err.Error(), "connect rejected") {
			e.streamOpenFailures.Add(1)
		}
		log.Printf("[tun] open TCP stream failed for %s:%d: %v", dstAddr, dstPort, err)
		if strings.Contains(err.Error(), "machine id rejected") {
			log.Printf("[tun] DEVICE BINDING CONFLICT: server reports this key is bound to a different machine fingerprint — stopping engine. (dst=%s:%d)", dstAddr, dstPort)
			e.mu.Lock()
			e.lastError = "Device is bound to a different machine"
			e.stopLocked()
			e.mu.Unlock()
		}
		return
	}
	e.streamOpenFailures.Store(0)

	sniff := newSNISniffer(local)
	start := time.Now()
	var bytesDown, bytesUp int64

	reason := streamRelay(sniff, stream, func(in, out int64) {
		atomic.AddInt64(&bytesDown, in)
		atomic.AddInt64(&bytesUp, out)
		e.meter.Add(in, out)
	})

	logTCPClose(dstAddr, dstPort, sniff.Host(), appPath,
		time.Since(start),
		atomic.LoadInt64(&bytesUp),
		atomic.LoadInt64(&bytesDown),
		reason)
}

func (e *Engine) proxyTCPLegacy(local net.Conn, dstAddr string, dstPort uint16) {
	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		log.Printf("[tun] protected dial failed: %v", err)
		return
	}

	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("[tun] tls handshake failed: %v", err)
		rawConn.Close()
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	fp := machineid.Fingerprint()
	if err := proto.WriteMachineID(tlsConn, fp); err != nil {
		log.Printf("[tun/legacy] machine id write failed: %v", err)
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		log.Printf("[tun/legacy] DEVICE BINDING CONFLICT: server rejected machine fingerprint (ok=%v err=%v) — stopping engine", ok, err)
		e.mu.Lock()
		e.lastError = "Device is bound to a different machine"
		e.stopLocked()
		e.mu.Unlock()
		return
	}

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		return
	}
	if err := proto.WriteConnect(tlsConn, dstAddr, dstPort); err != nil {
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	idleRelay(local, tlsConn, func(in, out int64) {
		e.meter.Add(in, out)
	})
}

func (e *Engine) bypassTCP(local net.Conn, dstAddr string, dstPort uint16) {
	target, err := protectedDial("tcp", fmt.Sprintf("%s:%d", dstAddr, dstPort))
	if err != nil {
		return
	}
	defer target.Close()
	idleRelay(local, target, nil)
}

func (e *Engine) handleUDP(r *udp.ForwarderRequest) {
	// Kill switch: drop new UDP flows while reconnecting. Returning
	// without calling CreateEndpoint causes gVisor to discard the
	// inbound packet silently. Apps see UDP timeout. We apply this
	// even to DNS (port 53) so apps fail cleanly instead of resolving
	// but failing to connect.
	if e.GetStatus() == StatusReconnecting {
		return
	}

	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	// Block QUIC (UDP 443) — forces Chrome to fall back to TCP/HTTPS
	if dstPort == 443 {
		return
	}

	// DNS (port 53) always bypasses — needed for system resolver to work
	if dstPort == 53 {
		var wq waiter.Queue
		ep, udpErr := r.CreateEndpoint(&wq)
		if udpErr != nil {
			return
		}
		conn := gonet.NewUDPConn(&wq, ep)
		connID := e.trackConn(conn, "")
		go func() {
			defer e.untrackConn(connID)
			e.bypassUDP(conn, dstAddr, dstPort)
		}()
		return
	}

	var appPath string
	shouldProxy := true
	if e.rules.NeedProcessLookup() {
		appPath, _ = e.procInfo.FindProcess("udp", srcPort)
		shouldProxy = !e.isSelf(appPath) && e.rules.ShouldProxy(appPath)
	}

	// Voice/video UDP (high ports) from known apps bypass TUN proxy
	// to avoid latency from UDP-over-TLS/TCP wrapping.
	if shouldProxy && dstPort >= 50000 && isVoiceApp(appPath) {
		shouldProxy = false
		log.Printf("[tun] UDP %s:%d from %s → bypass (voice)", dstAddr, dstPort, appPath)
	}

	var wq waiter.Queue
	ep, udpErr := r.CreateEndpoint(&wq)
	if udpErr != nil {
		return
	}

	conn := gonet.NewUDPConn(&wq, ep)
	connID := e.trackConn(conn, appPath)

	if shouldProxy {
		go func() {
			defer e.untrackConn(connID)
			e.proxyUDP(conn, dstAddr, dstPort, appPath)
		}()
	} else {
		go func() {
			defer e.untrackConn(connID)
			e.bypassUDP(conn, dstAddr, dstPort)
		}()
	}
}

func (e *Engine) proxyUDP(local net.Conn, dstAddr string, dstPort uint16, appPath string) {
	defer local.Close()

	e.mu.Lock()
	tr := e.transport
	e.mu.Unlock()

	if tr != nil {
		e.proxyUDPTransport(local, tr, dstAddr, dstPort)
	} else {
		e.proxyUDPLegacy(local, dstAddr, dstPort)
	}
}

func (e *Engine) proxyUDPTransport(local net.Conn, tr transport.Transport, dstAddr string, dstPort uint16) {
	stream, err := tr.OpenStream(0x02, dstAddr, dstPort)
	if err != nil {
		if !strings.Contains(err.Error(), "connect rejected") {
			e.streamOpenFailures.Add(1)
		}
		log.Printf("[tun] open UDP stream failed for %s:%d: %v", dstAddr, dstPort, err)
		if strings.Contains(err.Error(), "machine id rejected") {
			log.Printf("[tun] DEVICE BINDING CONFLICT: server reports this key is bound to a different machine fingerprint — stopping engine. (dst=%s:%d UDP)", dstAddr, dstPort)
			e.mu.Lock()
			e.lastError = "Device is bound to a different machine"
			e.stopLocked()
			e.mu.Unlock()
		}
		return
	}
	e.streamOpenFailures.Store(0)
	defer stream.Close()

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			if _, err := stream.Write(buf[:n]); err != nil {
				return
			}
			e.meter.Add(0, int64(n))
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			n, err := stream.Read(buf)
			if err != nil {
				return
			}
			e.meter.Add(int64(n), 0)
			if _, err := local.Write(buf[:n]); err != nil {
				return
			}
		}
	}()

	<-done
	local.Close()
	stream.Close()
	<-done
}

func (e *Engine) proxyUDPLegacy(local net.Conn, dstAddr string, dstPort uint16) {
	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		return
	}

	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err := tlsConn.Handshake(); err != nil {
		rawConn.Close()
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeUDP); err != nil {
		return
	}

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			if err := proto.WriteUDPFrame(tlsConn, dstAddr, dstPort, buf[:n]); err != nil {
				return
			}
			e.meter.Add(0, int64(n))
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, _, payload, err := proto.ReadUDPFrame(tlsConn)
			if err != nil {
				return
			}
			e.meter.Add(int64(len(payload)), 0)
			if _, err := local.Write(payload); err != nil {
				return
			}
		}
	}()

	<-done
}

func (e *Engine) bypassUDP(local net.Conn, dstAddr string, dstPort uint16) {
	defer local.Close()

	// Use protected dialer for bypass to avoid TUN routing loop
	remote, err := protectedDial("udp", fmt.Sprintf("%s:%d", dstAddr, dstPort))
	if err != nil {
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			remote.Write(buf[:n])
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			remote.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := remote.Read(buf)
			if err != nil {
				return
			}
			local.Write(buf[:n])
		}
	}()
	<-done
}

// isVoiceApp returns true for apps that use UDP voice/video
// which should bypass TUN proxy to avoid latency.
func isVoiceApp(appPath string) bool {
	lower := strings.ToLower(appPath)
	voiceApps := []string{"discord", "telegram", "slack", "zoom", "teams"}
	for _, app := range voiceApps {
		if strings.Contains(lower, app) {
			return true
		}
	}
	return false
}

func (e *Engine) isSelf(appPath string) bool {
	if appPath == "" || e.selfPath == "" {
		return false
	}
	return strings.EqualFold(appPath, e.selfPath)
}

const tcpIdleTimeout = 2 * time.Minute

// idleRelay copies data bidirectionally between c1 and c2.
// If no data flows in EITHER direction for tcpIdleTimeout, both connections
// are closed. This prevents goroutine leaks from idle TCP connections.
// onBytes is optional (can be nil) for traffic counting.
func idleRelay(c1, c2 net.Conn, onBytes func(in, out int64)) {
	var active int64 = time.Now().UnixNano()

	errc := make(chan error, 2)
	pipe := func(dst, src net.Conn, counter func(int64)) {
		buf := make([]byte, 32*1024)
		for {
			src.SetReadDeadline(time.Now().Add(tcpIdleTimeout))
			n, err := src.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&active, time.Now().UnixNano())
				if _, werr := dst.Write(buf[:n]); werr != nil {
					errc <- werr
					return
				}
				if counter != nil {
					counter(int64(n))
				}
			}
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					if time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&active)) < tcpIdleTimeout {
						continue
					}
				}
				errc <- err
				return
			}
		}
	}

	go pipe(c1, c2, func(n int64) {
		if onBytes != nil {
			onBytes(n, 0)
		}
	})
	go pipe(c2, c1, func(n int64) {
		if onBytes != nil {
			onBytes(0, n)
		}
	})
	<-errc
	c1.Close()
	c2.Close()
	<-errc
}

// streamRelay copies data bidirectionally between a net.Conn and a
// transport.Stream with idle timeout and traffic counting. Returns a
// short reason string for whoever killed the stream first — one of
// `download: <err>` (stream.Read / local.Write failed) or
// `upload: <err>` (local.Read / stream.Write failed). The caller can
// use this for post-mortem logging.
func streamRelay(local net.Conn, stream transport.Stream, onBytes func(in, out int64)) string {
	var active int64 = time.Now().UnixNano()

	type relayErr struct {
		dir string
		err error
	}
	errc := make(chan relayErr, 2)
	// stream → local (download)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&active, time.Now().UnixNano())
				if _, werr := local.Write(buf[:n]); werr != nil {
					errc <- relayErr{"download", werr}
					return
				}
				if onBytes != nil {
					onBytes(int64(n), 0)
				}
			}
			if err != nil {
				errc <- relayErr{"download", err}
				return
			}
		}
	}()
	// local → stream (upload)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			local.SetReadDeadline(time.Now().Add(tcpIdleTimeout))
			n, err := local.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&active, time.Now().UnixNano())
				if _, werr := stream.Write(buf[:n]); werr != nil {
					errc <- relayErr{"upload", werr}
					return
				}
				if onBytes != nil {
					onBytes(0, int64(n))
				}
			}
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					if time.Duration(time.Now().UnixNano()-atomic.LoadInt64(&active)) < tcpIdleTimeout {
						continue
					}
				}
				errc <- relayErr{"upload", err}
				return
			}
		}
	}()
	first := <-errc
	local.Close()
	stream.Close()
	<-errc
	return fmt.Sprintf("%s: %v", first.dir, first.err)
}

func dialHelper(addr string) (net.Conn, error) {
	if conn, err := net.DialTimeout("unix", addr, 2*time.Second); err == nil {
		return conn, nil
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}
