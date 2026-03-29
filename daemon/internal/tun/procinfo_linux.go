//go:build linux

package tun

import "fmt"

type linuxProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &linuxProcessInfo{}
}

func (l *linuxProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	return "", fmt.Errorf("process lookup not implemented on linux")
}
