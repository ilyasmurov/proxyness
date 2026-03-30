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

const socketPath = "/var/run/smurov-helper.sock"

var tunDevice tun.Device
var tunName string
var serverHost string // stored for route cleanup

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

	// Get default gateway before adding TUN routes
	gw := getDefaultGateway()

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

	// Assign IP to TUN interface
	run("ifconfig", name, "10.0.85.1", "10.0.85.1", "up")

	// Route traffic through TUN (split into two halves to avoid replacing default route)
	run("route", "add", "-net", "0.0.0.0/1", "-interface", name)
	run("route", "add", "-net", "128.0.0.0/1", "-interface", name)

	return Response{TUNName: name}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	run("route", "delete", "-net", "0.0.0.0/1")
	run("route", "delete", "-net", "128.0.0.0/1")

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
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
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
