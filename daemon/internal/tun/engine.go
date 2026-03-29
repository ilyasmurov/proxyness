package tun

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"smurov-proxy/pkg/proto"
)

type Status string

const (
	StatusInactive Status = "inactive"
	StatusActive   Status = "active"
)

type Engine struct {
	mu         sync.Mutex
	status     Status
	serverAddr string
	key        string
	rules      *Rules
	procInfo   ProcessInfo
	stack      *stack.Stack
	helperAddr string
}

func NewEngine() *Engine {
	return &Engine{
		status: StatusInactive,
		rules:  NewRules(),
	}
}

func (e *Engine) GetStatus() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) GetRules() *Rules {
	return e.rules
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
		return fmt.Errorf("TUN already active")
	}

	if err := e.requestHelper(req.HelperAddr, "create"); err != nil {
		return fmt.Errorf("helper create: %w", err)
	}

	s, _, err := newStack(1500)
	if err != nil {
		e.requestHelper(req.HelperAddr, "destroy")
		return fmt.Errorf("create stack: %w", err)
	}

	e.stack = s
	e.serverAddr = req.ServerAddr
	e.key = req.Key
	e.helperAddr = req.HelperAddr
	e.procInfo = newProcessInfo()

	tcpFwd := tcp.NewForwarder(s, 0, 2048, func(r *tcp.ForwarderRequest) {
		e.handleTCP(r)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		e.handleUDP(r)
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	e.status = StatusActive
	log.Printf("[tun] engine started, server=%s", req.ServerAddr)
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status == StatusInactive {
		return nil
	}

	if e.stack != nil {
		e.stack.Close()
		e.stack = nil
	}

	e.requestHelper(e.helperAddr, "destroy")
	e.status = StatusInactive
	log.Printf("[tun] engine stopped")
	return nil
}

func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	appPath, _ := e.procInfo.FindProcess("tcp", srcPort)
	shouldProxy := e.rules.ShouldProxy(appPath)

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
	defer conn.Close()

	if shouldProxy {
		e.proxyTCP(conn, dstAddr, dstPort)
	} else {
		e.bypassTCP(conn, dstAddr, dstPort)
	}
}

func (e *Engine) proxyTCP(local net.Conn, dstAddr string, dstPort uint16) {
	tlsConn, err := tls.Dial("tcp", e.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[tun] tls dial failed: %v", err)
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

	proto.Relay(local, tlsConn)
}

func (e *Engine) bypassTCP(local net.Conn, dstAddr string, dstPort uint16) {
	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", dstAddr, dstPort), 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()
	proto.Relay(local, target)
}

func (e *Engine) handleUDP(r *udp.ForwarderRequest) {
	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	appPath, _ := e.procInfo.FindProcess("udp", srcPort)
	shouldProxy := e.rules.ShouldProxy(appPath)

	var wq waiter.Queue
	ep, udpErr := r.CreateEndpoint(&wq)
	if udpErr != nil {
		return
	}

	conn := gonet.NewUDPConn(&wq, ep)

	if shouldProxy {
		go e.proxyUDP(conn, dstAddr, dstPort)
	} else {
		go e.bypassUDP(conn, dstAddr, dstPort)
	}
}

func (e *Engine) proxyUDP(local net.Conn, dstAddr string, dstPort uint16) {
	defer local.Close()

	tlsConn, err := tls.Dial("tcp", e.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
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
	if err := proto.WriteConnect(tlsConn, dstAddr, dstPort); err != nil {
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
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, _, payload, err := proto.ReadUDPFrame(tlsConn)
			if err != nil {
				return
			}
			if _, err := local.Write(payload); err != nil {
				return
			}
		}
	}()

	<-done
}

func (e *Engine) bypassUDP(local net.Conn, dstAddr string, dstPort uint16) {
	defer local.Close()

	raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstAddr, dstPort))
	if err != nil {
		return
	}
	remote, err := net.DialUDP("udp", nil, raddr)
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

func (e *Engine) requestHelper(addr, action string) error {
	conn, err := dialHelper(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := map[string]string{"action": action}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("helper: %s", resp.Error)
	}
	return nil
}

func dialHelper(addr string) (net.Conn, error) {
	if conn, err := net.DialTimeout("unix", addr, 2*time.Second); err == nil {
		return conn, nil
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}
