// Package vpnbridge exposes a gomobile-friendly API for connecting to
// the SmurovProxy server. This module reuses pkg/auth and pkg/proto
// for HMAC auth and the wire protocol, exposing only primitive types
// (string, []byte, error) across the gomobile boundary.
package vpnbridge

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"

	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
)

// Status constants returned by GetStatus.
const (
	StatusDisconnected = "disconnected"
	StatusConnecting   = "connecting"
	StatusConnected    = "connected"
)

var (
	mu     sync.Mutex
	conn   net.Conn
	status = StatusDisconnected
)

// Connect establishes a TLS connection to the proxy server, sends the
// HMAC auth handshake, and transitions to Connected state on success.
// Returns nil on success, error on failure. Thread-safe.
func Connect(server, key string) error {
	mu.Lock()
	defer mu.Unlock()

	if status == StatusConnected {
		return fmt.Errorf("already connected")
	}
	status = StatusConnecting

	tlsConn, err := tls.Dial("tcp", server, &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         "proxy.smurov.com",
	})
	if err != nil {
		status = StatusDisconnected
		return fmt.Errorf("TLS dial: %w", err)
	}

	// Send msg type (TCP)
	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		tlsConn.Close()
		status = StatusDisconnected
		return fmt.Errorf("write msg type: %w", err)
	}

	// HMAC auth
	authMsg, err := auth.CreateAuthMessage(key)
	if err != nil {
		tlsConn.Close()
		status = StatusDisconnected
		return fmt.Errorf("create auth: %w", err)
	}
	if _, err := tlsConn.Write(authMsg); err != nil {
		tlsConn.Close()
		status = StatusDisconnected
		return fmt.Errorf("write auth: %w", err)
	}

	// Read auth result
	result := make([]byte, 1)
	if _, err := io.ReadFull(tlsConn, result); err != nil {
		tlsConn.Close()
		status = StatusDisconnected
		return fmt.Errorf("read auth result: %w", err)
	}
	if result[0] != proto.ResultOK {
		tlsConn.Close()
		status = StatusDisconnected
		return fmt.Errorf("auth rejected")
	}

	conn = tlsConn
	status = StatusConnected
	return nil
}

// Disconnect tears down the active connection. Thread-safe.
func Disconnect() {
	mu.Lock()
	defer mu.Unlock()
	if conn != nil {
		conn.Close()
		conn = nil
	}
	status = StatusDisconnected
}

// GetStatus returns the current connection status string.
func GetStatus() string {
	mu.Lock()
	defer mu.Unlock()
	return status
}

// SendPacket writes a length-prefixed IP packet to the server.
// Used by the VPN service to forward captured traffic.
func SendPacket(data []byte) error {
	mu.Lock()
	c := conn
	mu.Unlock()
	if c == nil {
		return fmt.Errorf("not connected")
	}
	hdr := make([]byte, 2)
	binary.BigEndian.PutUint16(hdr, uint16(len(data)))
	if _, err := c.Write(hdr); err != nil {
		return err
	}
	_, err := c.Write(data)
	return err
}

// ReceivePacket reads a length-prefixed IP packet from the server.
// Blocks until data is available. Used by the VPN service to deliver
// incoming traffic back to the TUN device.
func ReceivePacket() ([]byte, error) {
	mu.Lock()
	c := conn
	mu.Unlock()
	if c == nil {
		return nil, fmt.Errorf("not connected")
	}
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint16(hdr)
	data := make([]byte, size)
	if _, err := io.ReadFull(c, data); err != nil {
		return nil, err
	}
	return data, nil
}
