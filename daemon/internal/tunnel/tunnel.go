package tunnel

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"smurov-proxy/daemon/internal/socks5"
	"smurov-proxy/pkg/proto"
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
}

func New() *Tunnel {
	return &Tunnel{status: Disconnected}
}

func (t *Tunnel) Start(listenAddr, serverAddr, key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.status != Disconnected {
		return fmt.Errorf("tunnel already %s", t.status)
	}

	// Verify server is reachable and key is valid before starting
	log.Printf("[tunnel] verifying connection to %s...", serverAddr)
	tlsConn, err := tls.Dial("tcp", serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return fmt.Errorf("server unreachable: %v", err)
	}
	if err := proto.WriteAuth(tlsConn, key); err != nil {
		tlsConn.Close()
		return fmt.Errorf("auth failed: %v", err)
	}
	ok, err := proto.ReadResult(tlsConn)
	tlsConn.Close()
	if err != nil {
		return fmt.Errorf("auth failed: %v", err)
	}
	if !ok {
		return fmt.Errorf("invalid key")
	}
	log.Printf("[tunnel] key verified OK")

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	t.listener = ln
	t.serverAddr = serverAddr
	t.key = key
	t.status = Connected
	t.startTime = time.Now()

	go t.acceptLoop(ln)
	return nil
}

func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.listener != nil {
		t.listener.Close()
		t.listener = nil
	}
	t.status = Disconnected
}

func (t *Tunnel) GetStatus() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Tunnel) Uptime() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status != Connected {
		return 0
	}
	return int64(time.Since(t.startTime).Seconds())
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
	proto.Relay(conn, tlsConn)
}
