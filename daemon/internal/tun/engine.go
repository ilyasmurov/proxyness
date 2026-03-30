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

	"smurov-proxy/pkg/proto"
)

type Status string

const (
	StatusInactive Status = "inactive"
	StatusActive   Status = "active"
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
	selfPath     string // daemon's own path — always bypassed to prevent loops
}

func NewEngine() *Engine {
	selfPath, _ := os.Executable()
	return &Engine{
		status:   StatusInactive,
		rules:    NewRules(),
		selfPath: selfPath,
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
	log.Printf("[tun] engine started, server=%s", req.ServerAddr)
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status == StatusInactive {
		return nil
	}

	// Cancel bridge goroutines
	if e.bridgeCancel != nil {
		e.bridgeCancel()
		e.bridgeCancel = nil
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

	e.endpoint = nil
	e.status = StatusInactive
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
		if count%100 == 0 {
			log.Printf("[tun] bridgeInbound injected %d packets (IPv4=%d, IPv6=%d)", count, ipv4Count, ipv6Count)
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
		if _, err := conn.Write(lenBuf); err != nil {
			return
		}
		if _, err := conn.Write(data); err != nil {
			return
		}
	}
}

func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	appPath, _ := e.procInfo.FindProcess("tcp", srcPort)

	// Always bypass daemon's own traffic to prevent routing loops
	shouldProxy := !e.isSelf(appPath) && e.rules.ShouldProxy(appPath)

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
	// Use protected dialer to bypass TUN routes
	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		log.Printf("[tun] protected dial failed: %v", err)
		return
	}

	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		log.Printf("[tun] tls handshake failed: %v", err)
		return
	}

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
	// Use protected dialer to bypass TUN routes
	target, err := protectedDial("tcp", fmt.Sprintf("%s:%d", dstAddr, dstPort))
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

	// Block QUIC (UDP 443) — forces Chrome to fall back to TCP/HTTPS
	if dstPort == 443 {
		return
	}

	appPath, _ := e.procInfo.FindProcess("udp", srcPort)
	shouldProxy := !e.isSelf(appPath) && e.rules.ShouldProxy(appPath)

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

	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		return
	}

	tlsConn := tls.Client(rawConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	defer tlsConn.Close()

	if err := tlsConn.Handshake(); err != nil {
		return
	}

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

func (e *Engine) isSelf(appPath string) bool {
	if appPath == "" || e.selfPath == "" {
		return false
	}
	return strings.EqualFold(appPath, e.selfPath)
}

func dialHelper(addr string) (net.Conn, error) {
	if conn, err := net.DialTimeout("unix", addr, 2*time.Second); err == nil {
		return conn, nil
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}
