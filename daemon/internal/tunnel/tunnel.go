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

type Tunnel struct {
	mu         sync.Mutex
	status     Status
	serverAddr string
	key        string
	listener   net.Listener
	startTime  time.Time
	stopHealth chan struct{}
	lastError  string
	meter      *dstats.RateMeter
}

func New(meter *dstats.RateMeter) *Tunnel {
	return &Tunnel{status: Disconnected, meter: meter}
}

func (t *Tunnel) Start(listenAddr, serverAddr, key string) error {
	t.mu.Lock()
	if t.status != Disconnected {
		t.mu.Unlock()
		return fmt.Errorf("tunnel already %s", t.status)
	}
	t.lastError = ""
	t.mu.Unlock()

	// Verify with retries (no lock held so status polling still works)
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
		// Don't retry auth errors — wrong key won't fix itself
		if strings.Contains(lastErr.Error(), "invalid key") {
			return lastErr
		}
		log.Printf("[tunnel] attempt %d failed: %v", attempt, lastErr)
	}
	if lastErr != nil {
		return fmt.Errorf("server temporarily unavailable, try again later")
	}

	log.Printf("[tunnel] key verified OK")

	t.mu.Lock()
	defer t.mu.Unlock()

	// Re-check in case Stop() was called during retries
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

func (t *Tunnel) healthLoop() {
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()

	failures := 0
	for {
		select {
		case <-t.stopHealth:
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
	defer conn.Close()

	req, err := socks5.Handshake(conn)
	if err != nil {
		log.Printf("[socks5] handshake failed: %v", err)
		return
	}
	target := fmt.Sprintf("%s:%d", req.Addr, req.Port)
	log.Printf("[tunnel] new request: %s", target)

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

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] msg type write failed: %v", err)
		return
	}

	if err := proto.WriteConnect(tlsConn, req.Addr, req.Port); err != nil {
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
