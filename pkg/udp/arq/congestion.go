package arq

import (
	"sync"
	"time"
)

const (
	initCwnd = 128
	minCwnd  = 32
	maxCwnd  = 512
	lossBeta = 0.7
)

// recoveryEpoch is the minimum time between congestion window reductions.
// All losses within one epoch are treated as a single congestion event.
const recoveryEpoch = 500 * time.Millisecond

// CongestionControl implements simple AIMD with always-slow-start recovery.
//
// This is a TCP-over-UDP proxy: inner TCP handles end-to-end congestion.
// The outer CC only prevents burst-induced congestion collapse on the UDP path.
// Always-slow-start means fast recovery (cwnd doubles each RTT) after any loss,
// avoiding the slow CUBIC congestion avoidance phase.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     float64
	inFlight int
	lastLoss time.Time
	notify   chan struct{}
}

// NewCongestionControl creates a new CongestionControl starting in slow start.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:   initCwnd,
		notify: make(chan struct{}, maxCwnd),
	}
	for i := 0; i < initCwnd; i++ {
		cc.notify <- struct{}{}
	}
	return cc
}

// Window returns the current congestion window size.
func (cc *CongestionControl) Window() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	w := int(cc.cwnd)
	if w > maxCwnd {
		return maxCwnd
	}
	return w
}

// AcquireSlot blocks until inFlight < cwnd, atomically increments inFlight,
// and returns true. Returns false if done is closed.
func (cc *CongestionControl) AcquireSlot(done <-chan struct{}) bool {
	for {
		select {
		case <-cc.notify:
			cc.mu.Lock()
			if cc.inFlight < int(cc.cwnd) {
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

// OnAck releases n in-flight slots and grows the window (slow start: +1 per ACK).
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()

	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}

	// Always slow start: cwnd++ per ACK → doubles each RTT.
	cc.cwnd += float64(n)
	if cc.cwnd > maxCwnd {
		cc.cwnd = maxCwnd
	}

	available := int(cc.cwnd) - cc.inFlight
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

// OnDrop releases n in-flight slots without growing the window.
func (cc *CongestionControl) OnDrop(n int) {
	cc.mu.Lock()
	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}
	available := int(cc.cwnd) - cc.inFlight
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

// OnLoss handles a loss event: reduce cwnd by lossBeta.
// Recovery epoch prevents cascading reductions from a single loss event.
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()

	if !cc.lastLoss.IsZero() && time.Since(cc.lastLoss) < recoveryEpoch {
		cc.mu.Unlock()
		return
	}

	cc.cwnd *= lossBeta
	if cc.cwnd < minCwnd {
		cc.cwnd = minCwnd
	}
	cc.lastLoss = time.Now()

	cc.mu.Unlock()
}

// InFlight returns the number of unacknowledged in-flight packets.
func (cc *CongestionControl) InFlight() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight
}

// Stats returns cwnd, inFlight, and available slot count for diagnostics.
func (cc *CongestionControl) Stats() (cwnd int, inFlight int, slots int) {
	cc.mu.Lock()
	w := int(cc.cwnd)
	f := cc.inFlight
	avail := w - f
	if avail < 0 {
		avail = 0
	}
	cc.mu.Unlock()
	return w, f, avail
}

// SignalAll is a no-op kept for API compatibility.
func (cc *CongestionControl) SignalAll() {}
