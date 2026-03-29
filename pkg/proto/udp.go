package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// UDP frame format: [2 bytes length][addr_type + addr + port][payload]
// Max UDP datagram: 65535 bytes

// WriteUDPFrame writes a single UDP datagram as a framed message.
func WriteUDPFrame(w io.Writer, addr string, port uint16, payload []byte) error {
	addrBytes := encodeAddr(addr, port)
	frameLen := uint16(len(addrBytes) + len(payload))

	buf := make([]byte, 2+len(addrBytes)+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], frameLen)
	copy(buf[2:2+len(addrBytes)], addrBytes)
	copy(buf[2+len(addrBytes):], payload)

	_, err := w.Write(buf)
	return err
}

// ReadUDPFrame reads a single framed UDP datagram.
func ReadUDPFrame(r io.Reader) (addr string, port uint16, payload []byte, err error) {
	lenBuf := make([]byte, 2)
	if _, err = io.ReadFull(r, lenBuf); err != nil {
		return
	}
	frameLen := binary.BigEndian.Uint16(lenBuf)
	if frameLen == 0 {
		err = fmt.Errorf("empty UDP frame")
		return
	}

	frame := make([]byte, frameLen)
	if _, err = io.ReadFull(r, frame); err != nil {
		return
	}

	// Parse addr from frame
	var addrLen int
	addr, port, addrLen, err = decodeAddr(frame)
	if err != nil {
		return
	}
	payload = frame[addrLen:]
	return
}

func encodeAddr(addr string, port uint16) []byte {
	ip := net.ParseIP(addr)
	if ip4 := ip.To4(); ip4 != nil {
		buf := make([]byte, 1+4+2)
		buf[0] = AddrTypeIPv4
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
		return buf
	} else if ip16 := ip.To16(); ip16 != nil {
		buf := make([]byte, 1+16+2)
		buf[0] = AddrTypeIPv6
		copy(buf[1:17], ip16)
		binary.BigEndian.PutUint16(buf[17:], port)
		return buf
	}
	buf := make([]byte, 1+1+len(addr)+2)
	buf[0] = AddrTypeDomain
	buf[1] = byte(len(addr))
	copy(buf[2:2+len(addr)], addr)
	binary.BigEndian.PutUint16(buf[2+len(addr):], port)
	return buf
}

func decodeAddr(data []byte) (addr string, port uint16, totalLen int, err error) {
	if len(data) < 1 {
		err = fmt.Errorf("empty addr data")
		return
	}
	switch data[0] {
	case AddrTypeIPv4:
		if len(data) < 7 {
			err = fmt.Errorf("short IPv4 addr")
			return
		}
		addr = net.IP(data[1:5]).String()
		port = binary.BigEndian.Uint16(data[5:7])
		totalLen = 7
	case AddrTypeDomain:
		if len(data) < 2 {
			err = fmt.Errorf("short domain addr")
			return
		}
		dLen := int(data[1])
		if len(data) < 2+dLen+2 {
			err = fmt.Errorf("short domain data")
			return
		}
		addr = string(data[2 : 2+dLen])
		port = binary.BigEndian.Uint16(data[2+dLen : 4+dLen])
		totalLen = 4 + dLen
	case AddrTypeIPv6:
		if len(data) < 19 {
			err = fmt.Errorf("short IPv6 addr")
			return
		}
		addr = net.IP(data[1:17]).String()
		port = binary.BigEndian.Uint16(data[17:19])
		totalLen = 19
	default:
		err = fmt.Errorf("unknown addr type: 0x%02x", data[0])
	}
	return
}
