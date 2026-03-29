package socks5

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	version    = 0x05
	cmdConnect = 0x01
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
	noAuth     = 0x00
)

type ConnectRequest struct {
	Addr string
	Port uint16
}

func Handshake(conn net.Conn) (*ConnectRequest, error) {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	if buf[0] != version {
		return nil, fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}

	methods := make([]byte, buf[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return nil, err
	}

	if _, err := conn.Write([]byte{version, noAuth}); err != nil {
		return nil, err
	}

	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != version {
		return nil, fmt.Errorf("bad version in request: %d", header[0])
	}
	if header[1] != cmdConnect {
		return nil, fmt.Errorf("unsupported command: %d", header[1])
	}

	var addr string
	switch header[3] {
	case atypIPv4:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return nil, err
		}
		addr = net.IP(ipBuf).String()
	case atypDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, err
		}
		domBuf := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domBuf); err != nil {
			return nil, err
		}
		addr = string(domBuf)
	case atypIPv6:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return nil, err
		}
		addr = net.IP(ipBuf).String()
	default:
		return nil, fmt.Errorf("unsupported address type: 0x%02x", header[3])
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return nil, err
	}

	return &ConnectRequest{
		Addr: addr,
		Port: binary.BigEndian.Uint16(portBuf),
	}, nil
}

func SendSuccess(conn net.Conn) error {
	reply := []byte{version, 0x00, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}

func SendFailure(conn net.Conn) error {
	reply := []byte{version, 0x01, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}
