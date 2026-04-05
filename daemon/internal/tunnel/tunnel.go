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
	healthInterval = 30 * time.Second
)

type Status string

const (
	Disconnected Status = "disconnected"
	Connected    Status = "connected"
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
}

func New(meter *dstats.RateMeter) *Tunnel {
	return &Tunnel{status: Disconnected, meter: meter, conns: make(map[uint64]net.Conn)}
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
	reconnectDelay    = 3 * time.Second
	maxReconnects     = 5
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
			log.Printf("[tunnel] transport closed, attempting reconnect...")
			if t.reconnectTransport() {
				doneCh = t.transportDone()
				failures = 0
				continue
			}
			log.Printf("[tunnel] reconnect failed, disconnecting")
			t.mu.Lock()
			t.lastError = "Connection lost, please reconnect"
			t.stopLocked()
			t.mu.Unlock()
			return
		case <-ticker.C:
			t.mu.Lock()
			addr := t.serverAddr
			key := t.key
			t.mu.Unlock()

			if err := verifyServer(addr, key); err != nil {
				failures++
				log.Printf("[tunnel] health check failed (%d/%d): %v", failures, maxRetries, err)
				if failures >= maxRetries {
					log.Printf("[tunnel] server unreachable, disconnecting")
					t.mu.Lock()
					t.lastError = "Server temporarily unavailable, try again later"
					t.stopLocked()
					t.mu.Unlock()
					return
				}
			} else {
				if failures > 0 {
					log.Printf("[tunnel] health check recovered after %d failures", failures)
				}
				failures = 0
			}
		}
	}
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
