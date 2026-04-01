package tun

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const (
	natDefaultTimeout  = 60 * time.Second
	natVoiceTimeout    = 120 * time.Second
	natCleanupInterval = 10 * time.Second
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

// NATTable maps inbound UDP flows to outbound Go sockets.
// When a bypass UDP packet arrives, it either reuses an existing socket
// or creates a new one via protectedDial. A read goroutine per entry
// receives responses and calls onReply with a raw IP+UDP response packet.
type NATTable struct {
	mu      sync.RWMutex
	entries map[natKey]*natEntry
	onReply func(pkt []byte) // callback to write response IP+UDP packet back to TUN
	dial    func(network, address string) (net.Conn, error)
	stop    chan struct{}
}

// NewNATTable creates a NAT table and starts the cleanup goroutine.
// Uses protectedDial to create UDP sockets that bypass TUN routing.
func NewNATTable(onReply func(pkt []byte)) *NATTable {
	t := &NATTable{
		entries: make(map[natKey]*natEntry),
		onReply: onReply,
		dial:    protectedDial,
		stop:    make(chan struct{}),
	}
	go t.cleanupLoop()
	return t
}

// Close stops the cleanup goroutine and closes all sockets.
func (t *NATTable) Close() {
	close(t.stop)

	t.mu.Lock()
	defer t.mu.Unlock()
	for k, e := range t.entries {
		e.conn.Close()
		delete(t.entries, k)
	}
}

// HandlePacket sends the UDP payload to dstIP:dstPort. If no entry exists
// for this flow, a new socket is created via the dial function and a read
// goroutine is started.
func (t *NATTable) HandlePacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) error {
	key := makeKey(srcIP, dstIP, srcPort, dstPort)

	t.mu.RLock()
	entry, ok := t.entries[key]
	t.mu.RUnlock()

	if ok {
		entry.lastSeen = time.Now()
		_, err := entry.conn.Write(payload)
		return err
	}

	// Create new entry
	addr := fmt.Sprintf("%s:%d", dstIP, dstPort)
	conn, err := t.dial("udp", addr)
	if err != nil {
		return fmt.Errorf("nat dial %s: %w", addr, err)
	}

	timeout := natDefaultTimeout
	if dstPort >= 50000 {
		timeout = natVoiceTimeout
	}

	entry = &natEntry{
		conn:     conn,
		lastSeen: time.Now(),
		timeout:  timeout,
	}

	t.mu.Lock()
	// Double-check: another goroutine may have created the entry
	if existing, ok := t.entries[key]; ok {
		t.mu.Unlock()
		conn.Close()
		existing.lastSeen = time.Now()
		_, err := existing.conn.Write(payload)
		return err
	}
	t.entries[key] = entry
	t.mu.Unlock()

	// Start read goroutine for responses
	go t.readLoop(key, entry, srcIP, dstIP, srcPort, dstPort)

	_, err = conn.Write(payload)
	return err
}

// readLoop reads responses from the remote socket and builds IP+UDP response
// packets with swapped src/dst, passing them to onReply for injection back
// into the TUN device.
func (t *NATTable) readLoop(key natKey, entry *natEntry, srcIP, dstIP net.IP, srcPort, dstPort uint16) {
	buf := make([]byte, 65535)
	for {
		entry.conn.SetReadDeadline(time.Now().Add(entry.timeout))
		n, err := entry.conn.Read(buf)
		if err != nil {
			t.removeEntry(key)
			return
		}

		// Build response packet: swap src/dst so it arrives at the original sender
		pkt := BuildUDPPacket(dstIP, srcIP, dstPort, srcPort, buf[:n])
		t.onReply(pkt)
	}
}

func (t *NATTable) cleanupLoop() {
	ticker := time.NewTicker(natCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C:
			t.cleanup()
		}
	}
}

func (t *NATTable) cleanup() {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, e := range t.entries {
		if now.Sub(e.lastSeen) > e.timeout {
			log.Printf("[nat] cleaning up expired entry %v:%d -> %v:%d",
				net.IP(k.srcIP[:]), k.srcPort, net.IP(k.dstIP[:]), k.dstPort)
			e.conn.Close()
			delete(t.entries, k)
		}
	}
}

func (t *NATTable) removeEntry(key natKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if e, ok := t.entries[key]; ok {
		e.conn.Close()
		delete(t.entries, key)
	}
}

func makeKey(srcIP, dstIP net.IP, srcPort, dstPort uint16) natKey {
	var k natKey
	copy(k.srcIP[:], srcIP.To4())
	copy(k.dstIP[:], dstIP.To4())
	k.srcPort = srcPort
	k.dstPort = dstPort
	return k
}
