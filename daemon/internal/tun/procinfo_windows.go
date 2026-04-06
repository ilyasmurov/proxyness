//go:build windows

package tun

import (
	"encoding/binary"
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessInfo struct {
	mu sync.Mutex

	// Table-level cache: refreshed at most once per tableTTL.
	// All lookups within the TTL window share the same snapshot.
	tcpPorts map[uint16]uint32 // local port → PID
	tcpTime  time.Time
	udpPorts map[uint16]uint32
	udpTime  time.Time

	// PID → executable path cache. Process paths don't change,
	// so entries live until the daemon restarts.
	pidPaths map[uint32]string

	// Reusable buffers to avoid allocation per call.
	tcpBuf []byte
	udpBuf []byte
}

const tableTTL = 2 * time.Second

func newProcessInfo() ProcessInfo {
	return &windowsProcessInfo{
		tcpPorts: make(map[uint16]uint32),
		udpPorts: make(map[uint16]uint32),
		pidPaths: make(map[uint32]string),
	}
}

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	getExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

func (w *windowsProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var pid uint32
	var found bool

	switch network {
	case "tcp":
		if time.Since(w.tcpTime) > tableTTL {
			w.refreshTCP()
		}
		pid, found = w.tcpPorts[localPort]
	case "udp":
		if time.Since(w.udpTime) > tableTTL {
			w.refreshUDP()
		}
		pid, found = w.udpPorts[localPort]
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}

	// Port not in cached table — force refresh and retry once.
	// Handles connections established between cache refreshes.
	if !found {
		switch network {
		case "tcp":
			w.refreshTCP()
			pid, found = w.tcpPorts[localPort]
		case "udp":
			w.refreshUDP()
			pid, found = w.udpPorts[localPort]
		}
		if !found {
			return "", nil
		}
	}

	// PID → path cache (permanent — paths don't change for a running process)
	if path, ok := w.pidPaths[pid]; ok {
		return path, nil
	}

	path, err := getProcessPath(pid)
	if err != nil {
		return "", nil
	}
	w.pidPaths[pid] = path
	return path, nil
}

// refreshTCP snapshots the entire TCP connection table into tcpPorts.
func (w *windowsProcessInfo) refreshTCP() {
	var size uint32
	getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 5, 0)
	if size == 0 {
		return
	}

	// Grow reusable buffer if needed
	if cap(w.tcpBuf) < int(size) {
		w.tcpBuf = make([]byte, size)
	}
	buf := w.tcpBuf[:size]

	ret, _, _ := getExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0, windows.AF_INET, 5, 0,
	)
	if ret != 0 {
		return
	}

	// Clear old entries
	for k := range w.tcpPorts {
		delete(w.tcpPorts, k)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 24
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		if offset+entrySize > uint32(len(buf)) {
			break
		}
		entry := buf[offset : offset+entrySize]
		port := binary.BigEndian.Uint16(entry[8:10])
		pid := binary.LittleEndian.Uint32(entry[20:24])
		w.tcpPorts[port] = pid
	}
	w.tcpTime = time.Now()
}

// refreshUDP snapshots the entire UDP connection table into udpPorts.
func (w *windowsProcessInfo) refreshUDP() {
	var size uint32
	getExtendedUdpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 1, 0)
	if size == 0 {
		return
	}

	if cap(w.udpBuf) < int(size) {
		w.udpBuf = make([]byte, size)
	}
	buf := w.udpBuf[:size]

	ret, _, _ := getExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0, windows.AF_INET, 1, 0,
	)
	if ret != 0 {
		return
	}

	for k := range w.udpPorts {
		delete(w.udpPorts, k)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 12
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		if offset+entrySize > uint32(len(buf)) {
			break
		}
		entry := buf[offset : offset+entrySize]
		port := binary.BigEndian.Uint16(entry[4:6])
		pid := binary.LittleEndian.Uint32(entry[8:12])
		w.udpPorts[port] = pid
	}
	w.udpTime = time.Now()
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
