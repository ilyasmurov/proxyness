//go:build darwin

package tun

import (
	"context"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const ipBoundIF = 25 // IP_BOUND_IF setsockopt on macOS

func getPhysicalInterfaceIndex() (int, error) {
	// Use "route -n get default" to find the actual default interface
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "interface:") {
				ifName := strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
				iface, err := net.InterfaceByName(ifName)
				if err == nil {
					log.Printf("[tun] default route interface: %s (index %d)", ifName, iface.Index)
					return iface.Index, nil
				}
			}
		}
	}

	return 0, fmt.Errorf("no default route interface found")
}

// protectedDial creates a connection that bypasses TUN routing by binding
// to the physical network interface via IP_BOUND_IF.
func protectedDial(network, address string) (net.Conn, error) {
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		log.Printf("[tun] protectedDial fallback (no interface): %v", err)
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
