//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/tun"
)

var tunDevice tun.Device
var tunName string
var serverHost string

func listenIPC() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:9091")
}

func createTUN(serverAddr string) Response {
	if tunDevice != nil {
		return Response{TUNName: tunName, Error: "TUN already exists"}
	}

	dev, err := tun.CreateTUN("Proxyness", 1500)
	if err != nil {
		return Response{Error: fmt.Sprintf("create tun: %v", err)}
	}

	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return Response{Error: fmt.Sprintf("get tun name: %v", err)}
	}

	tunDevice = dev
	tunName = name
	log.Printf("created TUN device: %s", name)

	// Get default gateway before adding TUN routes
	gw := getDefaultGateway()
	log.Printf("default gateway: %q", gw)

	// Add server route via original gateway to prevent routing loop
	if serverAddr != "" && gw != "" {
		host, _, _ := net.SplitHostPort(serverAddr)
		if host != "" {
			ips, err := net.LookupHost(host)
			if err == nil {
				for _, ip := range ips {
					runLog("route", "add", ip, "mask", "255.255.255.255", gw, "metric", "1")
					log.Printf("added server route: %s via %s", ip, gw)
				}
				serverHost = host
			} else {
				runLog("route", "add", host, "mask", "255.255.255.255", gw, "metric", "1")
				serverHost = host
			}
		}
	}

	// Configure IP on TUN adapter — netsh is async on Windows, wait for it
	runLog("netsh", "interface", "ip", "set", "address", name, "static", "10.0.85.1", "255.255.255.0")

	// Wait for interface to be ready (netsh is async)
	log.Printf("waiting for interface %s to be ready...", name)
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		out, _ := exec.Command("netsh", "interface", "ip", "show", "address", name).CombinedOutput()
		if strings.Contains(string(out), "10.0.85.1") {
			log.Printf("interface ready after %dms", (i+1)*500)
			break
		}
	}

	// Get TUN interface index for routing
	ifIndex := getInterfaceIndex(name)
	log.Printf("TUN interface index: %d", ifIndex)

	// Route DNS servers via gateway to keep resolution working
	if gw != "" {
		for _, dns := range getSystemDNS() {
			runLog("route", "add", dns, "mask", "255.255.255.255", gw, "metric", "1")
			log.Printf("added DNS route: %s via %s", dns, gw)
		}
	}

	// Add routes through TUN using interface index (more reliable than gateway IP)
	if ifIndex > 0 {
		runLog("route", "add", "0.0.0.0", "mask", "128.0.0.0", "0.0.0.0", "IF", fmt.Sprintf("%d", ifIndex), "metric", "5")
		runLog("route", "add", "128.0.0.0", "mask", "128.0.0.0", "0.0.0.0", "IF", fmt.Sprintf("%d", ifIndex), "metric", "5")
	} else {
		// Fallback to gateway-based routing
		runLog("route", "add", "0.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")
		runLog("route", "add", "128.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")
	}

	// Add bypass routes via physical interface for IP_UNICAST_IF (like -ifscope on macOS).
	// Higher metric so TUN routes win by default; IP_UNICAST_IF selects these.
	if gw != "" {
		physIdx := getPhysicalInterfaceIndex()
		if physIdx > 0 {
			runLog("route", "add", "0.0.0.0", "mask", "128.0.0.0", gw, "IF", fmt.Sprintf("%d", physIdx), "metric", "9999")
			runLog("route", "add", "128.0.0.0", "mask", "128.0.0.0", gw, "IF", fmt.Sprintf("%d", physIdx), "metric", "9999")
			log.Printf("added bypass routes via IF %d (gw %s)", physIdx, gw)
		}
	}

	return Response{TUNName: name}
}

// refreshRoutes mirrors the darwin version — re-installs server host,
// DNS, and physical-interface bypass routes without destroying the TUN
// device. Used by waitForNetwork to kick the kernel's neighbor cache
// when sendto() returns "network is unreachable" despite routes being
// present. See main_darwin.go refreshRoutes for the full rationale.
func refreshRoutes() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	newGw := getDefaultGateway()
	if newGw == "" {
		return Response{Error: "no default gateway (system offline)"}
	}
	physIdx := getPhysicalInterfaceIndex()

	// Drop stale routes (ignore errors — may not exist).
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				run("route", "delete", ip)
			}
		} else {
			run("route", "delete", serverHost)
		}
	}
	for _, dns := range getSystemDNS() {
		run("route", "delete", dns)
	}
	if physIdx > 0 {
		run("route", "delete", "0.0.0.0", "mask", "128.0.0.0", "IF", fmt.Sprintf("%d", physIdx))
		run("route", "delete", "128.0.0.0", "mask", "128.0.0.0", "IF", fmt.Sprintf("%d", physIdx))
	}

	// Re-add.
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				runLog("route", "add", ip, "mask", "255.255.255.255", newGw, "metric", "1")
				log.Printf("refresh: re-added server route %s via %s", ip, newGw)
			}
		} else {
			runLog("route", "add", serverHost, "mask", "255.255.255.255", newGw, "metric", "1")
		}
	}
	for _, dns := range getSystemDNS() {
		runLog("route", "add", dns, "mask", "255.255.255.255", newGw, "metric", "1")
	}
	if physIdx > 0 {
		runLog("route", "add", "0.0.0.0", "mask", "128.0.0.0", newGw, "IF", fmt.Sprintf("%d", physIdx), "metric", "9999")
		runLog("route", "add", "128.0.0.0", "mask", "128.0.0.0", newGw, "IF", fmt.Sprintf("%d", physIdx), "metric", "9999")
		log.Printf("refresh: re-added bypass routes via IF %d (gw %s)", physIdx, newGw)
	}

	return Response{}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	run("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	run("route", "delete", "128.0.0.0", "mask", "128.0.0.0")

	// Remove bypass routes (may fail if not added, that's ok)
	physIdx := getPhysicalInterfaceIndex()
	if physIdx > 0 {
		run("route", "delete", "0.0.0.0", "mask", "128.0.0.0", "IF", fmt.Sprintf("%d", physIdx))
		run("route", "delete", "128.0.0.0", "mask", "128.0.0.0", "IF", fmt.Sprintf("%d", physIdx))
	}

	// Remove DNS routes
	for _, dns := range getSystemDNS() {
		run("route", "delete", dns)
	}

	// Remove server routes
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				run("route", "delete", ip)
			}
		} else {
			run("route", "delete", serverHost)
		}
		serverHost = ""
	}

	tunDevice.Close()
	tunDevice = nil
	tunName = ""
	log.Printf("destroyed TUN device")
	return Response{}
}

func relayPackets(conn net.Conn) {
	dev := tunDevice
	if dev == nil {
		return
	}

	log.Printf("starting packet relay")
	done := make(chan struct{}, 2)
	var tunToDaemon, daemonToTun atomic.Int64

	// Packet counter logger
	go func() {
		for {
			time.Sleep(5 * time.Second)
			t2d := tunToDaemon.Load()
			d2t := daemonToTun.Load()
			if t2d > 0 || d2t > 0 {
				log.Printf("relay stats: TUN→daemon=%d, daemon→TUN=%d", t2d, d2t)
			}
			// Check if relay stopped
			select {
			case <-done:
				return
			default:
			}
		}
	}()

	// TUN → Daemon (Windows: no AF header, offset=0)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 1500)
		bufs := [][]byte{buf}
		sizes := []int{0}
		lenBuf := make([]byte, 2)

		for {
			_, err := dev.Read(bufs, sizes, 0)
			if err != nil {
				log.Printf("TUN read: %v", err)
				return
			}
			pktLen := sizes[0]
			if pktLen == 0 {
				continue
			}

			if tunToDaemon.Load() == 0 {
				log.Printf("first packet from TUN: %d bytes, IP version=%d", pktLen, buf[0]>>4)
			}
			tunToDaemon.Add(1)

			binary.BigEndian.PutUint16(lenBuf, uint16(pktLen))
			if _, err := conn.Write(lenBuf); err != nil {
				return
			}
			if _, err := conn.Write(buf[:pktLen]); err != nil {
				return
			}
		}
	}()

	// Daemon → TUN (Windows: no AF header, offset=0)
	go func() {
		defer func() { done <- struct{}{} }()
		lenBuf := make([]byte, 2)

		for {
			if _, err := io.ReadFull(conn, lenBuf); err != nil {
				return
			}
			pktLen := int(binary.BigEndian.Uint16(lenBuf))
			if pktLen == 0 || pktLen > 1500 {
				log.Printf("invalid packet length: %d", pktLen)
				continue
			}

			buf := make([]byte, pktLen)
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}

			if daemonToTun.Load() == 0 {
				log.Printf("first packet to TUN: %d bytes", pktLen)
			}
			daemonToTun.Add(1)

			bufs := [][]byte{buf}
			if _, err := dev.Write(bufs, 0); err != nil {
				log.Printf("TUN write: %v", err)
				return
			}
		}
	}()

	<-done
	t2d := tunToDaemon.Load()
	d2t := daemonToTun.Load()
	log.Printf("packet relay stopped (TUN→daemon=%d, daemon→TUN=%d)", t2d, d2t)
}

func getPhysicalInterfaceIndex() int {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		lower := strings.ToLower(iface.Name)
		if strings.Contains(lower, "proxyness") || strings.Contains(lower, "wintun") || strings.Contains(lower, "loopback") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return iface.Index
			}
		}
	}
	return 0
}

func getInterfaceIndex(name string) int {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	for _, iface := range ifaces {
		if strings.EqualFold(iface.Name, name) {
			return iface.Index
		}
	}
	return 0
}

func getDefaultGateway() string {
	out, err := exec.Command("cmd", "/c", "route", "print", "0.0.0.0").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 3 && fields[0] == "0.0.0.0" && fields[1] == "0.0.0.0" {
			return fields[2]
		}
	}
	return ""
}

func getSystemDNS() []string {
	out, err := exec.Command("cmd", "/c", "netsh", "interface", "ip", "show", "dns").Output()
	if err != nil {
		return nil
	}
	var servers []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Lines like "DNS Servers:                  8.8.8.8" or just "8.8.8.8"
		parts := strings.Split(line, ":")
		candidate := strings.TrimSpace(parts[len(parts)-1])
		if net.ParseIP(candidate) != nil && !seen[candidate] {
			seen[candidate] = true
			servers = append(servers, candidate)
		}
	}
	return servers
}

// runLog runs a command and always logs output (for debugging)
func runLog(name string, args ...string) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("FAIL %s %v: %v: %s", name, args, err, strings.TrimSpace(string(out)))
	} else {
		log.Printf("OK %s %v", name, args)
	}
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
