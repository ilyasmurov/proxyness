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

	dev, err := tun.CreateTUN("SmurovProxy", 1500)
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

	// Add server route via original gateway to prevent routing loop
	if serverAddr != "" && gw != "" {
		host, _, _ := net.SplitHostPort(serverAddr)
		if host != "" {
			ips, err := net.LookupHost(host)
			if err == nil {
				for _, ip := range ips {
					run("route", "add", ip, "mask", "255.255.255.255", gw, "metric", "1")
					log.Printf("added server route: %s via %s", ip, gw)
				}
				serverHost = host
			} else {
				run("route", "add", host, "mask", "255.255.255.255", gw, "metric", "1")
				serverHost = host
			}
		}
	}

	// Configure IP on TUN adapter
	run("netsh", "interface", "ip", "set", "address", name, "static", "10.0.85.1", "255.255.255.0")

	// Add routes through TUN
	run("route", "add", "0.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")
	run("route", "add", "128.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")

	return Response{TUNName: name}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	run("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	run("route", "delete", "128.0.0.0", "mask", "128.0.0.0")

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
				continue
			}

			buf := make([]byte, pktLen)
			if _, err := io.ReadFull(conn, buf); err != nil {
				return
			}

			bufs := [][]byte{buf}
			if _, err := dev.Write(bufs, 0); err != nil {
				log.Printf("TUN write: %v", err)
				return
			}
		}
	}()

	<-done
	log.Printf("packet relay stopped")
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

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
