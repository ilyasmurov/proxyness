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

// Physical interface cache. Windows GetAdaptersAddresses (called
// transitively from net.Interfaces) is extremely expensive — a profile
// of an idle TUN-mode daemon on Windows showed ~28% of CPU time spent
// in interface enumeration → GetAdaptersAddresses, plus another ~35%
// in the GC sweeping up the allocations it makes. Because this is called
// on every protectedDial (per bypass TCP/UDP connection), a busy browser
// with dozens of background requests was burning 40-60% of one core on
// interface enumeration alone. The cache is populated once at engine
// startup via CachePhysicalInterface and cleared on Stop. The interface
// basically never changes mid-session — swapping network adapters
// already requires a reconnect at higher layers.
var (
	cachedIfIndex int
	cachedIfIP    net.IP
	cachedIfMu    sync.RWMutex
	cachedIfSet   bool
)

// CachePhysicalInterface detects and caches the physical interface.
// Called from engine.Start before any TUN routes are added, so the
// interface enumeration sees the real physical adapters (not TUN).
func CachePhysicalInterface() {
	idx, ip, err := detectPhysicalInterface()
	if err != nil {
		log.Printf("[tun] failed to cache physical interface: %v", err)
		return
	}
	cachedIfMu.Lock()
	cachedIfIndex = idx
	cachedIfIP = ip
	cachedIfSet = true
	cachedIfMu.Unlock()
	log.Printf("[tun] cached physical interface index=%d ip=%s", idx, ip)
}

func ClearPhysicalInterfaceCache() {
	cachedIfMu.Lock()
	cachedIfSet = false
	cachedIfIP = nil
	cachedIfMu.Unlock()
}

func detectPhysicalInterface() (int, net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0, nil, err
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
				return iface.Index, ipnet.IP, nil
			}
		}
	}
	return 0, nil, fmt.Errorf("no physical interface found")
}

func getPhysicalInterface() (int, net.IP, error) {
	cachedIfMu.RLock()
	if cachedIfSet {
		idx, ip := cachedIfIndex, cachedIfIP
		cachedIfMu.RUnlock()
		return idx, ip, nil
	}
	cachedIfMu.RUnlock()
	// Cache miss — detect and populate lazily. Shouldn't happen in
	// practice (engine.Start calls CachePhysicalInterface) but keeps
	// the dialer functional if someone dials before the cache is warmed.
	return detectPhysicalInterface()
}

// protectedDial creates a connection that bypasses TUN routing by binding
// to the physical network interface's IPv4 address AND setting
// IP_UNICAST_IF. Both are needed on Windows.
//
// History of the traps here (important to understand before "simplifying"):
//
// Trap 1 — byte order. IP_UNICAST_IF on IPv4 requires the interface index
// in network byte order, i.e. `htonl(ifIndex)`. For index 3 on little-
// endian x86 that's the uint32 `0x03000000`. An earlier implementation
// used `<< 16` which produced garbage; Windows silently ignored the
// invalid option and fell back to the routing table, which in TUN mode
// points at... the TUN device. Bypass sockets looped straight back into
// bridgeInbound. Fix: shift by 24.
//
// Trap 2 — IP_UNICAST_IF is advisory on some Windows builds. Even with
// the correct byte order, setting IP_UNICAST_IF alone wasn't enough —
// the kernel can still consult the routing table for source-address
// selection, and if the TUN default route has higher priority, bypass
// packets get sent out the TUN device anyway. Same catastrophic loop:
// daemon dials bypass, Windows routes the dial back through TUN, helper
// re-reads it, daemon processes it as a new flow, dials bypass again,
// etc. On a busy Chrome session in "Selected" mode this produced ~16000
// loopback packets/sec, starved bridgeOutbound, and kept real responses
// from ever reaching the app. We already hit this trap once in the
// transport package (fixed in ca45bce) — this tun-package dialer was
// an identical copy that was NOT updated at the time, so the bug lived
// on for the bypass-TCP/UDP path while the server transport path was
// fine. Symptom: "Selected apps mode periodically dies, All traffic
// fixes it."
//
// Fix (same as transport/dialer_windows.go): bind the source IP directly
// via Dialer.LocalAddr to the physical interface's IPv4 address. With
// source = physical NIC IP, the kernel's source-address selection forces
// it to pick a matching route — i.e., the physical gateway, not TUN.
// IP_UNICAST_IF stays set as belt-and-suspenders for builds where
// LocalAddr alone might still get re-routed.
func protectedDial(network, address string) (net.Conn, error) {
	ifIndex, ifIP, err := getPhysicalInterface()
	if err != nil {
		return net.DialTimeout(network, address, 10*time.Second)
	}

	var localAddr net.Addr
	switch network {
	case "udp", "udp4":
		localAddr = &net.UDPAddr{IP: ifIP}
	case "tcp", "tcp4":
		localAddr = &net.TCPAddr{IP: ifIP}
	}

	dialer := net.Dialer{
		Timeout:   10 * time.Second,
		LocalAddr: localAddr,
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
