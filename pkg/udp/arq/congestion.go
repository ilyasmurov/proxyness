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

	// startupPacingGain is the pacing multiplier during STARTUP phase.
	// 2/ln(2) ≈ 2.885 — doubles sending rate each RTT (same as BBR STARTUP).
	startupPacingGain = 2.885

	// steadyPacingGain is the pacing multiplier during steady-state.
	// 1.25 probes for more bandwidth without excessive buffer bloat.
	steadyPacingGain = 1.25

	// startupFullBWCount is how many rounds of non-increasing delivery rate
	// signal that the pipe is full and STARTUP should end.
	startupFullBWCount = 3

	// startupFullBWThresh is the minimum growth ratio to consider delivery
	// rate "still increasing" during STARTUP (1.25 = 25% growth per round).
	startupFullBWThresh = 1.25
)

// CongestionControl implements a BBR-like rate-based congestion control.
//
// Instead of reacting to packet loss (CUBIC), it estimates the actual delivery
// rate and sets cwnd = BDP * gain. Random packet drops on ISP paths don't
// reduce cwnd — only a decrease in measured delivery rate does.
//
// Phases:
//   - STARTUP: high pacing gain (2.885x) to double sending rate each RTT.
//     Exits when delivery rate stops growing for 3 consecutive rounds.
//   - STEADY: moderate pacing gain (1.25x) to probe for bandwidth without bloat.
//
// The notify channel is a wake-up signal only; the authoritative send gate
// is inFlight < cwnd checked under the mutex inside AcquireSlot.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     int
	inFlight int
	bwe      *BandwidthEstimator
	notify   chan struct{} // wake-up signal for blocked senders

	// STARTUP phase tracking
	inStartup      bool
	fullBWCount    int     // consecutive rounds with <25% growth
	lastMaxBW      float64 // maxBW at end of previous round
	roundAcked     int     // packets acked in current round
	roundThreshold int     // ack threshold before checking round end
}

// NewCongestionControl creates a new rate-based CongestionControl.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:           startupCwnd,
		bwe:            NewBandwidthEstimator(),
		notify:         make(chan struct{}, maxCwnd),
		inStartup:      true,
		roundThreshold: startupCwnd, // first round = initial window
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

	// Check STARTUP exit: has delivery rate plateaued?
	if cc.inStartup {
		cc.checkStartupExit(n)
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

// checkStartupExit checks whether delivery rate has stopped growing,
// signaling that the pipe is full and STARTUP should end.
// Checks once per "round" (after acking cwnd packets), not per ACK.
// Must be called with mu held.
func (cc *CongestionControl) checkStartupExit(acked int) {
	// Don't check exit until BWE has stable estimates. Before stability,
	// the unpaced initial burst rate doesn't reflect pipe capacity — it's
	// limited to cwnd/RTT which looks like a plateau and triggers false exit.
	if !cc.bwe.IsStable() {
		return
	}
	cc.roundAcked += acked
	if cc.roundAcked < cc.roundThreshold {
		return // not enough data for a round yet
	}

	// Round complete — check growth
	cc.roundAcked = 0
	maxBW := cc.bwe.MaxBW()
	if maxBW == 0 {
		return
	}

	if cc.lastMaxBW > 0 && maxBW/cc.lastMaxBW < startupFullBWThresh {
		cc.fullBWCount++
		if cc.fullBWCount >= startupFullBWCount {
			cc.inStartup = false
		}
	} else {
		cc.fullBWCount = 0
	}
	cc.lastMaxBW = maxBW
	// Next round threshold = current cwnd (one full window)
	cc.roundThreshold = cc.cwnd
}

// PacingGain returns the current pacing gain based on CC phase.
func (cc *CongestionControl) PacingGain() float64 {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	if cc.inStartup {
		return startupPacingGain
	}
	return steadyPacingGain
}

// InStartup reports whether the connection is still in STARTUP phase.
func (cc *CongestionControl) InStartup() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inStartup
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
	if target <= 0 {
		return // no estimate yet, keep current cwnd
	}

	if cc.inStartup {
		// STARTUP: only grow — early samples are noisy and we're probing upward
		if target > cc.cwnd {
			cc.cwnd = target
		}
	} else {
		// Steady state: track BDP directly (can shrink on rate decrease)
		cc.cwnd = target
	}

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
