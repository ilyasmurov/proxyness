package tun

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
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

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/transport"
	"smurov-proxy/pkg/machineid"
	"smurov-proxy/pkg/proto"
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
	conns   map[uint64]net.Conn
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
		conns:    make(map[uint64]net.Conn),
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

// UpdateRules applies new rules and closes all active connections so apps
// reconnect with the updated routing policy.
func (e *Engine) UpdateRules(data []byte) error {
	if err := e.rules.FromJSON(data); err != nil {
		return err
	}
	e.closeAllConns()
	return nil
}

func (e *Engine) trackConn(c net.Conn) uint64 {
	e.connsMu.Lock()
	defer e.connsMu.Unlock()
	e.connSeq++
	id := e.connSeq
	e.conns[id] = c
	return id
}

func (e *Engine) untrackConn(id uint64) {
	e.connsMu.Lock()
	defer e.connsMu.Unlock()
	delete(e.conns, id)
}

func (e *Engine) closeAllConns() {
	e.connsMu.Lock()
	snapshot := make(map[uint64]net.Conn, len(e.conns))
	for k, v := range e.conns {
		snapshot[k] = v
	}
	e.connsMu.Unlock()

	for _, c := range snapshot {
		c.Close()
	}
	log.Printf("[tun] closed %d connections after rules update", len(snapshot))
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
		log.Printf("[tun] already active, restarting...")
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

	nat := NewNATTable(func(pkt []byte) {
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(pkt)))
		e.helperWriteMu.Lock()
		defer e.helperWriteMu.Unlock()
		e.mu.Lock()
		conn := e.helperConn
		e.mu.Unlock()
		if conn != nil {
			conn.Write(lenBuf)
			conn.Write(pkt)
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
	go e.bridgeInbound(helperConn, ep)
	go e.bridgeOutbound(ctx, helperConn, ep)

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

	if e.stopHealth != nil {
		close(e.stopHealth)
		e.stopHealth = nil
	}

	// Cancel bridge goroutines
	if e.bridgeCancel != nil {
		e.bridgeCancel()
		e.bridgeCancel = nil
	}

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

// bridgeInbound reads framed IP packets from helper and injects into gVisor stack.
func (e *Engine) bridgeInbound(r io.Reader, ep *channel.Endpoint) {
	log.Printf("[tun] bridgeInbound started")
	lenBuf := make([]byte, 2)
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

		data := make([]byte, pktLen)
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
func (e *Engine) bridgeOutbound(ctx context.Context, conn net.Conn, ep *channel.Endpoint) {
	log.Printf("[tun] bridgeOutbound started")
	lenBuf := make([]byte, 2)
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
		data := buf.Flatten()
		pkt.DecRef()

		binary.BigEndian.PutUint16(lenBuf, uint16(len(data)))
		e.helperWriteMu.Lock()
		_, err1 := conn.Write(lenBuf)
		if err1 == nil {
			_, err1 = conn.Write(data)
		}
		e.helperWriteMu.Unlock()
		if err1 != nil {
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

func (e *Engine) reconnectTransport() bool {
	e.mu.Lock()
	factory := e.transportFactory
	serverAddr := e.serverAddr
	key := e.key
	mid := e.machineID
	if e.transport != nil {
		e.transport.Close()
		e.transport = nil
	}
	e.mu.Unlock()

	if factory == nil {
		return false
	}

	const maxReconnects = 5
	const reconnectDelay = 3 * time.Second

	for attempt := 1; attempt <= maxReconnects; attempt++ {
		select {
		case <-e.stopHealth:
			return false
		default:
		}

		if attempt > 1 {
			time.Sleep(reconnectDelay)
		}

		log.Printf("[tun] reconnect attempt %d/%d", attempt, maxReconnects)
		tr := factory()
		if err := tr.Connect(serverAddr, key, mid); err != nil {
			log.Printf("[tun] reconnect attempt %d failed: %v", attempt, err)
			tr.Close()
			if strings.Contains(err.Error(), "invalid key") || strings.Contains(err.Error(), "machine id rejected") {
				return false
			}
			continue
		}

		e.mu.Lock()
		e.transport = tr
		e.mu.Unlock()
		log.Printf("[tun] reconnected via %s", tr.Mode())
		return true
	}
	return false
}

func (e *Engine) healthLoop() {
	const maxFailures = 3
	ticker := time.NewTicker(5 * time.Second) // was 30s — needed for fast D2 detection
	defer ticker.Stop()

	doneCh := e.transportDone()
	failures := 0
	for {
		select {
		case <-e.stopHealth:
			return

		case <-doneCh:
			// D1 — transport closed: engage kill switch, then try to reconnect.
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
			// D3 — stream-failure detector. transport.Alive() is too
			// optimistic for TLS (reports "alive" even when the OS has
			// dropped all routes), so we also watch the OpenStream
			// failure counter. Apps retry aggressively, so once the
			// network is down the counter races past the threshold in
			// ~1s. When it does, we force-close the transport so the
			// <-doneCh: branch picks up the reconnect naturally on the
			// next iteration.
			if e.streamOpenFailures.Load() >= streamFailureThreshold {
				log.Printf("[tun] D3: %d consecutive stream failures, forcing transport close",
					e.streamOpenFailures.Load())
				e.streamOpenFailures.Store(0)
				e.setReconnecting()
				e.mu.Lock()
				if e.transport != nil {
					e.transport.Close()
				}
				e.mu.Unlock()
				continue
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
	connID := e.trackConn(conn)
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
		e.proxyTCPTransport(local, tr, dstAddr, dstPort)
	} else {
		e.proxyTCPLegacy(local, dstAddr, dstPort)
	}
}

func (e *Engine) proxyTCPTransport(local net.Conn, tr transport.Transport, dstAddr string, dstPort uint16) {
	stream, err := tr.OpenStream(0x01, dstAddr, dstPort)
	if err != nil {
		e.streamOpenFailures.Add(1)
		log.Printf("[tun] open TCP stream failed for %s:%d: %v", dstAddr, dstPort, err)
		if strings.Contains(err.Error(), "machine id rejected") {
			e.mu.Lock()
			e.lastError = "Device is bound to a different machine"
			e.stopLocked()
			e.mu.Unlock()
		}
		return
	}
	e.streamOpenFailures.Store(0)

	streamRelay(local, stream, func(in, out int64) {
		e.meter.Add(in, out)
	})
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
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
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
		connID := e.trackConn(conn)
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
	connID := e.trackConn(conn)

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
		e.streamOpenFailures.Add(1)
		log.Printf("[tun] open UDP stream failed for %s:%d: %v", dstAddr, dstPort, err)
		if strings.Contains(err.Error(), "machine id rejected") {
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
// transport.Stream with idle timeout and traffic counting.
func streamRelay(local net.Conn, stream transport.Stream, onBytes func(in, out int64)) {
	var active int64 = time.Now().UnixNano()

	errc := make(chan error, 2)
	// stream → local (download)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stream.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&active, time.Now().UnixNano())
				if _, werr := local.Write(buf[:n]); werr != nil {
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
	// local → stream (upload)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			local.SetReadDeadline(time.Now().Add(tcpIdleTimeout))
			n, err := local.Read(buf)
			if n > 0 {
				atomic.StoreInt64(&active, time.Now().UnixNano())
				if _, werr := stream.Write(buf[:n]); werr != nil {
					errc <- werr
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
				errc <- err
				return
			}
		}
	}()
	<-errc
	local.Close()
	stream.Close()
	<-errc
}

func dialHelper(addr string) (net.Conn, error) {
	if conn, err := net.DialTimeout("unix", addr, 2*time.Second); err == nil {
		return conn, nil
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}
