//go:build darwin

package main

import (
	"fmt"
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

func listenIPC() (net.Listener, error) {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	os.Chmod(socketPath, 0666)
	return ln, nil
}

func createTUN() Response {
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

	tunDevice.Close()
	tunDevice = nil
	tunName = ""
	log.Printf("destroyed TUN device")
	return Response{}
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
