//go:build linux

package tun

import (
	"net"
	"time"
)

func protectedDial(network, address string) (net.Conn, error) {
	return net.DialTimeout(network, address, 10*time.Second)
}
