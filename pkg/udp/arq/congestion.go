package arq

import (
	"math"
	"sync"
	"time"
)

const (
	initCwnd  = 128
	minCwnd   = 64
	maxCwnd   = 256
	cubicBeta = 0.9
	cubicC    = 0.4
)

// CongestionControl implements CUBIC congestion control algorithm.
// The notify channel is a wake-up signal only; the authoritative send gate
// is inFlight < cwnd checked under the mutex inside AcquireSlot.
type CongestionControl struct {
	mu        sync.Mutex
	cwnd      float64
	ssthresh  float64
	wMax      float64
	lastLoss  time.Time
	inFlight  int
	lossCount int          // total loss events (for diagnostics)
	notify    chan struct{} // wake-up signal for blocked senders, capacity = maxCwnd
}

// NewCongestionControl creates a new CongestionControl starting in slow start.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:     initCwnd,
		ssthresh: 192, // exit slow start early to prevent burst that ISPs drop
		notify:   make(chan struct{}, maxCwnd),
	}
	// Pre-fill signals for initial cwnd
	for i := 0; i < initCwnd; i++ {
		cc.notify <- struct{}{}
	}
	return cc
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
			// spurious wake (stale signal after OnLoss), retry
		case <-done:
			return false
		}
	}
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

	available := int(cc.cwnd) - cc.inFlight
	if available < 0 {
		available = 0
	}
	cc.mu.Unlock()

	// Wake up to 'available' blocked senders
	for i := 0; i < available; i++ {
		select {
		case cc.notify <- struct{}{}:
		default:
		}
	}
}

// OnDrop releases n cwnd slots without growing the window (used when packets
// are dropped after max retransmits — these are NOT successful deliveries).
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

// recoveryEpoch is the minimum time between congestion window reductions.
// All losses within one epoch are treated as a single congestion event.
// 500ms (~8 RTTs at 60ms) balances fast convergence with stability.
const recoveryEpoch = 500 * time.Millisecond

// OnLoss handles a loss event: reduce cwnd and enter congestion avoidance.
//
// Standard CUBIC behavior: set ssthresh = cwnd * beta, then grow linearly.
// Always-slow-start was causing burst-loss oscillation because exponential
// growth after each loss flooded the UDP path faster than ISPs could absorb.
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()

	// Suppress duplicate loss signals within the same recovery epoch.
	if !cc.lastLoss.IsZero() && time.Since(cc.lastLoss) < recoveryEpoch {
		cc.mu.Unlock()
		return
	}

	cc.wMax = cc.cwnd
	newCwnd := cc.cwnd * cubicBeta
	if newCwnd < float64(minCwnd) {
		newCwnd = float64(minCwnd)
	}
	cc.ssthresh = newCwnd
	cc.cwnd = newCwnd
	cc.lossCount++
	cc.lastLoss = time.Now()

	cc.mu.Unlock()
}

// cubicGrow applies the CUBIC window growth function.
// Must be called with mu held.
func (cc *CongestionControl) cubicGrow() {
	if cc.lastLoss.IsZero() {
		// No loss yet — grow faster than Reno (additive increase of 0.5 per ACK
		// instead of 1/cwnd) to ramp up quickly on fresh connections.
		cc.cwnd += 0.5
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

// Stats returns cwnd, inFlight, available slot count, and total loss events.
func (cc *CongestionControl) Stats() (cwnd int, inFlight int, slots int, losses int) {
	cc.mu.Lock()
	w := int(cc.cwnd)
	f := cc.inFlight
	avail := w - f
	if avail < 0 {
		avail = 0
	}
	l := cc.lossCount
	cc.mu.Unlock()
	return w, f, avail, l
}

// SignalAll is a no-op kept for API compatibility.
// Close is handled by closing the done channel passed to AcquireSlot.
func (cc *CongestionControl) SignalAll() {}
