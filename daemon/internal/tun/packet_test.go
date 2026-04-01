package tun

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestParseIPv4Header_UDP(t *testing.T) {
	pkt := make([]byte, 28)
	pkt[0] = 0x45                                   // version=4, IHL=5 (20 bytes)
	pkt[9] = 17                                     // protocol = UDP
	copy(pkt[12:16], net.IP{10, 0, 0, 1}.To4())
	copy(pkt[16:20], net.IP{8, 8, 8, 8}.To4())
	pkt[20] = 0x12
	pkt[21] = 0x34 // src port 4660
	pkt[22] = 0x00
	pkt[23] = 0x35 // dst port 53

	proto, srcIP, dstIP, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 17 {
		t.Errorf("proto = %d, want 17", proto)
	}
	if !srcIP.Equal(net.IP{10, 0, 0, 1}) {
		t.Errorf("srcIP = %v", srcIP)
	}
	if !dstIP.Equal(net.IP{8, 8, 8, 8}) {
		t.Errorf("dstIP = %v", dstIP)
	}
	if hdrLen != 20 {
		t.Errorf("hdrLen = %d, want 20", hdrLen)
	}
}

func TestParseIPv4Header_TCP(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 6
	proto, _, _, _, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 6 {
		t.Errorf("proto = %d, want 6", proto)
	}
}

func TestParseIPv4Header_TooShort(t *testing.T) {
	_, _, _, _, err := ParseIPv4Header([]byte{0x45, 0x00})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseUDPHeader(t *testing.T) {
	udp := []byte{
		0x12, 0x34, // src port 4660
		0x00, 0x35, // dst port 53
		0x00, 0x0B, // length 11 (8 header + 3 payload)
		0x00, 0x00, // checksum
		0xAA, 0xBB, 0xCC, // payload
	}
	srcPort, dstPort, payload, err := ParseUDPHeader(udp)
	if err != nil {
		t.Fatal(err)
	}
	if srcPort != 4660 {
		t.Errorf("srcPort = %d, want 4660", srcPort)
	}
	if dstPort != 53 {
		t.Errorf("dstPort = %d, want 53", dstPort)
	}
	if len(payload) != 3 || payload[0] != 0xAA {
		t.Errorf("payload = %x", payload)
	}
}

func TestParseUDPHeader_TooShort(t *testing.T) {
	_, _, _, err := ParseUDPHeader([]byte{0x00, 0x35})
	if err == nil {
		t.Error("expected error for short UDP header")
	}
}

func TestBuildUDPPacket(t *testing.T) {
	srcIP := net.IP{8, 8, 8, 8}
	dstIP := net.IP{10, 0, 0, 1}
	payload := []byte{0xAA, 0xBB, 0xCC}

	pkt := BuildUDPPacket(srcIP, dstIP, 53, 4660, payload)

	proto, pSrc, pDst, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 17 {
		t.Errorf("proto = %d", proto)
	}
	if !pSrc.Equal(srcIP) {
		t.Errorf("srcIP = %v", pSrc)
	}
	if !pDst.Equal(dstIP) {
		t.Errorf("dstIP = %v", pDst)
	}

	sp, dp, pl, err := ParseUDPHeader(pkt[hdrLen:])
	if err != nil {
		t.Fatal(err)
	}
	if sp != 53 || dp != 4660 {
		t.Errorf("ports = %d:%d", sp, dp)
	}
	if len(pl) != 3 || pl[0] != 0xAA {
		t.Errorf("payload = %x", pl)
	}

	totalLen := binary.BigEndian.Uint16(pkt[2:4])
	if int(totalLen) != len(pkt) {
		t.Errorf("totalLen = %d, packet = %d", totalLen, len(pkt))
	}
}
