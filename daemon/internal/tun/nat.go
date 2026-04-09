package tun

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

const (
	// natDefaultTimeout was 60s but most non-voice UDP flows we see (DNS,
	// short HTTPS-over-QUIC fallbacks, mDNS) are one-shot — request goes
	// out, response comes back, that's it. Holding the socket for a full
	// minute meant browsers doing hundreds of DNS lookups while watching
	// YouTube ended up with thousands of live NAT entries, each with a
	// goroutine and a 64KB buffer. Heap profile in 1.28.16 showed 1 GB
	// resident in NATTable.readLoop alone. 10s is plenty for any
	// reasonable single request/response and lets the cleanup loop
	// reclaim memory in a timely fashion.
	natDefaultTimeout  = 10 * time.Second
	natVoiceTimeout    = 120 * time.Second
	natCleanupInterval = 5 * time.Second

	// natReadBufSize was 65535 (max UDP datagram). Shrinking to 2048 covers
	// any realistic MTU + headroom and saves ~63KB per live entry — at
	// thousands of entries that's the difference between 1 GB resident and
	// ~30 MB. The pool below recycles these between goroutines so we don't
	// pay an allocation per readLoop start either.
	natReadBufSize = 2048
)

// natBufPool reuses readLoop buffers across NAT entry lifetimes.
var natBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, natReadBufSize)
		return &buf
	},
}

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
//
// Important: srcIP and dstIP arrive as slice aliases into the caller's
// packet buffer (ParseIPv4Header returns net.IP slices over pkt[12:16] /
// pkt[16:20]). Since 1.28.9 bridgeInbound reuses its pktBuf across
// iterations, those aliases would be silently corrupted on the next
// inbound packet — breaking the readLoop goroutine that builds reply
// packets from them. We clone both IPs before spawning the goroutine
// so readLoop owns its own memory. payload is consumed synchronously
// by conn.Write before HandlePacket returns, so no clone needed for it.
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

	// Clone IPs before handing to the goroutine — they alias the caller's
	// packet buffer which gets reused after this call returns.
	srcIPCopy := append(net.IP(nil), srcIP...)
	dstIPCopy := append(net.IP(nil), dstIP...)

	// Start read goroutine for responses
	go t.readLoop(key, entry, srcIPCopy, dstIPCopy, srcPort, dstPort)

	_, err = conn.Write(payload)
	return err
}

// readLoop reads responses from the remote socket and builds IP+UDP response
// packets with swapped src/dst, passing them to onReply for injection back
// into the TUN device.
func (t *NATTable) readLoop(key natKey, entry *natEntry, srcIP, dstIP net.IP, srcPort, dstPort uint16) {
	bufPtr := natBufPool.Get().(*[]byte)
	buf := *bufPtr
	defer natBufPool.Put(bufPtr)
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
