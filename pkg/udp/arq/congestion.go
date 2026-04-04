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
	if cc.ssthresh < initCwnd {
		cc.ssthresh = initCwnd
	}
	cc.cwnd = cc.ssthresh
	cc.lastLoss = time.Now()
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

// SignalAll broadcasts to all waiters (used on close).
func (cc *CongestionControl) SignalAll() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.sendReady.Broadcast()
}
