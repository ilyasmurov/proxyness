//go:build windows

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
	"unsafe"
)

const ipUnicastIF = 31 // IP_UNICAST_IF setsockopt on Windows

// Physical interface index cache. Windows GetAdaptersAddresses (called
// transitively from net.Interfaces) is extremely expensive — a profile
// of an idle TUN-mode daemon on Windows showed ~28% of CPU time spent
// in getPhysicalInterfaceIndex → GetAdaptersAddresses, plus another ~35%
// in the GC sweeping up the allocations it makes. Because this is called
// on every protectedDial (per bypass TCP/UDP connection), a busy browser
// with dozens of background requests was burning 40-60% of one core on
// interface enumeration alone. The cache is populated once at engine
// startup via CachePhysicalInterface and cleared on Stop. The interface
// index basically never changes mid-session — swapping network adapters
// already requires a reconnect at higher layers.
var (
	cachedIfIndex int
	cachedIfMu    sync.RWMutex
	cachedIfSet   bool
)

// CachePhysicalInterface detects and caches the physical interface index.
// Called from engine.Start before any TUN routes are added, so the
// interface enumeration sees the real physical adapters (not TUN).
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
	log.Printf("[tun] cached physical interface index %d", idx)
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

func getPhysicalInterfaceIndex() (int, error) {
	cachedIfMu.RLock()
	if cachedIfSet {
		idx := cachedIfIndex
		cachedIfMu.RUnlock()
		return idx, nil
	}
	cachedIfMu.RUnlock()
	// Cache miss — detect and populate lazily. Shouldn't happen in
	// practice (engine.Start calls CachePhysicalInterface) but keeps
	// the dialer functional if someone dials before the cache is warmed.
	return detectPhysicalInterface()
}

// protectedDial creates a connection that bypasses TUN routing by binding
// to the physical network interface via IP_UNICAST_IF.
//
// Byte-order trap: Microsoft's IP_UNICAST_IF on IPv4 requires the interface
// index in network byte order, i.e. `htonl(ifIndex)`. For index 3 on
// little-endian x86 that's the uint32 value `0x03000000`. The previous
// implementation used `uint32(ifIndex) << 16` which produced `0x00030000`
// — neither host nor network order, just garbage. Windows ignored the
// (invalid) socket option silently, so every "bypass" socket happily
// followed the system default route... which in TUN mode points at the
// TUN device. Result: the daemon's own outbound TCP connections looped
// straight back into bridgeInbound. In "All traffic" mode it survived
// because the main transport stream is opened once and multiplexed; in
// "Selected" mode every isSelf bypass needed a fresh dial and the loop
// blew up immediately. Fix: shift by 24, equivalent to htonl(ifIndex)
// on little-endian.
func protectedDial(network, address string) (net.Conn, error) {
	ifIndex, err := getPhysicalInterfaceIndex()
	if err != nil {
		return net.DialTimeout(network, address, 10*time.Second)
	}

	dialer := net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				val := uint32(ifIndex) << 24 // htonl on little-endian
				syscall.Setsockopt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIF,
					(*byte)(unsafe.Pointer(&val)), int32(unsafe.Sizeof(val)))
			})
		},
	}
	return dialer.DialContext(context.Background(), network, address)
}
