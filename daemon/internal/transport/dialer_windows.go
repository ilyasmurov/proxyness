//go:build windows

package transport

import (
	"context"
	"log"
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
	// Loopback dials must skip interface binding — restricting to a
	// physical interface kills loopback routing. Only happens in tests.
	if isLoopbackAddr(address) {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}

	// Find the physical interface AND its IPv4 address. We bind the socket
	// to that IP via Dialer.LocalAddr instead of relying on IP_UNICAST_IF
	// — turns out the latter is only "advisory" on connected UDP sockets
	// on some Windows builds, and the kernel can still route via TUN if
	// the TUN default route is at a higher metric. Binding the source IP
	// directly forces the kernel's source-address selection and the
	// routing decision falls in line: with source = physical NIC IP, the
	// kernel must use the physical interface's route to reach the remote.
	// IP_UNICAST_IF is still set as belt-and-suspenders.
	ifIndex, ifIP, err := getPhysicalInterface()
	if err != nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		return dialer.DialContext(context.Background(), network, address)
	}

	var localAddr net.Addr
	switch network {
	case "udp", "udp4":
		localAddr = &net.UDPAddr{IP: ifIP}
	case "tcp", "tcp4":
		localAddr = &net.TCPAddr{IP: ifIP}
	}

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		LocalAddr: localAddr,
		Control: func(_, _ string, c syscall.RawConn) error {
			return c.Control(func(fd uintptr) {
				val := uint32(ifIndex) << 24 // htonl(ifIndex) on little-endian
				syscall.Setsockopt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIF,
					(*byte)(unsafe.Pointer(&val)), int32(unsafe.Sizeof(val)))
			})
		},
	}
	conn, err := dialer.DialContext(context.Background(), network, address)
	if err != nil {
		log.Printf("[transport] protectedDialUDP %s %s failed: %v", network, address, err)
		return nil, err
	}
	log.Printf("[transport] protectedDialUDP %s %s → local=%s remote=%s ifIndex=%d",
		network, address, conn.LocalAddr(), conn.RemoteAddr(), ifIndex)
	return conn, nil
}

// getPhysicalInterface returns the first non-loopback, non-TUN interface
// with an IPv4 address, along with that address. Used to bind sockets so
// they bypass any active TUN routing.
func getPhysicalInterface() (int, net.IP, error) {
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
	return 0, nil, syscall.EINVAL
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
