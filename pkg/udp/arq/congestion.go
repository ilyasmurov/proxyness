package arq

import (
	"math"
	"sync"
	"time"
)

const (
	initCwnd  = 10
	maxCwnd   = 1024
	cubicBeta = 0.7
	cubicC    = 0.4
)

// CongestionControl implements CUBIC congestion control algorithm.
// Send slots are managed via a channel semaphore for clean select/context integration.
// After every cwnd or inFlight change, adjustSlots() syncs the channel to
// exactly max(0, cwnd - inFlight) tokens, preventing slot leaks.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     float64
	ssthresh float64
	wMax     float64
	lastLoss time.Time
	inFlight int
	slots    chan struct{} // buffered semaphore, capacity = maxCwnd
}

// NewCongestionControl creates a new CongestionControl starting in slow start.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:     initCwnd,
		ssthresh: math.MaxFloat64,
		slots:    make(chan struct{}, maxCwnd),
	}
	// Pre-fill slots for initial cwnd
	for i := 0; i < initCwnd; i++ {
		cc.slots <- struct{}{}
	}
	return cc
}

// adjustSlots sets the channel to exactly max(0, cwnd - inFlight) tokens.
// Must be called with mu held for reading cwnd/inFlight, but releases mu
// before touching the channel to avoid deadlock with WaitForSlot.
func (cc *CongestionControl) adjustSlots() {
	target := int(cc.cwnd) - cc.inFlight
	if target < 0 {
		target = 0
	}
	cc.mu.Unlock()

	current := len(cc.slots)
	if current < target {
		// Add slots
		for i := 0; i < target-current; i++ {
			select {
			case cc.slots <- struct{}{}:
			default:
			}
		}
	} else if current > target {
		// Drain excess slots
		for i := 0; i < current-target; i++ {
			select {
			case <-cc.slots:
			default:
				return
			}
		}
	}
}

// Window returns the current congestion window size, capped at maxCwnd.
func (cc *CongestionControl) Window() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	w := int(cc.cwnd)
	if w > maxCwnd {
		return maxCwnd
	}
	return w
}

// CanSend reports whether a new packet can be sent (inFlight < cwnd).
func (cc *CongestionControl) CanSend() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight < int(cc.cwnd)
}

// WaitForSlot blocks until a send slot is available or done is closed.
// Returns false if done was closed, true if a slot became available.
func (cc *CongestionControl) WaitForSlot(done <-chan struct{}) bool {
	select {
	case <-cc.slots:
		return true
	case <-done:
		return false
	}
}

// OnSend records that a packet has been sent.
func (cc *CongestionControl) OnSend() {
	cc.mu.Lock()
	cc.inFlight++
	cc.adjustSlots() // unlocks mu
}

// OnAck records that n packets were acknowledged and updates the window.
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()

	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}

	for i := 0; i < n; i++ {
		if cc.cwnd < cc.ssthresh {
			cc.cwnd++
		} else {
			cc.cubicGrow()
		}

		if cc.cwnd > maxCwnd {
			cc.cwnd = maxCwnd
		}
	}

	cc.adjustSlots() // unlocks mu
}

// OnLoss handles a loss event: set ssthresh and cwnd via CUBIC beta.
// When cwnd is at initCwnd (minimum), we keep ssthresh at MaxFloat64 so that
// the next recovery uses slow start (exponential growth) instead of CUBIC
// congestion avoidance (glacial growth).
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()
	if cc.cwnd <= float64(initCwnd) {
		cc.lastLoss = time.Now()
		cc.adjustSlots() // unlocks mu
		return
	}
	cc.wMax = cc.cwnd
	cc.ssthresh = cc.cwnd * cubicBeta
	if cc.ssthresh < float64(initCwnd) {
		cc.ssthresh = float64(initCwnd)
	}
	cc.cwnd = cc.ssthresh
	cc.lastLoss = time.Now()
	cc.adjustSlots() // unlocks mu
}

// cubicGrow applies the CUBIC window growth function.
// Must be called with mu held.
func (cc *CongestionControl) cubicGrow() {
	if cc.lastLoss.IsZero() {
		cc.cwnd += 1.0 / cc.cwnd
		return
	}
	t := time.Since(cc.lastLoss).Seconds()
	k := math.Cbrt(cc.wMax * (1 - cubicBeta) / cubicC)
	w := cubicC*math.Pow(t-k, 3) + cc.wMax
	if w > cc.cwnd {
		cc.cwnd = w
	} else {
		cc.cwnd += 1.0 / cc.cwnd
	}
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
	cc.mu.Unlock()
	return w, f, len(cc.slots)
}

// SignalAll is a no-op kept for API compatibility.
// Close is handled by closing the done channel passed to WaitForSlot.
func (cc *CongestionControl) SignalAll() {}
