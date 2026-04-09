//go:build windows

package transport

import (
	"context"
	"net"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ipUnicastIF = 31 // IP_UNICAST_IF setsockopt level for IPv4
)

// protectedDialUDP dials a UDP socket bound to the physical (non-TUN)
// interface so that outbound packets bypass any active TUN routing.
// Mirrors daemon/internal/tun.protectedDial but lives in this package
// to avoid an import cycle. See the comment over IP_UNICAST_IF in
// daemon/internal/tun/dialer_windows.go for the byte-order trap that
// burned us in 1.27-1.28.13.
func protectedDialUDP(network, address string) (net.Conn, error) {
	// Loopback dials must skip interface binding — IP_UNICAST_IF restricts
	// the socket to the physical interface, which has no route to 127/8.
	// Loopback only happens in tests.
	if isLoopbackAddr(address) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		// Fall back to a plain dial if interface detection fails — that
		// matches the pre-TUN behavior and is no worse than what we had.
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}

	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				val := uint32(ifIndex) << 24 // htonl(ifIndex) on little-endian
				syscall.Setsockopt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIF,
					(*byte)(unsafe.Pointer(&val)), int32(unsafe.Sizeof(val)))
			})
		},
	}
	return dialer.DialContext(context.Background(), network, address)
}

// getPhysicalInterfaceIndex enumerates network interfaces and returns the
// index of the first non-loopback, non-TUN interface with an IPv4 address.
// Stand-alone copy (intentionally not shared with the tun package) so this
// transport-layer concern stays self-contained — the perf cost of the
// per-call enumeration is irrelevant here because Connect runs once.
func getPhysicalInterfaceIndex() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.Contains(strings.ToLower(iface.Name), "smurovproxy") {
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

// silence unused-import warning when ws2 isn't used directly
var _ = windows.AF_INET
