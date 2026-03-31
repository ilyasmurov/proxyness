//go:build darwin && !cgo

package tun

import "fmt"

type darwinProcessInfoNoCgo struct{}

func newProcessInfo() ProcessInfo {
	return &darwinProcessInfoNoCgo{}
}

func (d *darwinProcessInfoNoCgo) FindProcess(network string, localPort uint16) (string, error) {
	return "", fmt.Errorf("process lookup requires CGo (native build)")
}
