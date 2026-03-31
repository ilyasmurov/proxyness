//go:build windows

package tun

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const ipUnicastIF = 31 // IP_UNICAST_IF setsockopt on Windows

// CachePhysicalInterface is a no-op on Windows — interface detection
// already enumerates interfaces and skips TUN by name.
func CachePhysicalInterface()          {}
func ClearPhysicalInterfaceCache()     {}

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
	return 0, fmt.Errorf("no physical interface found")
}

// protectedDial creates a connection that bypasses TUN routing by binding
// to the physical network interface via IP_UNICAST_IF.
func protectedDial(network, address string) (net.Conn, error) {
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		return net.DialTimeout(network, address, 10*time.Second)
	}

	dialer := net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				// IP_UNICAST_IF expects the interface index in network byte order
				// (big-endian) as a 4-byte value with index in the upper 16 bits.
				val := uint32(ifIndex) << 16
				// Use raw setsockopt to ensure correct 4-byte representation
				syscall.Setsockopt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIF,
					(*byte)(unsafe.Pointer(&val)), int32(unsafe.Sizeof(val)))
			})
		},
	}
	return dialer.DialContext(context.Background(), network, address)
}
