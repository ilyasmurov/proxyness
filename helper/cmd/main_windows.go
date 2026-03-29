//go:build windows

package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"
)

var tunDevice tun.Device
var tunName string

func listenIPC() (net.Listener, error) {
	// TCP on localhost — simpler than named pipes, sufficient for single-user desktop app
	return net.Listen("tcp", "127.0.0.1:9091")
}

func createTUN() Response {
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

	tunDevice.Close()
	tunDevice = nil
	tunName = ""
	log.Printf("destroyed TUN device")
	return Response{}
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
