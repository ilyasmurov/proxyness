package udp

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	StreamTypeTCP byte = 0x01
	StreamTypeUDP byte = 0x02
)

// StreamOpenMsg is the payload of a MsgStreamOpen packet.
type StreamOpenMsg struct {
	StreamType byte   // StreamTypeTCP or StreamTypeUDP
	Addr       string // destination host (IP or domain)
	Port       uint16
}

// Encode serializes StreamOpenMsg: streamType(1) + address encoding.
// Address encoding: addrType(1) + addr + port(2).
func (m *StreamOpenMsg) Encode() []byte {
	var buf []byte

	if ip := net.ParseIP(m.Addr); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = make([]byte, 1+1+4+2)
			buf[0] = m.StreamType
			buf[1] = 0x01 // IPv4
			copy(buf[2:6], ip4)
			binary.BigEndian.PutUint16(buf[6:8], m.Port)
		} else {
			buf = make([]byte, 1+1+16+2)
			buf[0] = m.StreamType
			buf[1] = 0x04 // IPv6
			copy(buf[2:18], ip.To16())
			binary.BigEndian.PutUint16(buf[18:20], m.Port)
		}
	} else {
		buf = make([]byte, 1+1+1+len(m.Addr)+2)
		buf[0] = m.StreamType
		buf[1] = 0x03 // domain
		buf[2] = byte(len(m.Addr))
		copy(buf[3:3+len(m.Addr)], m.Addr)
		binary.BigEndian.PutUint16(buf[3+len(m.Addr):], m.Port)
	}

	return buf
}

// DecodeStreamOpen parses a StreamOpenMsg from raw bytes.
func DecodeStreamOpen(data []byte) (*StreamOpenMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("stream open too short")
	}

	msg := &StreamOpenMsg{StreamType: data[0]}
	addrType := data[1]

	switch addrType {
	case 0x01: // IPv4
		if len(data) < 8 {
			return nil, fmt.Errorf("ipv4 too short")
		}
		msg.Addr = net.IP(data[2:6]).String()
		msg.Port = binary.BigEndian.Uint16(data[6:8])
	case 0x04: // IPv6
		if len(data) < 20 {
			return nil, fmt.Errorf("ipv6 too short")
		}
		msg.Addr = net.IP(data[2:18]).String()
		msg.Port = binary.BigEndian.Uint16(data[18:20])
	case 0x03: // domain
		dlen := int(data[2])
		if len(data) < 3+dlen+2 {
			return nil, fmt.Errorf("domain too short")
		}
		msg.Addr = string(data[3 : 3+dlen])
		msg.Port = binary.BigEndian.Uint16(data[3+dlen : 5+dlen])
	default:
		return nil, fmt.Errorf("unknown addr type: 0x%02x", addrType)
	}

	return msg, nil
}
