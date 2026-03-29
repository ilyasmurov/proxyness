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
		log.Printf("socks5 handshake: %v", err)
		return
	}

	tlsConn, err := tls.Dial("tcp", t.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		socks5.SendFailure(conn)
		log.Printf("tls dial %s: %v", t.serverAddr, err)
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, t.key); err != nil {
		socks5.SendFailure(conn)
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		return
	}

	if err := proto.WriteConnect(tlsConn, req.Addr, req.Port); err != nil {
		socks5.SendFailure(conn)
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		return
	}

	socks5.SendSuccess(conn)
	proto.Relay(conn, tlsConn)
}
