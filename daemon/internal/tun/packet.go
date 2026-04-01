package tun

import (
	"encoding/binary"
	"errors"
	"net"
)

var (
	errPacketTooShort = errors.New("packet too short")
	errNotIPv4        = errors.New("not IPv4")
)

// ParseIPv4Header extracts protocol, source/destination IPs, and header length
// from a raw IPv4 packet. Zero allocations — returns slices into pkt.
func ParseIPv4Header(pkt []byte) (proto uint8, srcIP, dstIP net.IP, hdrLen int, err error) {
	if len(pkt) < 20 {
		return 0, nil, nil, 0, errPacketTooShort
	}
	if pkt[0]>>4 != 4 {
		return 0, nil, nil, 0, errNotIPv4
	}
	hdrLen = int(pkt[0]&0x0F) * 4
	if len(pkt) < hdrLen {
		return 0, nil, nil, 0, errPacketTooShort
	}
	proto = pkt[9]
	srcIP = net.IP(pkt[12:16])
	dstIP = net.IP(pkt[16:20])
	return proto, srcIP, dstIP, hdrLen, nil
}

// ParseUDPHeader extracts ports and payload from a UDP segment.
func ParseUDPHeader(udp []byte) (srcPort, dstPort uint16, payload []byte, err error) {
	if len(udp) < 8 {
		return 0, 0, nil, errPacketTooShort
	}
	srcPort = binary.BigEndian.Uint16(udp[0:2])
	dstPort = binary.BigEndian.Uint16(udp[2:4])
	length := int(binary.BigEndian.Uint16(udp[4:6]))
	if length < 8 || len(udp) < length {
		return srcPort, dstPort, nil, errPacketTooShort
	}
	payload = udp[8:length]
	return srcPort, dstPort, payload, nil
}

// BuildUDPPacket constructs a complete IPv4+UDP packet with checksums.
func BuildUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	src4 := srcIP.To4()
	dst4 := dstIP.To4()

	udpLen := 8 + len(payload)
	totalLen := 20 + udpLen
	pkt := make([]byte, totalLen)

	// IPv4 header (20 bytes)
	pkt[0] = 0x45 // version=4, IHL=5
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[8] = 64 // TTL
	pkt[9] = 17 // protocol = UDP
	copy(pkt[12:16], src4)
	copy(pkt[16:20], dst4)
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:20]))

	// UDP header
	udp := pkt[20:]
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	copy(udp[8:], payload)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(src4, dst4, udp[:udpLen]))

	return pkt
}

func ipChecksum(hdr []byte) uint16 {
	var sum uint32
	for i := 0; i < len(hdr)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}
	for sum > 0xFFFF {
		sum = (sum >> 16) + (sum & 0xFFFF)
	}
	return ^uint16(sum)
}

func udpChecksum(srcIP, dstIP net.IP, udp []byte) uint16 {
	var sum uint32
	sum += uint32(srcIP[0])<<8 | uint32(srcIP[1])
	sum += uint32(srcIP[2])<<8 | uint32(srcIP[3])
	sum += uint32(dstIP[0])<<8 | uint32(dstIP[1])
	sum += uint32(dstIP[2])<<8 | uint32(dstIP[3])
	sum += 17
	sum += uint32(len(udp))
	for i := 0; i < len(udp)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(udp[i : i+2]))
	}
	if len(udp)%2 == 1 {
		sum += uint32(udp[len(udp)-1]) << 8
	}
	for sum > 0xFFFF {
		sum = (sum >> 16) + (sum & 0xFFFF)
	}
	cs := ^uint16(sum)
	if cs == 0 {
		cs = 0xFFFF
	}
	return cs
}
