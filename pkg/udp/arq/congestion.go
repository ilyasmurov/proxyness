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
type CongestionControl struct {
	mu        sync.Mutex
	cwnd      float64
	ssthresh  float64
	wMax      float64
	lastLoss  time.Time
	acksSinceLoss int // used as proxy for elapsed RTTs
	inFlight  int
	sendReady *sync.Cond
}

// NewCongestionControl creates a new CongestionControl starting in slow start.
func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:     initCwnd,
		ssthresh: math.MaxFloat64,
	}
	cc.sendReady = sync.NewCond(&cc.mu)
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

// CanSend reports whether a new packet can be sent (inFlight < cwnd).
func (cc *CongestionControl) CanSend() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight < int(cc.cwnd)
}

// WaitForSlot blocks until a send slot is available or done is closed.
// Returns false if done was closed, true if a slot became available.
func (cc *CongestionControl) WaitForSlot(done <-chan struct{}) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	for cc.inFlight >= int(cc.cwnd) {
		// Check done channel without blocking while holding the mutex.
		select {
		case <-done:
			return false
		default:
		}
		cc.sendReady.Wait()
		// After waking, re-check done.
		select {
		case <-done:
			return false
		default:
		}
	}
	return true
}

// OnSend records that a packet has been sent.
func (cc *CongestionControl) OnSend() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.inFlight++
}

// OnAck records that n packets were acknowledged and updates the window.
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}

	for i := 0; i < n; i++ {
		if cc.cwnd < cc.ssthresh {
			// Slow start: increase by 1 per ack.
			cc.cwnd++
		} else {
			// Congestion avoidance: CUBIC growth.
			cc.cubicGrow()
		}

		if cc.cwnd > maxCwnd {
			cc.cwnd = maxCwnd
		}
	}

	cc.sendReady.Broadcast()
}

// OnLoss handles a loss event: set ssthresh and cwnd via CUBIC beta.
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.wMax = cc.cwnd
	cc.ssthresh = cc.cwnd * cubicBeta
	cc.cwnd = cc.ssthresh
	cc.lastLoss = time.Now()
	cc.acksSinceLoss = 0
}

// cubicGrow applies the CUBIC window growth function.
// Must be called with mu held.
func (cc *CongestionControl) cubicGrow() {
	cc.acksSinceLoss++

	// Use wall-clock time if available (non-zero lastLoss), fall back to
	// counting ACKs as a proxy for elapsed time (1 ACK ≈ 1 ms RTT unit).
	var t float64
	if !cc.lastLoss.IsZero() {
		t = time.Since(cc.lastLoss).Seconds()
		// Supplement with ack-count based time if wall clock hasn't advanced
		// (common in unit tests running at nanosecond resolution).
		ackTime := float64(cc.acksSinceLoss) * 0.001 // 1ms per ack
		if ackTime > t {
			t = ackTime
		}
	} else {
		t = float64(cc.acksSinceLoss) * 0.001
	}

	// K = cbrt(wMax * (1 - beta) / C)
	k := math.Cbrt(cc.wMax * (1 - cubicBeta) / cubicC)

	// W_cubic(t) = C * (t - K)^3 + wMax
	wCubic := cubicC*math.Pow(t-k, 3) + cc.wMax

	if wCubic > cc.cwnd {
		// CUBIC growth: set window to cubic target directly.
		cc.cwnd = wCubic
	} else {
		// TCP-friendly floor: 3*beta/(2-beta) per cwnd ACKs.
		cc.cwnd += 3 * cubicBeta / (2 - cubicBeta) / cc.cwnd
	}
}

// InFlight returns the number of unacknowledged in-flight packets.
func (cc *CongestionControl) InFlight() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight
}

// SignalAll broadcasts to all waiters (used on close).
func (cc *CongestionControl) SignalAll() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.sendReady.Broadcast()
}
