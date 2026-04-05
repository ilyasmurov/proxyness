package arq

import (
	"sync"
	"time"
)

const (
	minRTO = 100 * time.Millisecond
	maxRTO = 500 * time.Millisecond
)

// RTTEstimator implements Jacobson/Karels RTT estimation algorithm.
type RTTEstimator struct {
	mu     sync.Mutex
	srtt   time.Duration
	rttvar time.Duration
	rto    time.Duration
	init   bool
}

// NewRTTEstimator creates a new RTTEstimator with initial RTO of 300ms.
// Lower initial RTO allows faster loss recovery before the first RTT sample.
func NewRTTEstimator() *RTTEstimator {
	return &RTTEstimator{
		rto: 300 * time.Millisecond,
	}
}

// Update incorporates a new RTT sample using Jacobson/Karels algorithm.
func (r *RTTEstimator) Update(sample time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.init {
		r.srtt = sample
		r.rttvar = sample / 2
		r.init = true
	} else {
		// rttvar = 3/4 * rttvar + 1/4 * |srtt - sample|
		diff := r.srtt - sample
		if diff < 0 {
			diff = -diff
		}
		r.rttvar = r.rttvar*3/4 + diff/4
		// srtt = 7/8 * srtt + 1/8 * sample
		r.srtt = r.srtt*7/8 + sample/8
	}

	r.rto = r.srtt + 4*r.rttvar
	r.rto = r.clampRTO(r.rto)
}

// Backoff doubles the RTO (on retransmission timeout), clamped to maxRTO.
func (r *RTTEstimator) Backoff() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.rto = r.clampRTO(r.rto * 2)
}

// clampRTO enforces min/max bounds on rto. Must be called with mu held.
func (r *RTTEstimator) clampRTO(d time.Duration) time.Duration {
	if d < minRTO {
		return minRTO
	}
	if d > maxRTO {
		return maxRTO
	}
	return d
}

// RTO returns the current retransmission timeout.
func (r *RTTEstimator) RTO() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rto
}

// SRTT returns the current smoothed RTT.
func (r *RTTEstimator) SRTT() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.srtt
}
