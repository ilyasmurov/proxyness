package tun

import (
	"log"
	"net"
	"strings"
)

// RawUDPHandler is a decision engine for UDP packets that runs BEFORE gVisor.
// Called from bridgeInbound with a raw IPv4 packet. Returns true if the packet
// was handled (bypass or drop), false if gVisor should process it (proxy).
type RawUDPHandler struct {
	nat      *NATTable
	rules    *Rules
	procInfo ProcessInfo
	selfPath string
}

// Handle inspects a raw IP packet and decides whether to handle it directly
// (bypass via NAT or silently drop) or let gVisor process it for proxying.
func (h *RawUDPHandler) Handle(pkt []byte) bool {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return false
	}

	proto, srcIP, dstIP, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil || proto != 17 {
		return false
	}

	if len(pkt) < hdrLen+8 {
		return false
	}

	srcPort, dstPort, payload, err := ParseUDPHeader(pkt[hdrLen:])
	if err != nil {
		return false
	}

	// QUIC block — drop UDP 443 to force TCP/HTTPS fallback
	if dstPort == 443 {
		return true
	}

	// DNS always bypasses — system resolver must work
	if dstPort == 53 {
		if h.nat != nil {
			h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
		}
		return true
	}

	// No per-app rules configured — let gVisor proxy everything
	if !h.rules.NeedProcessLookup() {
		return false
	}

	// Look up which app owns this packet
	var appPath string
	if h.procInfo != nil {
		appPath, _ = h.procInfo.FindProcess("udp", srcPort)
	}

	// Self-detection — daemon's own traffic always bypasses
	if appPath != "" && h.selfPath != "" && strings.EqualFold(appPath, h.selfPath) {
		if h.nat != nil {
			h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
		}
		return true
	}

	shouldProxy := h.rules.ShouldProxy(appPath)

	// Voice/video UDP from known apps bypasses proxy to avoid latency
	if shouldProxy && dstPort >= 50000 && isVoiceApp(appPath) {
		shouldProxy = false
		log.Printf("[tun] raw UDP %s:%d from %s → bypass (voice)", net.IP(dstIP), dstPort, appPath)
	}

	if shouldProxy {
		return false
	}

	// Bypass — send through NAT table
	if h.nat != nil {
		h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
	}
	return true
}
