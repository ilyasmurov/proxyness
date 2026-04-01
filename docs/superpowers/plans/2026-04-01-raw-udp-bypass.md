# Raw UDP Bypass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Intercept UDP packets before gVisor and bypass them via regular Go sockets, eliminating gVisor overhead for ~90% of UDP traffic.

**Architecture:** In `bridgeInbound`, parse raw IP+UDP headers before gVisor injection. For bypass UDP, route through a NAT table using `protectedDial` sockets. Response packets are reassembled as raw IP+UDP and sent back through helper to TUN. Proxy UDP still goes through gVisor.

**Tech Stack:** Go, raw IP/UDP packet parsing, gVisor (unchanged for proxy path)

---

### Task 1: Packet Parser

**Files:**
- Create: `daemon/internal/tun/packet.go`
- Create: `daemon/internal/tun/packet_test.go`

- [ ] **Step 1: Write failing tests for ParseIPv4Header**

In `daemon/internal/tun/packet_test.go`:

```go
package tun

import (
	"net"
	"testing"
)

func TestParseIPv4Header_UDP(t *testing.T) {
	// Minimal IPv4 header (20 bytes) + UDP
	pkt := make([]byte, 28)
	pkt[0] = 0x45            // version=4, IHL=5 (20 bytes)
	pkt[9] = 17              // protocol = UDP
	copy(pkt[12:16], net.IP{10, 0, 0, 1}.To4())  // src
	copy(pkt[16:20], net.IP{8, 8, 8, 8}.To4())    // dst
	// UDP header at offset 20
	pkt[20] = 0x12; pkt[21] = 0x34 // src port 0x1234 = 4660
	pkt[22] = 0x00; pkt[23] = 0x35 // dst port 53

	proto, srcIP, dstIP, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 17 {
		t.Errorf("proto = %d, want 17", proto)
	}
	if !srcIP.Equal(net.IP{10, 0, 0, 1}) {
		t.Errorf("srcIP = %v", srcIP)
	}
	if !dstIP.Equal(net.IP{8, 8, 8, 8}) {
		t.Errorf("dstIP = %v", dstIP)
	}
	if hdrLen != 20 {
		t.Errorf("hdrLen = %d, want 20", hdrLen)
	}
}

func TestParseIPv4Header_TCP(t *testing.T) {
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	pkt[9] = 6 // TCP
	proto, _, _, _, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 6 {
		t.Errorf("proto = %d, want 6", proto)
	}
}

func TestParseIPv4Header_TooShort(t *testing.T) {
	_, _, _, _, err := ParseIPv4Header([]byte{0x45, 0x00})
	if err == nil {
		t.Error("expected error for short packet")
	}
}

func TestParseUDPHeader(t *testing.T) {
	// UDP header: srcPort(2) + dstPort(2) + length(2) + checksum(2) + payload
	udp := []byte{
		0x12, 0x34, // src port 4660
		0x00, 0x35, // dst port 53
		0x00, 0x0B, // length 11 (8 header + 3 payload)
		0x00, 0x00, // checksum
		0xAA, 0xBB, 0xCC, // payload
	}
	srcPort, dstPort, payload, err := ParseUDPHeader(udp)
	if err != nil {
		t.Fatal(err)
	}
	if srcPort != 4660 {
		t.Errorf("srcPort = %d, want 4660", srcPort)
	}
	if dstPort != 53 {
		t.Errorf("dstPort = %d, want 53", dstPort)
	}
	if len(payload) != 3 || payload[0] != 0xAA {
		t.Errorf("payload = %x", payload)
	}
}

func TestParseUDPHeader_TooShort(t *testing.T) {
	_, _, _, err := ParseUDPHeader([]byte{0x00, 0x35})
	if err == nil {
		t.Error("expected error for short UDP header")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd daemon && go test ./internal/tun/ -run "TestParseIPv4|TestParseUDP" -v`
Expected: FAIL — functions don't exist.

- [ ] **Step 3: Implement packet parser**

Create `daemon/internal/tun/packet.go`:

```go
package tun

import (
	"encoding/binary"
	"errors"
	"net"
)

var (
	errPacketTooShort = errors.New("packet too short")
	errNotIPv4        = errors.New("not IPv4")
)

// ParseIPv4Header extracts protocol, source/destination IPs, and header length
// from a raw IPv4 packet. Zero allocations — returns slices into pkt.
func ParseIPv4Header(pkt []byte) (proto uint8, srcIP, dstIP net.IP, hdrLen int, err error) {
	if len(pkt) < 20 {
		return 0, nil, nil, 0, errPacketTooShort
	}
	if pkt[0]>>4 != 4 {
		return 0, nil, nil, 0, errNotIPv4
	}
	hdrLen = int(pkt[0]&0x0F) * 4
	if len(pkt) < hdrLen {
		return 0, nil, nil, 0, errPacketTooShort
	}
	proto = pkt[9]
	srcIP = net.IP(pkt[12:16])
	dstIP = net.IP(pkt[16:20])
	return proto, srcIP, dstIP, hdrLen, nil
}

// ParseUDPHeader extracts ports and payload from a UDP segment.
func ParseUDPHeader(udp []byte) (srcPort, dstPort uint16, payload []byte, err error) {
	if len(udp) < 8 {
		return 0, 0, nil, errPacketTooShort
	}
	srcPort = binary.BigEndian.Uint16(udp[0:2])
	dstPort = binary.BigEndian.Uint16(udp[2:4])
	length := int(binary.BigEndian.Uint16(udp[4:6]))
	if length < 8 || len(udp) < length {
		return srcPort, dstPort, nil, errPacketTooShort
	}
	payload = udp[8:length]
	return srcPort, dstPort, payload, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/tun/ -run "TestParseIPv4|TestParseUDP" -v`
Expected: all PASS.

- [ ] **Step 5: Write failing test for BuildUDPPacket**

Add to `packet_test.go`:

```go
func TestBuildUDPPacket(t *testing.T) {
	srcIP := net.IP{8, 8, 8, 8}
	dstIP := net.IP{10, 0, 0, 1}
	payload := []byte{0xAA, 0xBB, 0xCC}

	pkt := BuildUDPPacket(srcIP, dstIP, 53, 4660, payload)

	// Parse it back
	proto, pSrc, pDst, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if proto != 17 {
		t.Errorf("proto = %d", proto)
	}
	if !pSrc.Equal(srcIP) {
		t.Errorf("srcIP = %v", pSrc)
	}
	if !pDst.Equal(dstIP) {
		t.Errorf("dstIP = %v", pDst)
	}

	sp, dp, pl, err := ParseUDPHeader(pkt[hdrLen:])
	if err != nil {
		t.Fatal(err)
	}
	if sp != 53 || dp != 4660 {
		t.Errorf("ports = %d:%d", sp, dp)
	}
	if len(pl) != 3 || pl[0] != 0xAA {
		t.Errorf("payload = %x", pl)
	}

	// Verify IP total length
	totalLen := binary.BigEndian.Uint16(pkt[2:4])
	if int(totalLen) != len(pkt) {
		t.Errorf("totalLen = %d, packet = %d", totalLen, len(pkt))
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `cd daemon && go test ./internal/tun/ -run TestBuildUDPPacket -v`
Expected: FAIL.

- [ ] **Step 7: Implement BuildUDPPacket**

Add to `packet.go`:

```go
// BuildUDPPacket constructs a complete IPv4+UDP packet with checksums.
func BuildUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	src4 := srcIP.To4()
	dst4 := dstIP.To4()

	udpLen := 8 + len(payload)
	totalLen := 20 + udpLen
	pkt := make([]byte, totalLen)

	// IPv4 header (20 bytes)
	pkt[0] = 0x45 // version=4, IHL=5
	// pkt[1] = 0 (DSCP/ECN)
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	// pkt[4:6] identification = 0
	// pkt[6:8] flags/fragment = 0
	pkt[8] = 64  // TTL
	pkt[9] = 17  // protocol = UDP
	// pkt[10:12] checksum (filled below)
	copy(pkt[12:16], src4)
	copy(pkt[16:20], dst4)

	// IP header checksum
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:20]))

	// UDP header
	udp := pkt[20:]
	binary.BigEndian.PutUint16(udp[0:2], srcPort)
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(udpLen))
	// udp[6:8] checksum (filled below)
	copy(udp[8:], payload)

	// UDP checksum (with pseudo-header)
	binary.BigEndian.PutUint16(udp[6:8], udpChecksum(src4, dst4, udp[:udpLen]))

	return pkt
}

func ipChecksum(hdr []byte) uint16 {
	var sum uint32
	for i := 0; i < len(hdr)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(hdr[i : i+2]))
	}
	for sum > 0xFFFF {
		sum = (sum >> 16) + (sum & 0xFFFF)
	}
	return ^uint16(sum)
}

func udpChecksum(srcIP, dstIP net.IP, udp []byte) uint16 {
	var sum uint32
	// Pseudo-header
	sum += uint32(srcIP[0])<<8 | uint32(srcIP[1])
	sum += uint32(srcIP[2])<<8 | uint32(srcIP[3])
	sum += uint32(dstIP[0])<<8 | uint32(dstIP[1])
	sum += uint32(dstIP[2])<<8 | uint32(dstIP[3])
	sum += 17 // protocol
	sum += uint32(len(udp))
	// UDP header + data
	for i := 0; i < len(udp)-1; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(udp[i : i+2]))
	}
	if len(udp)%2 == 1 {
		sum += uint32(udp[len(udp)-1]) << 8
	}
	for sum > 0xFFFF {
		sum = (sum >> 16) + (sum & 0xFFFF)
	}
	cs := ^uint16(sum)
	if cs == 0 {
		cs = 0xFFFF // UDP checksum 0 means "no checksum"; use 0xFFFF instead
	}
	return cs
}
```

- [ ] **Step 8: Run all packet tests**

Run: `cd daemon && go test ./internal/tun/ -run "TestParse|TestBuild" -v`
Expected: all PASS.

- [ ] **Step 9: Commit**

```bash
git add daemon/internal/tun/packet.go daemon/internal/tun/packet_test.go
git commit -m "feat: raw IP+UDP packet parser and builder for bypass"
```

---

### Task 2: NAT Table

**Files:**
- Create: `daemon/internal/tun/nat.go`
- Create: `daemon/internal/tun/nat_test.go`

- [ ] **Step 1: Write failing tests**

Create `daemon/internal/tun/nat_test.go`:

```go
package tun

import (
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestNATTable_HandlePacket(t *testing.T) {
	// Start a real UDP echo server
	echoAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	echoConn, err := net.ListenUDP("udp", echoAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer echoConn.Close()

	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := echoConn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			echoConn.WriteToUDP(buf[:n], addr)
		}
	}()

	var replies int64
	nat := NewNATTable(func(pkt []byte) {
		atomic.AddInt64(&replies, 1)
	})
	defer nat.Close()

	echoPort := uint16(echoConn.LocalAddr().(*net.UDPAddr).Port)
	err = nat.HandlePacket(
		net.IP{10, 0, 0, 1}, net.IP{127, 0, 0, 1},
		12345, echoPort,
		[]byte("hello"),
	)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for reply
	time.Sleep(100 * time.Millisecond)
	if r := atomic.LoadInt64(&replies); r != 1 {
		t.Errorf("replies = %d, want 1", r)
	}
}

func TestNATTable_ReuseEntry(t *testing.T) {
	echoAddr, _ := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	echoConn, _ := net.ListenUDP("udp", echoAddr)
	defer echoConn.Close()
	go func() {
		buf := make([]byte, 1024)
		for {
			n, addr, err := echoConn.ReadFromUDP(buf)
			if err != nil { return }
			echoConn.WriteToUDP(buf[:n], addr)
		}
	}()

	var replies int64
	nat := NewNATTable(func(pkt []byte) {
		atomic.AddInt64(&replies, 1)
	})
	defer nat.Close()

	port := uint16(echoConn.LocalAddr().(*net.UDPAddr).Port)
	for i := 0; i < 5; i++ {
		nat.HandlePacket(net.IP{10, 0, 0, 1}, net.IP{127, 0, 0, 1}, 12345, port, []byte("hi"))
	}

	time.Sleep(200 * time.Millisecond)
	if r := atomic.LoadInt64(&replies); r != 5 {
		t.Errorf("replies = %d, want 5", r)
	}

	// Should be one entry, not five
	nat.mu.RLock()
	count := len(nat.entries)
	nat.mu.RUnlock()
	if count != 1 {
		t.Errorf("entries = %d, want 1", count)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd daemon && go test ./internal/tun/ -run TestNATTable -v`
Expected: FAIL.

- [ ] **Step 3: Implement NAT table**

Create `daemon/internal/tun/nat.go`:

```go
package tun

import (
	"log"
	"net"
	"sync"
	"time"
)

const (
	natDefaultTimeout = 60 * time.Second
	natVoiceTimeout   = 120 * time.Second
	natCleanupTick    = 10 * time.Second
)

type natKey struct {
	srcIP   [4]byte
	dstIP   [4]byte
	srcPort uint16
	dstPort uint16
}

type natEntry struct {
	conn     net.Conn
	lastSeen time.Time
	timeout  time.Duration
}

type NATTable struct {
	mu      sync.RWMutex
	entries map[natKey]*natEntry
	onReply func(pkt []byte) // callback: writes response IP packet to TUN
	stop    chan struct{}
}

func NewNATTable(onReply func(pkt []byte)) *NATTable {
	n := &NATTable{
		entries: make(map[natKey]*natEntry),
		onReply: onReply,
		stop:    make(chan struct{}),
	}
	go n.cleanupLoop()
	return n
}

func (n *NATTable) Close() {
	close(n.stop)
	n.mu.Lock()
	for _, e := range n.entries {
		e.conn.Close()
	}
	n.entries = make(map[natKey]*natEntry)
	n.mu.Unlock()
}

func (n *NATTable) HandlePacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) error {
	src4 := srcIP.To4()
	dst4 := dstIP.To4()
	key := natKey{srcPort: srcPort, dstPort: dstPort}
	copy(key.srcIP[:], src4)
	copy(key.dstIP[:], dst4)

	n.mu.RLock()
	entry, exists := n.entries[key]
	n.mu.RUnlock()

	if exists {
		entry.lastSeen = time.Now()
		_, err := entry.conn.Write(payload)
		return err
	}

	// New flow — create socket
	conn, err := protectedDial("udp", net.JoinHostPort(dstIP.String(), itoa(dstPort)))
	if err != nil {
		return err
	}

	timeout := natDefaultTimeout
	if dstPort >= 50000 {
		timeout = natVoiceTimeout
	}

	entry = &natEntry{conn: conn, lastSeen: time.Now(), timeout: timeout}
	n.mu.Lock()
	n.entries[key] = entry
	n.mu.Unlock()

	// Send first packet
	if _, err := conn.Write(payload); err != nil {
		conn.Close()
		n.mu.Lock()
		delete(n.entries, key)
		n.mu.Unlock()
		return err
	}

	// Read goroutine: receive responses and build IP packets for TUN
	go func() {
		buf := make([]byte, 65535)
		for {
			conn.SetReadDeadline(time.Now().Add(timeout))
			nr, err := conn.Read(buf)
			if err != nil {
				return
			}
			// Build response: swap src/dst relative to original packet
			pkt := BuildUDPPacket(dst4, src4, dstPort, srcPort, buf[:nr])
			n.onReply(pkt)
		}
	}()

	return nil
}

func (n *NATTable) cleanupLoop() {
	ticker := time.NewTicker(natCleanupTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			n.cleanup()
		case <-n.stop:
			return
		}
	}
}

func (n *NATTable) cleanup() {
	now := time.Now()
	n.mu.Lock()
	for key, entry := range n.entries {
		if now.Sub(entry.lastSeen) > entry.timeout {
			entry.conn.Close()
			delete(n.entries, key)
		}
	}
	n.mu.Unlock()
}

func itoa(n uint16) string {
	return log.Prefix() // placeholder — replaced below
}
```

Wait — we need a proper `itoa`. Let me use `strconv` instead:

Replace the `itoa` function with an import of `"strconv"` and use `strconv.Itoa(int(dstPort))` inline. Actually, simpler — use `fmt.Sprintf`:

Replace the `itoa` function and update the `HandlePacket` call:

```go
package tun

import (
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	natDefaultTimeout = 60 * time.Second
	natVoiceTimeout   = 120 * time.Second
	natCleanupTick    = 10 * time.Second
)

type natKey struct {
	srcIP   [4]byte
	dstIP   [4]byte
	srcPort uint16
	dstPort uint16
}

type natEntry struct {
	conn     net.Conn
	lastSeen time.Time
	timeout  time.Duration
}

type NATTable struct {
	mu      sync.RWMutex
	entries map[natKey]*natEntry
	onReply func(pkt []byte)
	stop    chan struct{}
}

func NewNATTable(onReply func(pkt []byte)) *NATTable {
	n := &NATTable{
		entries: make(map[natKey]*natEntry),
		onReply: onReply,
		stop:    make(chan struct{}),
	}
	go n.cleanupLoop()
	return n
}

func (n *NATTable) Close() {
	close(n.stop)
	n.mu.Lock()
	for _, e := range n.entries {
		e.conn.Close()
	}
	n.entries = make(map[natKey]*natEntry)
	n.mu.Unlock()
}

func (n *NATTable) HandlePacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) error {
	src4 := srcIP.To4()
	dst4 := dstIP.To4()
	key := natKey{srcPort: srcPort, dstPort: dstPort}
	copy(key.srcIP[:], src4)
	copy(key.dstIP[:], dst4)

	n.mu.RLock()
	entry, exists := n.entries[key]
	n.mu.RUnlock()

	if exists {
		entry.lastSeen = time.Now()
		_, err := entry.conn.Write(payload)
		return err
	}

	conn, err := protectedDial("udp", fmt.Sprintf("%s:%d", dstIP, dstPort))
	if err != nil {
		return err
	}

	timeout := natDefaultTimeout
	if dstPort >= 50000 {
		timeout = natVoiceTimeout
	}

	entry = &natEntry{conn: conn, lastSeen: time.Now(), timeout: timeout}
	n.mu.Lock()
	n.entries[key] = entry
	n.mu.Unlock()

	if _, err := conn.Write(payload); err != nil {
		conn.Close()
		n.mu.Lock()
		delete(n.entries, key)
		n.mu.Unlock()
		return err
	}

	go func() {
		buf := make([]byte, 65535)
		for {
			conn.SetReadDeadline(time.Now().Add(timeout))
			nr, err := conn.Read(buf)
			if err != nil {
				return
			}
			pkt := BuildUDPPacket(dst4, src4, dstPort, srcPort, buf[:nr])
			n.onReply(pkt)
		}
	}()

	return nil
}

func (n *NATTable) cleanupLoop() {
	ticker := time.NewTicker(natCleanupTick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			n.cleanup()
		case <-n.stop:
			return
		}
	}
}

func (n *NATTable) cleanup() {
	now := time.Now()
	n.mu.Lock()
	for key, entry := range n.entries {
		if now.Sub(entry.lastSeen) > entry.timeout {
			entry.conn.Close()
			delete(n.entries, key)
		}
	}
	n.mu.Unlock()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/tun/ -run TestNATTable -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/nat.go daemon/internal/tun/nat_test.go
git commit -m "feat: NAT table for raw UDP bypass with auto-cleanup"
```

---

### Task 3: Raw UDP Handler

**Files:**
- Create: `daemon/internal/tun/rawudp.go`
- Create: `daemon/internal/tun/rawudp_test.go`

- [ ] **Step 1: Write failing tests for routing logic**

Create `daemon/internal/tun/rawudp_test.go`:

```go
package tun

import (
	"encoding/binary"
	"net"
	"testing"
)

// buildTestUDPPacket creates a minimal IPv4+UDP packet for testing.
func buildTestUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	udpLen := 8 + len(payload)
	totalLen := 20 + udpLen
	pkt := make([]byte, totalLen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(totalLen))
	pkt[9] = 17 // UDP
	copy(pkt[12:16], srcIP.To4())
	copy(pkt[16:20], dstIP.To4())
	binary.BigEndian.PutUint16(pkt[20:22], srcPort)
	binary.BigEndian.PutUint16(pkt[22:24], dstPort)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(udpLen))
	copy(pkt[28:], payload)
	return pkt
}

func TestRawUDPHandler_QUICBlock(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 443, []byte("quic"))
	if !h.Handle(pkt) {
		t.Error("UDP port 443 should be handled (dropped)")
	}
}

func TestRawUDPHandler_DNS(t *testing.T) {
	nat := NewNATTable(func(pkt []byte) {})
	defer nat.Close()
	h := &RawUDPHandler{rules: NewRules(), nat: nat}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{8, 8, 8, 8}, 5000, 53, []byte("dns"))
	if !h.Handle(pkt) {
		t.Error("DNS should be handled (bypassed)")
	}
}

func TestRawUDPHandler_TCPIgnored(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[9] = 6 // TCP
	if h.Handle(pkt) {
		t.Error("TCP should not be handled")
	}
}

func TestRawUDPHandler_IPv6Ignored(t *testing.T) {
	h := &RawUDPHandler{rules: NewRules()}
	pkt := make([]byte, 40)
	pkt[0] = 0x60 // IPv6
	if h.Handle(pkt) {
		t.Error("IPv6 should not be handled")
	}
}

func TestRawUDPHandler_ProxyOnly_NotSelected(t *testing.T) {
	nat := NewNATTable(func(pkt []byte) {})
	defer nat.Close()
	rules := NewRules()
	rules.SetMode(ModeProxyOnly)
	rules.SetApps([]string{"/applications/telegram.app"})
	h := &RawUDPHandler{rules: rules, nat: nat, procInfo: &mockProcInfo{path: "/usr/bin/curl"}}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 8080, []byte("data"))
	if !h.Handle(pkt) {
		t.Error("non-selected app should be bypassed")
	}
}

func TestRawUDPHandler_ProxyOnly_Selected(t *testing.T) {
	rules := NewRules()
	rules.SetMode(ModeProxyOnly)
	rules.SetApps([]string{"/applications/telegram.app"})
	h := &RawUDPHandler{rules: rules, procInfo: &mockProcInfo{path: "/applications/telegram.app/contents/macos/telegram"}}
	pkt := buildTestUDPPacket(net.IP{10, 0, 0, 1}, net.IP{1, 1, 1, 1}, 5000, 8080, []byte("data"))
	if h.Handle(pkt) {
		t.Error("selected app should go to gVisor (proxy)")
	}
}

type mockProcInfo struct {
	path string
}

func (m *mockProcInfo) FindProcess(network string, localPort uint16) (string, error) {
	return m.path, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd daemon && go test ./internal/tun/ -run TestRawUDP -v`
Expected: FAIL.

- [ ] **Step 3: Implement raw UDP handler**

Create `daemon/internal/tun/rawudp.go`:

```go
package tun

import (
	"log"
	"net"
	"strings"
)

// RawUDPHandler intercepts UDP packets before gVisor injection.
// Returns true if the packet was handled (bypass/drop), false if gVisor should process it (proxy).
type RawUDPHandler struct {
	nat      *NATTable
	rules    *Rules
	procInfo ProcessInfo
	selfPath string
}

func (h *RawUDPHandler) Handle(pkt []byte) bool {
	// Only handle IPv4
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return false
	}

	proto, srcIP, dstIP, hdrLen, err := ParseIPv4Header(pkt)
	if err != nil || proto != 17 { // 17 = UDP
		return false
	}

	if len(pkt) < hdrLen+8 {
		return false
	}

	srcPort, dstPort, payload, err := ParseUDPHeader(pkt[hdrLen:])
	if err != nil {
		return false
	}

	// QUIC block: drop UDP port 443
	if dstPort == 443 {
		return true
	}

	// DNS: always bypass
	if dstPort == 53 {
		if h.nat != nil {
			h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
		}
		return true
	}

	// Process lookup
	if !h.rules.NeedProcessLookup() {
		// proxy_all_except with no exclusions: everything proxied → gVisor
		return false
	}

	var appPath string
	if h.procInfo != nil {
		appPath, _ = h.procInfo.FindProcess("udp", srcPort)
	}

	// Self-detection: daemon's own traffic always bypasses
	if appPath != "" && h.selfPath != "" && strings.EqualFold(appPath, h.selfPath) {
		if h.nat != nil {
			h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
		}
		return true
	}

	shouldProxy := h.rules.ShouldProxy(appPath)

	// Voice bypass: selected voice apps on high ports
	if shouldProxy && dstPort >= 50000 && isVoiceApp(appPath) {
		shouldProxy = false
		log.Printf("[tun] raw UDP %s:%d from %s → bypass (voice)", net.IP(dstIP), dstPort, appPath)
	}

	if shouldProxy {
		return false // let gVisor handle for proxy
	}

	// Bypass via NAT
	if h.nat != nil {
		h.nat.HandlePacket(srcIP, dstIP, srcPort, dstPort, payload)
	}
	return true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd daemon && go test ./internal/tun/ -run TestRawUDP -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/rawudp.go daemon/internal/tun/rawudp_test.go
git commit -m "feat: raw UDP handler — routes bypass/proxy before gVisor"
```

---

### Task 4: Hook into Engine

**Files:**
- Modify: `daemon/internal/tun/engine.go`

- [ ] **Step 1: Add rawUDP field to Engine struct**

In `engine.go`, add to the `Engine` struct (after `selfPath` field, around line 51):

```go
rawUDP *RawUDPHandler
```

- [ ] **Step 2: Initialize rawUDP and NAT in Engine.Start**

In `Engine.Start()`, after `e.procInfo = newProcessInfo()` (line 173), add:

```go
nat := NewNATTable(func(pkt []byte) {
	// Write response packet to helper → TUN
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(pkt)))
	e.mu.Lock()
	conn := e.helperConn
	e.mu.Unlock()
	if conn != nil {
		conn.Write(lenBuf)
		conn.Write(pkt)
	}
})
e.rawUDP = &RawUDPHandler{
	nat:      nat,
	rules:    e.rules,
	procInfo: e.procInfo,
	selfPath: e.selfPath,
}
```

- [ ] **Step 3: Close NAT in Engine.stopLocked**

In `stopLocked()`, before `if e.stack != nil` (around line 223), add:

```go
if e.rawUDP != nil && e.rawUDP.nat != nil {
	e.rawUDP.nat.Close()
	e.rawUDP = nil
}
```

- [ ] **Step 4: Hook into bridgeInbound**

In `bridgeInbound()`, right before the `pkt := stack.NewPacketBuffer(...)` line (line 334), add:

```go
// Raw UDP bypass: handle before gVisor injection
if e.rawUDP != nil && e.rawUDP.Handle(data) {
	continue
}
```

- [ ] **Step 5: Write response packets safely**

The NAT reply callback writes to `helperConn`. But `bridgeOutbound` also writes to `helperConn`. We need a write mutex. Add a field to Engine:

```go
helperWriteMu sync.Mutex
```

Wrap the NAT reply callback:

```go
nat := NewNATTable(func(pkt []byte) {
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(pkt)))
	e.helperWriteMu.Lock()
	defer e.helperWriteMu.Unlock()
	e.mu.Lock()
	conn := e.helperConn
	e.mu.Unlock()
	if conn != nil {
		conn.Write(lenBuf)
		conn.Write(pkt)
	}
})
```

Also wrap `bridgeOutbound` writes with the same mutex. In `bridgeOutbound`, replace:

```go
binary.BigEndian.PutUint16(lenBuf, uint16(len(data)))
if _, err := conn.Write(lenBuf); err != nil {
	return
}
if _, err := conn.Write(data); err != nil {
	return
}
```

With:

```go
binary.BigEndian.PutUint16(lenBuf, uint16(len(data)))
e.helperWriteMu.Lock()
_, err1 := conn.Write(lenBuf)
if err1 == nil {
	_, err1 = conn.Write(data)
}
e.helperWriteMu.Unlock()
if err1 != nil {
	return
}
```

- [ ] **Step 6: Build and verify**

Run: `cd daemon && GOOS=windows GOARCH=amd64 go build -o /tmp/d.exe ./cmd/ && GOOS=darwin GOARCH=arm64 go build -o /tmp/d ./cmd/ && echo "OK"`
Expected: OK.

- [ ] **Step 7: Run all tests**

Run: `cd daemon && go test ./internal/tun/ -v`
Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add daemon/internal/tun/engine.go
git commit -m "feat: hook raw UDP bypass into engine — skip gVisor for bypass UDP"
```

---

### Task 5: Build, Test, Release

- [ ] **Step 1: Run full test suite**

```bash
cd pkg && go test ./... && cd ../daemon && go test ./... && cd ../test && go test -v -timeout 30s
```
Expected: all PASS.

- [ ] **Step 2: Build all targets**

```bash
make build-server && make build-daemon
```

- [ ] **Step 3: Bump version and tag**

Bump `client/package.json` version to `1.17.0` (minor — new feature).

```bash
git add -A && git commit -m "feat: raw UDP bypass — skip gVisor for non-proxy UDP traffic"
git push origin main && git tag v1.17.0 && git push origin v1.17.0
```
