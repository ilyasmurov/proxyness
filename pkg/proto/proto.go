package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"smurov-proxy/pkg/auth"
)

const (
	AddrTypeIPv4   = 0x01
	AddrTypeDomain = 0x03
	AddrTypeIPv6   = 0x04

	ResultOK   = 0x01
	ResultFail = 0x00
)

// WriteAuth sends a 41-byte auth message over the connection.
func WriteAuth(conn net.Conn, key string) error {
	msg, err := auth.CreateAuthMessage(key)
	if err != nil {
		return err
	}
	_, err = conn.Write(msg)
	return err
}

// ReadAuth reads and validates a 41-byte auth message.
func ReadAuth(conn net.Conn, key string) error {
	msg := make([]byte, auth.AuthMsgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return err
	}
	return auth.ValidateAuthMessage(key, msg)
}

// WriteResult sends a 1-byte result (0x01=OK, 0x00=fail).
func WriteResult(conn net.Conn, ok bool) error {
	b := byte(ResultFail)
	if ok {
		b = ResultOK
	}
	_, err := conn.Write([]byte{b})
	return err
}

// ReadResult reads a 1-byte result.
func ReadResult(conn net.Conn) (bool, error) {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false, err
	}
	return buf[0] == ResultOK, nil
}

// WriteMachineID sends 0x03 + 16-byte machine fingerprint.
func WriteMachineID(conn net.Conn, id [16]byte) error {
	buf := make([]byte, 1+MachineIDLen)
	buf[0] = MsgTypeMachineID
	copy(buf[1:], id[:])
	_, err := conn.Write(buf)
	return err
}

// ReadMachineID reads 16-byte machine fingerprint (after 0x03 type byte was already read).
func ReadMachineID(conn net.Conn) ([16]byte, error) {
	var id [16]byte
	_, err := io.ReadFull(conn, id[:])
	return id, err
}

// WriteConnect sends address type + address + port.
func WriteConnect(conn net.Conn, addr string, port uint16) error {
	ip := net.ParseIP(addr)

	var buf []byte
	if ip4 := ip.To4(); ip4 != nil {
		buf = make([]byte, 1+4+2)
		buf[0] = AddrTypeIPv4
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
	} else if ip16 := ip.To16(); ip16 != nil {
		buf = make([]byte, 1+16+2)
		buf[0] = AddrTypeIPv6
		copy(buf[1:17], ip16)
		binary.BigEndian.PutUint16(buf[17:], port)
	} else {
		if len(addr) > 255 {
			return fmt.Errorf("domain name too long: %d bytes", len(addr))
		}
		buf = make([]byte, 1+1+len(addr)+2)
		buf[0] = AddrTypeDomain
		buf[1] = byte(len(addr))
		copy(buf[2:2+len(addr)], addr)
		binary.BigEndian.PutUint16(buf[2+len(addr):], port)
	}

	_, err := conn.Write(buf)
	return err
}

// ReadConnect reads address type + address + port.
func ReadConnect(conn net.Conn) (addr string, port uint16, err error) {
	typeBuf := make([]byte, 1)
	if _, err = io.ReadFull(conn, typeBuf); err != nil {
		return
	}

	switch typeBuf[0] {
	case AddrTypeIPv4:
		buf := make([]byte, 4)
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = net.IP(buf).String()
	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err = io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		buf := make([]byte, lenBuf[0])
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = string(buf)
	case AddrTypeIPv6:
		buf := make([]byte, 16)
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = net.IP(buf).String()
	default:
		err = fmt.Errorf("unsupported address type: 0x%02x", typeBuf[0])
		return
	}

	portBuf := make([]byte, 2)
	if _, err = io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port = binary.BigEndian.Uint16(portBuf)
	return
}

type countingWriter struct {
	dst     net.Conn
	onBytes func(n int64)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.onBytes(int64(n))
	}
	return n, err
}

func CountingRelay(c1, c2 net.Conn, onBytes func(in, out int64)) error {
	errc := make(chan error, 2)
	go func() {
		cw := &countingWriter{dst: c1, onBytes: func(n int64) { onBytes(n, 0) }}
		_, err := io.Copy(cw, c2)
		errc <- err
	}()
	go func() {
		cw := &countingWriter{dst: c2, onBytes: func(n int64) { onBytes(0, n) }}
		_, err := io.Copy(cw, c1)
		errc <- err
	}()
	err := <-errc
	c1.Close()
	c2.Close()
	<-errc
	return err
}

// Relay copies data bidirectionally between two connections.
// Returns when either direction hits an error or EOF.
func Relay(c1, c2 net.Conn) error {
	errc := make(chan error, 2)
	cp := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		errc <- err
	}
	go cp(c1, c2)
	go cp(c2, c1)
	err := <-errc
	c1.Close()
	c2.Close()
	<-errc
	return err
}
