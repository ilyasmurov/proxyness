package transport

import "io"

// Mode constants for transport selection.
const (
	ModeAuto = "auto"
	ModeUDP  = "udp"
	ModeTLS  = "tls"

	// UDPPort is the dedicated server port for UDP transport.
	// Separate from TLS 443 to avoid TSPU/ISP blocks on UDP 443.
	UDPPort = "8443"
)

// Transport abstracts the connection to the proxy server.
type Transport interface {
	// Connect establishes a session with the server.
	Connect(server, key string, machineID [16]byte) error

	// OpenStream opens a new proxied stream to the given destination.
	// streamType is StreamTypeTCP (0x01) or StreamTypeUDP (0x02).
	OpenStream(streamType byte, addr string, port uint16) (Stream, error)

	// Close tears down the transport and all streams.
	Close() error

	// Mode returns the active transport type ("udp" or "tls").
	Mode() string
}

// Stream is a single proxied connection within a Transport.
type Stream interface {
	io.ReadWriteCloser
	ID() uint32
}
