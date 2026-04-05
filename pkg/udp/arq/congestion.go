package arq

import (
	"sync"
)

const (
	// fixedCwnd is the congestion window size. No dynamic congestion control.
	//
	// This is a TCP-over-UDP proxy: inner TCP already handles congestion control.
	// The outer UDP transport provides reliability (ARQ retransmission) and must
	// not be the throughput bottleneck. A fixed window avoids the double-punishment
	// problem (outer CC + inner CC both reducing on loss).
	//
	// 1024 * 1340 bytes / 60ms RTT = 22 MB/s theoretical max — enough headroom.
	fixedCwnd = 1024
)

// CongestionControl provides flow control via a fixed send window.
// It does NOT implement congestion control — inner TCP handles that.
// The notify channel wakes blocked senders when slots become available.
type CongestionControl struct {
	mu       sync.Mutex
	inFlight int
	notify   chan struct{}
}

// NewCongestionControl creates a new fixed-window flow controller.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		notify: make(chan struct{}, fixedCwnd),
	}
	for i := 0; i < fixedCwnd; i++ {
		cc.notify <- struct{}{}
	}
	return cc
}

// Window returns the fixed congestion window size.
func (cc *CongestionControl) Window() int {
	return fixedCwnd
}

// AcquireSlot blocks until inFlight < fixedCwnd, atomically increments inFlight,
// and returns true. Returns false if done is closed.
func (cc *CongestionControl) AcquireSlot(done <-chan struct{}) bool {
	for {
		select {
		case <-cc.notify:
			cc.mu.Lock()
			if cc.inFlight < fixedCwnd {
				cc.inFlight++
				cc.mu.Unlock()
				return true
			}
			cc.mu.Unlock()
		case <-done:
			return false
		}
	}
}

// OnAck releases n in-flight slots and wakes blocked senders.
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()
	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}
	available := fixedCwnd - cc.inFlight
	if available < 0 {
		available = 0
	}
	cc.mu.Unlock()

	for i := 0; i < available; i++ {
		select {
		case cc.notify <- struct{}{}:
		default:
		}
	}
}

// OnDrop releases n in-flight slots without any window adjustment.
func (cc *CongestionControl) OnDrop(n int) {
	cc.OnAck(n)
}

// OnLoss is a no-op. Inner TCP handles congestion control.
func (cc *CongestionControl) OnLoss() {}

// InFlight returns the number of unacknowledged in-flight packets.
func (cc *CongestionControl) InFlight() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight
}

// Stats returns cwnd, inFlight, and available slot count for diagnostics.
func (cc *CongestionControl) Stats() (cwnd int, inFlight int, slots int) {
	cc.mu.Lock()
	f := cc.inFlight
	avail := fixedCwnd - f
	if avail < 0 {
		avail = 0
	}
	cc.mu.Unlock()
	return fixedCwnd, f, avail
}

// SignalAll is a no-op kept for API compatibility.
func (cc *CongestionControl) SignalAll() {}
