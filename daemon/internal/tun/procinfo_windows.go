//go:build windows

package tun

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &windowsProcessInfo{}
}

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	getExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

func (w *windowsProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	switch network {
	case "tcp":
		return w.findTCP(localPort)
	case "udp":
		return w.findUDP(localPort)
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}
}

func (w *windowsProcessInfo) findTCP(localPort uint16) (string, error) {
	var size uint32
	getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 5, 0)

	buf := make([]byte, size)
	ret, _, _ := getExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		windows.AF_INET,
		5,
		0,
	)
	if ret != 0 {
		return "", fmt.Errorf("GetExtendedTcpTable: %d", ret)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 24
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		entry := buf[offset : offset+entrySize]
		port := binary.BigEndian.Uint16(entry[8:10])
		if port == localPort {
			pid := binary.LittleEndian.Uint32(entry[20:24])
			return getProcessPath(pid)
		}
	}
	return "", nil
}

func (w *windowsProcessInfo) findUDP(localPort uint16) (string, error) {
	var size uint32
	getExtendedUdpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 1, 0)

	buf := make([]byte, size)
	ret, _, _ := getExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		windows.AF_INET,
		1,
		0,
	)
	if ret != 0 {
		return "", fmt.Errorf("GetExtendedUdpTable: %d", ret)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 12
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		entry := buf[offset : offset+entrySize]
		port := binary.BigEndian.Uint16(entry[4:6])
		if port == localPort {
			pid := binary.LittleEndian.Uint32(entry[8:12])
			return getProcessPath(pid)
		}
	}
	return "", nil
}

func getProcessPath(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}
