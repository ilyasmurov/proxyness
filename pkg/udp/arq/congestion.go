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
// The notify channel is a wake-up signal only; the authoritative send gate
// is inFlight < cwnd checked under the mutex inside AcquireSlot.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     float64
	ssthresh float64
	wMax     float64
	lastLoss time.Time
	inFlight int
	notify   chan struct{} // wake-up signal for blocked senders, capacity = maxCwnd
}

// NewCongestionControl creates a new CongestionControl starting in slow start.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:     initCwnd,
		ssthresh: math.MaxFloat64,
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

// OnLoss handles a loss event: set ssthresh and cwnd via CUBIC beta.
// Implements a recovery epoch: all losses within one minRTO are treated as
// a single congestion event (like TCP Fast Recovery). Without this, a burst
// of lost packets triggers cascading OnLoss calls that crash cwnd to minimum.
//
// At minimum cwnd, full reset to slow start — clears wMax and lastLoss so
// CUBIC doesn't jump cwnd using a stale peak (e.g. wMax=50 → cwnd 10→35
// in one ACK → burst → collapse → 0.0 MB/s). Clean slow start discovers
// real capacity gradually (doubles each RTT).
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()

	// Suppress duplicate loss signals within the same recovery epoch.
	// A burst of packets lost together should reduce cwnd only once.
	if !cc.lastLoss.IsZero() && time.Since(cc.lastLoss) < minRTO {
		cc.mu.Unlock()
		return
	}

	if cc.cwnd <= float64(initCwnd) {
		cc.ssthresh = math.MaxFloat64
		cc.wMax = 0
		cc.lastLoss = time.Time{}
	} else {
		cc.wMax = cc.cwnd
		cc.ssthresh = cc.cwnd * cubicBeta
		if cc.ssthresh < float64(initCwnd) {
			cc.ssthresh = float64(initCwnd)
		}
		cc.cwnd = cc.ssthresh
		cc.lastLoss = time.Now()
	}
	cc.mu.Unlock()
	// No signal needed — reduced cwnd means fewer slots, not more.
	// AcquireSlot re-checks inFlight < cwnd under the mutex;
	// stale signals in the channel cause harmless spurious wakeups.
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
	avail := w - f
	if avail < 0 {
		avail = 0
	}
	cc.mu.Unlock()
	return w, f, avail
}

// SignalAll is a no-op kept for API compatibility.
// Close is handled by closing the done channel passed to AcquireSlot.
func (cc *CongestionControl) SignalAll() {}
