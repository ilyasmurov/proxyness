package arq

import "sync"

// RecvBuffer provides per-stream reordering for incoming data packets.
// It delivers packets to the caller in sequence order, buffering out-of-order
// arrivals until the gap is filled.
type RecvBuffer struct {
	mu       sync.Mutex
	expected uint32
	buffer   map[uint32][]byte
	maxBuf   int
	deliver  func(seq uint32, data []byte)
}

// NewRecvBuffer creates a RecvBuffer that buffers at most maxBuf out-of-order
// packets and calls deliverFn for each packet in sequence order.
func NewRecvBuffer(maxBuf int, deliverFn func(seq uint32, data []byte)) *RecvBuffer {
	return &RecvBuffer{
		expected: 0,
		buffer:   make(map[uint32][]byte),
		maxBuf:   maxBuf,
		deliver:  deliverFn,
	}
}

// Insert delivers the packet with the given sequence number. Packets below
// the expected sequence are silently dropped as duplicates. Packets at the
// expected sequence are delivered immediately, then consecutive buffered
// packets are flushed. Packets above the expected sequence are buffered if
// space permits, otherwise dropped.
func (rb *RecvBuffer) Insert(seq uint32, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if seq < rb.expected {
		// duplicate or already delivered
		return
	}

	if seq == rb.expected {
		rb.deliver(seq, data)
		rb.expected++
		// flush consecutive buffered packets
		for {
			d, ok := rb.buffer[rb.expected]
			if !ok {
				break
			}
			delete(rb.buffer, rb.expected)
			rb.deliver(rb.expected, d)
			rb.expected++
		}
		return
	}

	// seq > expected: buffer if space available
	if _, exists := rb.buffer[seq]; exists {
		// already buffered
		return
	}
	if len(rb.buffer) >= rb.maxBuf {
		// buffer full, drop
		return
	}
	// copy data to avoid retaining caller's slice
	cp := make([]byte, len(data))
	copy(cp, data)
	rb.buffer[seq] = cp
}

// Expected returns the next sequence number the buffer is waiting for.
func (rb *RecvBuffer) Expected() uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.expected
}
