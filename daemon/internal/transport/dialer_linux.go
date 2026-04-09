//go:build linux

package transport

import (
	"context"
	"net"
	"time"
)

// protectedDialUDP on Linux is a plain dial — we don't currently configure
// per-route TUN binding here because Linux uses cgroups/fwmark for split
// tunneling instead of socket-level interface binding. Kept as a stub so
// the cross-platform call site in udp.go compiles.
func protectedDialUDP(network, address string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	return dialer.DialContext(context.Background(), network, address)
}
