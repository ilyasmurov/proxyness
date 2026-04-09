//go:build darwin

package transport

import (
	"context"
	"net"
	"strings"
	"syscall"
	"time"
)

const ipBoundIF = 25 // IP_BOUND_IF setsockopt level for IPv4 on macOS

// protectedDialUDP dials a UDP socket bound to the physical (non-TUN)
// interface so that outbound packets bypass any active TUN routing.
// Mirrors daemon/internal/tun.protectedDial but lives in this package
// to avoid an import cycle.
func protectedDialUDP(network, address string) (net.Conn, error) {
	// Loopback dials must skip interface binding — IP_BOUND_IF restricts
	// the socket to the physical interface, which has no route to 127/8
	// or ::1. Loopback only happens in tests against a local mock server.
	if isLoopbackAddr(address) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}

	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipBoundIF, ifIndex)
			})
		},
	}
	return dialer.DialContext(context.Background(), network, address)
}

func isLoopbackAddr(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host == "localhost"
	}
	return ip.IsLoopback()
}

func getPhysicalInterfaceIndex() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "lo") ||
			strings.HasPrefix(iface.Name, "bridge") || strings.HasPrefix(iface.Name, "awdl") ||
			strings.HasPrefix(iface.Name, "llw") || strings.HasPrefix(iface.Name, "anpi") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return iface.Index, nil
			}
		}
	}
	return 0, syscall.EINVAL
}
