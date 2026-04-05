package arq

import (
	"sync"
)

const (
	// packetMSS is the assumed maximum segment size for cwnd calculations.
	packetMSS = 1340

	// startupCwnd is the initial cwnd before any bandwidth estimate is available.
	// Conservative to avoid the burst-loss that plagued all previous CUBIC iterations.
	startupCwnd = 32

	// minCwnd prevents the window from collapsing to zero during startup.
	minCwnd = 4

	// maxCwnd is a safety cap to prevent buffer bloat even with high BW estimates.
	maxCwnd = 512

	// cwndGain is the multiplier over BDP for the congestion window.
	// 2.0 allows enough headroom for retransmits and bursty ACKs.
	cwndGain = 2.0

	// pacingGain is the multiplier for pacing rate over estimated maxBW.
	// 1.25 probes for more bandwidth (BBR-style).
	pacingGain = 1.25
)

// CongestionControl implements a BBR-like rate-based congestion control.
//
// Instead of reacting to packet loss (CUBIC), it estimates the actual delivery
// rate and sets cwnd = BDP * gain. Random packet drops on ISP paths don't
// reduce cwnd — only a decrease in measured delivery rate does.
//
// The notify channel is a wake-up signal only; the authoritative send gate
// is inFlight < cwnd checked under the mutex inside AcquireSlot.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     int
	inFlight int
	bwe      *BandwidthEstimator
	notify   chan struct{} // wake-up signal for blocked senders
}

// NewCongestionControl creates a new rate-based CongestionControl.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:   startupCwnd,
		bwe:    NewBandwidthEstimator(),
		notify: make(chan struct{}, maxCwnd),
	}
	// Pre-fill signals for initial cwnd
	for i := 0; i < startupCwnd; i++ {
		cc.notify <- struct{}{}
	}
	return cc
}

// BWE returns the bandwidth estimator for use by the Controller.
func (cc *CongestionControl) BWE() *BandwidthEstimator {
	return cc.bwe
}

// Window returns the current congestion window size.
func (cc *CongestionControl) Window() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.cwnd
}

// AcquireSlot blocks until inFlight < cwnd, atomically increments inFlight,
// and returns true. Returns false if done is closed.
func (cc *CongestionControl) AcquireSlot(done <-chan struct{}) bool {
	for {
		select {
		case <-cc.notify:
			cc.mu.Lock()
			if cc.inFlight < cc.cwnd {
				cc.inFlight++
				cc.mu.Unlock()
				return true
			}
			cc.mu.Unlock()
			// spurious wake, retry
		case <-done:
			return false
		}
	}
}

// OnAck records that n packets were acknowledged and recalculates cwnd
// from the bandwidth estimate.
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()

	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}

	// Recalculate cwnd from BDP estimate
	cc.recalcCwnd()

	available := cc.cwnd - cc.inFlight
	if available < 0 {
		available = 0
	}
	cc.mu.Unlock()

	// Wake up blocked senders
	for i := 0; i < available; i++ {
		select {
		case cc.notify <- struct{}{}:
		default:
		}
	}
}

// OnDrop releases n cwnd slots without growing the window (used when packets
// are dropped after max retransmits).
func (cc *CongestionControl) OnDrop(n int) {
	cc.mu.Lock()
	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}
	available := cc.cwnd - cc.inFlight
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

// OnLoss is a no-op in rate-based CC. Random packet loss on ISP paths is not
// a congestion signal. cwnd is driven by measured delivery rate, not loss events.
//
// This is the fundamental difference from CUBIC: we don't punish cwnd for every
// lost packet. Only a sustained decrease in delivery rate will lower cwnd.
func (cc *CongestionControl) OnLoss() {
	// intentionally empty
}

// recalcCwnd sets cwnd from the bandwidth estimate. Must be called with mu held.
func (cc *CongestionControl) recalcCwnd() {
	target := cc.bwe.CwndFromBDP(packetMSS, cwndGain)
	if target > 0 {
		if cc.bwe.IsStable() {
			// Steady state: track BDP directly
			cc.cwnd = target
		} else if target > cc.cwnd {
			// Startup: only grow, don't shrink — early samples are noisy
			cc.cwnd = target
		}
	}
	// else: keep current cwnd (no estimate yet)

	// Enforce bounds
	if cc.cwnd < minCwnd {
		cc.cwnd = minCwnd
	}
	if cc.cwnd > maxCwnd {
		cc.cwnd = maxCwnd
	}
}

// InFlight returns the number of unacknowledged in-flight packets.
func (cc *CongestionControl) InFlight() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight
}

// Stats returns cwnd, inFlight, available slot count, and total loss events.
// Loss events is always 0 in rate-based CC (kept for API compatibility).
func (cc *CongestionControl) Stats() (cwnd int, inFlight int, slots int, losses int) {
	cc.mu.Lock()
	w := cc.cwnd
	f := cc.inFlight
	avail := w - f
	if avail < 0 {
		avail = 0
	}
	cc.mu.Unlock()
	return w, f, avail, 0
}

// SignalAll is a no-op kept for API compatibility.
func (cc *CongestionControl) SignalAll() {}
