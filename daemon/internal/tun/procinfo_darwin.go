//go:build darwin

package tun

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type darwinProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &darwinProcessInfo{}
}

func (d *darwinProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	var sysctlName string
	switch network {
	case "tcp":
		sysctlName = "net.inet.tcp.pcblist_n"
	case "udp":
		sysctlName = "net.inet.udp.pcblist_n"
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}

	data, err := unix.SysctlRaw(sysctlName)
	if err != nil {
		return "", fmt.Errorf("sysctl %s: %w", sysctlName, err)
	}

	if len(data) < 24 {
		return "", nil
	}

	itemSize := int(binary.LittleEndian.Uint32(data[0:4]))
	if itemSize == 0 {
		return "", nil
	}

	for offset := itemSize; offset+itemSize <= len(data); offset += itemSize {
		entry := data[offset : offset+itemSize]
		if len(entry) < 188 {
			continue
		}

		lport := binary.BigEndian.Uint16(entry[18:20])
		if lport != localPort {
			continue
		}

		pid := binary.LittleEndian.Uint32(entry[172:176])
		if pid == 0 {
			continue
		}

		path, err := getExecPath(pid)
		if err != nil {
			continue
		}
		return path, nil
	}

	return "", nil
}

func getExecPath(pid uint32) (string, error) {
	const (
		procPIDPathInfo     = 0xb
		procPIDPathInfoSize = 1024
		procCallNumPIDInfo  = 0x2
	)

	buf := make([]byte, procPIDPathInfoSize)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procCallNumPIDInfo,
		uintptr(pid),
		procPIDPathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		procPIDPathInfoSize,
	)
	if errno != 0 {
		return "", fmt.Errorf("proc_info: %v", errno)
	}

	path := string(buf)
	if idx := strings.IndexByte(path, 0); idx >= 0 {
		path = path[:idx]
	}
	return path, nil
}
