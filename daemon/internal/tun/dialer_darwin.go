//go:build darwin

package tun

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
)

const ipBoundIF = 25 // IP_BOUND_IF setsockopt on macOS

func getPhysicalInterfaceIndex() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(iface.Name, "utun") {
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
// to the physical network interface via IP_BOUND_IF.
func protectedDial(network, address string) (net.Conn, error) {
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		return net.DialTimeout(network, address, 10*time.Second)
	}

	dialer := net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, ipBoundIF, ifIndex)
			})
		},
	}
	return dialer.DialContext(context.Background(), network, address)
}
