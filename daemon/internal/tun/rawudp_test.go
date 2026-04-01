package tun

import (
	"encoding/binary"
	"net"
	"testing"
)

func buildTestUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	udpLen := 8 + len(payload)
	totalLen := 20 + udpLen
	pkt := make([]byte, totalLen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[9] = 17
	copy(pkt[12:16], srcIP.To4())
	copy(pkt[16:20], dstIP.To4())
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(udpLen))
	copy(pkt[28:], payload)
	return pkt
}

func TestRawUDPHandler_QUICBlock(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 443, []byte("quic"))
	if !h.Handle(pkt) {
		t.Error("UDP port 443 should be handled (dropped)")
	}
}

func TestRawUDPHandler_DNS(t *testing.T) {
	nat := NewNATTable(func(pkt []byte) {})
	defer nat.Close()
	h := &RawUDPHandler{rules: NewRules(), nat: nat}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{8, 8, 8, 8}, 5000, 53, []byte("dns"))
	if !h.Handle(pkt) {
		t.Error("DNS should be handled (bypassed)")
	}
}

func TestRawUDPHandler_TCPIgnored(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[9] = 6 // TCP
	if h.Handle(pkt) {
		t.Error("TCP should not be handled")
	}
}

func TestRawUDPHandler_IPv6Ignored(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := make([]byte, 40)
	pkt[0] = 0x60 // IPv6
	if h.Handle(pkt) {
		t.Error("IPv6 should not be handled")
	}
}

func TestRawUDPHandler_ProxyOnly_NotSelected(t *testing.T) {
	nat := NewNATTable(func(pkt []byte) {})
	defer nat.Close()
	rules := NewRules()
	rules.SetMode(ModeProxyOnly)
	rules.SetApps([]string{"/applications/telegram.app"})
	h := &RawUDPHandler{rules: rules, nat: nat, procInfo: &mockProcInfo{path: "/usr/bin/curl"}}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 8080, []byte("data"))
	if !h.Handle(pkt) {
		t.Error("non-selected app should be bypassed")
	}
}

func TestRawUDPHandler_ProxyOnly_Selected(t *testing.T) {
	rules := NewRules()
	rules.SetMode(ModeProxyOnly)
	rules.SetApps([]string{"/applications/telegram.app"})
	h := &RawUDPHandler{rules: rules, procInfo: &mockProcInfo{path: "/applications/telegram.app/contents/macos/telegram"}}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 8080, []byte("data"))
	if h.Handle(pkt) {
		t.Error("selected app should go to gVisor (proxy)")
	}
}

type mockProcInfo struct {
	path string
}

func (m *mockProcInfo) FindProcess(network string, localPort uint16) (string, error) {
	return m.path, nil
}
