//go:build darwin

package tun

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"syscall"
	"time"
)

const ipBoundIF = 25 // IP_BOUND_IF setsockopt on macOS

var (
	cachedIfIndex int
	cachedIfMu    sync.RWMutex
	cachedIfSet   bool
)

// CachePhysicalInterface detects and caches the physical interface index.
// Must be called before TUN routes are added, because after that
// the routing table points to utun as default.
func CachePhysicalInterface() {
	idx, err := detectPhysicalInterface()
	if err != nil {
		log.Printf("[tun] failed to cache physical interface: %v", err)
		return
	}
	cachedIfMu.Lock()
	cachedIfIndex = idx
	cachedIfSet = true
	cachedIfMu.Unlock()
}

func ClearPhysicalInterfaceCache() {
	cachedIfMu.Lock()
	cachedIfSet = false
	cachedIfMu.Unlock()
}

func detectPhysicalInterface() (int, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		// Skip TUN/virtual interfaces
		if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "lo") ||
			strings.HasPrefix(iface.Name, "bridge") || strings.HasPrefix(iface.Name, "awdl") ||
			strings.HasPrefix(iface.Name, "llw") || strings.HasPrefix(iface.Name, "anpi") {
			continue
		}
		// Must have an IPv4 address
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				log.Printf("[tun] physical interface: %s (index %d, ip %s)", iface.Name, iface.Index, ipnet.IP)
				return iface.Index, nil
			}
		}
	}
	return 0, fmt.Errorf("no physical interface found")
}

func getPhysicalInterfaceIndex() (int, error) {
	cachedIfMu.RLock()
	if cachedIfSet {
		idx := cachedIfIndex
		cachedIfMu.RUnlock()
		return idx, nil
	}
	cachedIfMu.RUnlock()
	return detectPhysicalInterface()
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
