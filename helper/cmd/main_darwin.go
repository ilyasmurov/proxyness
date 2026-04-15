//go:build darwin

package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

const socketPath = "/var/run/proxyness-helper.sock"

var tunDevice tun.Device
var tunName string
var serverHost string // stored for route cleanup
var origGateway string // stored for bypass routes
var origInterface string // stored for bypass routes

func listenIPC() (net.Listener, error) {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	os.Chmod(socketPath, 0666)
	return ln, nil
}

func createTUN(serverAddr string) Response {
	if tunDevice != nil {
		return Response{TUNName: tunName, Error: "TUN already exists"}
	}

	dev, err := tun.CreateTUN("utun", 1500)
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

	// Get default gateway and interface before adding TUN routes
	gw := getDefaultGateway()
	origGateway = gw
	origInterface = getDefaultInterface()

	// Add server route via original gateway to prevent routing loop
	if serverAddr != "" && gw != "" {
		host, _, _ := net.SplitHostPort(serverAddr)
		if host != "" {
			// Resolve hostname to IPs
			ips, err := net.LookupHost(host)
			if err == nil {
				for _, ip := range ips {
					run("route", "add", "-host", ip, gw)
					log.Printf("added server route: %s via %s", ip, gw)
				}
				serverHost = host
			} else {
				// Try using host directly as IP
				run("route", "add", "-host", host, gw)
				serverHost = host
			}
		}
	}

	// Route system DNS servers via gateway to keep resolution working
	if gw != "" {
		for _, dns := range getSystemDNS() {
			run("route", "add", "-host", dns, gw)
			log.Printf("added DNS route: %s via %s", dns, gw)
		}
	}

	// Assign IP to TUN interface
	run("ifconfig", name, "10.0.85.1", "10.0.85.1", "up")

	// Route traffic through TUN (split into two halves to avoid replacing default route)
	run("route", "add", "-net", "0.0.0.0/1", "-interface", name)
	run("route", "add", "-net", "128.0.0.0/1", "-interface", name)

	// Add interface-scoped bypass routes via physical interface.
	// These only match when socket has IP_BOUND_IF set to the physical interface,
	// allowing protectedDial to bypass TUN for excluded apps.
	if origInterface != "" && gw != "" {
		run("route", "add", "-net", "0.0.0.0/1", gw, "-ifscope", origInterface)
		run("route", "add", "-net", "128.0.0.0/1", gw, "-ifscope", origInterface)
		log.Printf("added bypass routes via %s (gw %s)", origInterface, gw)
	}

	return Response{TUNName: name}
}

// refreshRoutes re-installs the server host, DNS, and ifscope bypass
// routes without touching the TUN device or its default-split routes.
// Called by the daemon's waitForNetwork when sendto() returns
// ENETUNREACH despite routes appearing present — typical darwin symptom
// of a stale ARP/neighbor cache for the gateway after an interface flap
// (Docker vmnetd, USB-ethernet plug/unplug, brief wifi loss). The
// `route delete` followed by `route add -host <server> <gw>` forces the
// kernel to re-resolve the gateway's MAC, which unsticks the blackhole
// state that a plain socket reconnect can't clear.
//
// Also re-reads the current default gateway/interface, so if the
// physical interface or gateway changed (wifi ↔ ethernet) the new
// values get used for the re-added routes.
func refreshRoutes() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	newGw := getDefaultGateway()
	newIface := getDefaultInterface()
	if newGw == "" {
		return Response{Error: "no default gateway (system offline)"}
	}

	// Drop stale routes. These may fail if the route was already flushed
	// by some OS event — that's expected, run() just logs and continues.
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				run("route", "delete", "-host", ip)
			}
		} else {
			run("route", "delete", "-host", serverHost)
		}
	}
	for _, dns := range getSystemDNS() {
		run("route", "delete", "-host", dns)
	}
	if origInterface != "" {
		run("route", "delete", "-net", "0.0.0.0/1", "-ifscope", origInterface)
		run("route", "delete", "-net", "128.0.0.0/1", "-ifscope", origInterface)
	}

	origGateway = newGw
	origInterface = newIface

	// Re-add — each `route add -host X gw` triggers a fresh neighbor
	// resolution for gw, which is the whole point of this function.
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				run("route", "add", "-host", ip, newGw)
				log.Printf("refresh: re-added server route %s via %s", ip, newGw)
			}
		} else {
			run("route", "add", "-host", serverHost, newGw)
			log.Printf("refresh: re-added server route %s via %s", serverHost, newGw)
		}
	}
	for _, dns := range getSystemDNS() {
		run("route", "add", "-host", dns, newGw)
	}
	if newIface != "" {
		run("route", "add", "-net", "0.0.0.0/1", newGw, "-ifscope", newIface)
		run("route", "add", "-net", "128.0.0.0/1", newGw, "-ifscope", newIface)
		log.Printf("refresh: re-added bypass routes via %s (gw %s)", newIface, newGw)
	}

	return Response{}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	run("route", "delete", "-net", "0.0.0.0/1")
	run("route", "delete", "-net", "128.0.0.0/1")

	// Remove bypass scoped routes
	if origInterface != "" {
		run("route", "delete", "-net", "0.0.0.0/1", "-ifscope", origInterface)
		run("route", "delete", "-net", "128.0.0.0/1", "-ifscope", origInterface)
		origGateway = ""
		origInterface = ""
	}

	// Remove DNS routes
	for _, dns := range getSystemDNS() {
		run("route", "delete", "-host", dns)
	}

	// Remove server routes
	if serverHost != "" {
		ips, err := net.LookupHost(serverHost)
		if err == nil {
			for _, ip := range ips {
				run("route", "delete", "-host", ip)
			}
		} else {
			run("route", "delete", "-host", serverHost)
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
	dev := tunDevice // local copy — safe if destroyTUN clears global
	if dev == nil {
		return
	}

	log.Printf("starting packet relay")
	done := make(chan struct{}, 2)

	// TUN → Daemon
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 4+1500) // 4-byte AF header + MTU
		bufs := [][]byte{buf}
		sizes := []int{0}
		lenBuf := make([]byte, 2)

		for {
			_, err := dev.Read(bufs, sizes, 4)
			if err != nil {
				log.Printf("TUN read: %v", err)
				return
			}
			pktLen := sizes[0]
			if pktLen == 0 {
				continue
			}

			// IP packet at buf[4:4+pktLen] (Read strips AF header via offset)
			binary.BigEndian.PutUint16(lenBuf, uint16(pktLen))
			if _, err := conn.Write(lenBuf); err != nil {
				return
			}
			if _, err := conn.Write(buf[4 : 4+pktLen]); err != nil {
				return
			}
		}
	}()

	// Daemon → TUN
	go func() {
		defer func() { done <- struct{}{} }()
		lenBuf := make([]byte, 2)

		for {
			if _, err := io.ReadFull(conn, lenBuf); err != nil {
				return
			}
			pktLen := int(binary.BigEndian.Uint16(lenBuf))
			if pktLen == 0 || pktLen > 1500 {
				continue
			}

			// 4 bytes space for AF header + IP packet
			buf := make([]byte, 4+pktLen)
			if _, err := io.ReadFull(conn, buf[4:]); err != nil {
				return
			}

			// Write with offset=4: library sets AF header from IP version
			bufs := [][]byte{buf}
			if _, err := dev.Write(bufs, 4); err != nil {
				log.Printf("TUN write: %v", err)
				return
			}
		}
	}()

	<-done
	log.Printf("packet relay stopped")
}

func getDefaultGateway() string {
	gw, _ := parseDefaultRoute()
	return gw
}

func getDefaultInterface() string {
	_, iface := parseDefaultRoute()
	return iface
}

func parseDefaultRoute() (gateway, iface string) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			gateway = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
		if strings.HasPrefix(line, "interface:") {
			iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		}
	}
	return
}

func getSystemDNS() []string {
	out, err := exec.Command("networksetup", "-getdnsservers", "Wi-Fi").Output()
	if err != nil {
		return nil
	}
	var servers []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if net.ParseIP(line) != nil {
			servers = append(servers, line)
		}
	}
	return servers
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
