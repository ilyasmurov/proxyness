package arq

import (
	"math"
	"sync"
	"time"
)

const (
	// bwWindowSize is the number of samples kept for maxBW estimation.
	// ~10 RTTs worth at typical ACK rates.
	bwWindowSize = 64

	// minRTTWindow is how long a minRTT sample stays valid before being
	// refreshed. Keeps minRTT from getting stale if path changes.
	minRTTWindow = 10 * time.Second

	// minStableSamples is the number of delivery rate samples needed before the
	// estimate is considered stable enough to drive cwnd down.
	minStableSamples = 8
)

// DeliverySnapshot captures the sender's delivery state at the moment a packet
// is sent. When the ACK for that packet arrives, comparing the snapshot with
// the current state yields the delivery rate over that interval.
type DeliverySnapshot struct {
	Delivered   int64     // cumulative bytes delivered at send time
	DeliveredAt time.Time // when the last delivery happened at send time
	SentAt      time.Time // wall-clock time the packet was sent
}

// bwSample is one delivery rate observation.
type bwSample struct {
	bw float64   // bytes per second
	at time.Time // when this sample was taken
}

// BandwidthEstimator tracks delivery rate and RTT to estimate available
// bandwidth. Inspired by BBR's bandwidth and RTT probing.
//
// Usage pattern in HandleAck:
//  1. RecordDelivered(totalAckedBytes) — count ALL bytes from this ACK
//  2. SampleRate(snap, rtt) — take one rate sample from best snapshot
type BandwidthEstimator struct {
	mu sync.Mutex

	// Delivery tracking — monotonically increasing counter of ALL acked bytes
	delivered   int64     // cumulative bytes acknowledged
	deliveredAt time.Time // wall-clock time of the most recent delivery

	// maxBW: sliding window maximum of delivery rate samples
	samples []bwSample
	maxBW   float64 // cached max from samples (bytes/sec)

	// minRTT: minimum observed RTT with expiry
	minRTT     time.Duration
	minRTTTime time.Time // when minRTT was last updated
}

// NewBandwidthEstimator creates a new estimator.
func NewBandwidthEstimator() *BandwidthEstimator {
	return &BandwidthEstimator{
		deliveredAt: time.Now(),
	}
}

// TakeSnapshot returns the current delivery state for embedding in a sent packet.
func (bwe *BandwidthEstimator) TakeSnapshot() DeliverySnapshot {
	bwe.mu.Lock()
	snap := DeliverySnapshot{
		Delivered:   bwe.delivered,
		DeliveredAt: bwe.deliveredAt,
		SentAt:      time.Now(),
	}
	bwe.mu.Unlock()
	return snap
}

// RecordDelivered adds bytes to the cumulative delivered counter.
// Call this with ALL bytes acknowledged by an ACK (cumulative + selective),
// before calling SampleRate.
func (bwe *BandwidthEstimator) RecordDelivered(bytes int) {
	if bytes <= 0 {
		return
	}
	bwe.mu.Lock()
	bwe.delivered += int64(bytes)
	bwe.deliveredAt = time.Now()
	bwe.mu.Unlock()
}

// SampleRate takes a delivery rate sample using the snapshot from a sent packet
// and the current delivered state. Call after RecordDelivered.
// rtt is the RTT measured for this packet (0 if unavailable, e.g. retransmit).
func (bwe *BandwidthEstimator) SampleRate(snap DeliverySnapshot, rtt time.Duration) {
	now := time.Now()

	bwe.mu.Lock()
	defer bwe.mu.Unlock()

	// Calculate delivery rate:
	// Rate = (bytes delivered since snapshot) / max(send_elapsed, ack_elapsed)
	// Using max() avoids overestimating when ACKs are compressed.
	deliveredInterval := bwe.delivered - snap.Delivered
	if deliveredInterval <= 0 {
		return
	}

	sendElapsed := now.Sub(snap.SentAt)
	ackElapsed := now.Sub(snap.DeliveredAt)

	elapsed := sendElapsed
	if ackElapsed > elapsed {
		elapsed = ackElapsed
	}
	if elapsed <= 0 {
		return
	}

	rate := float64(deliveredInterval) / elapsed.Seconds()

	// Add sample to sliding window
	bwe.samples = append(bwe.samples, bwSample{bw: rate, at: now})
	if len(bwe.samples) > bwWindowSize {
		bwe.samples = bwe.samples[len(bwe.samples)-bwWindowSize:]
	}

	// Recompute maxBW from window
	bwe.maxBW = 0
	for _, s := range bwe.samples {
		if s.bw > bwe.maxBW {
			bwe.maxBW = s.bw
		}
	}

	// Update minRTT
	if rtt > 0 {
		if bwe.minRTT == 0 || rtt < bwe.minRTT || time.Since(bwe.minRTTTime) > minRTTWindow {
			bwe.minRTT = rtt
			bwe.minRTTTime = now
		}
	}
}

// MaxBW returns the estimated maximum bandwidth in bytes/sec.
func (bwe *BandwidthEstimator) MaxBW() float64 {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	return bwe.maxBW
}

// MinRTT returns the minimum observed RTT. Returns 0 if no samples yet.
func (bwe *BandwidthEstimator) MinRTT() time.Duration {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	return bwe.minRTT
}

// BDP returns the estimated bandwidth-delay product in bytes.
func (bwe *BandwidthEstimator) BDP() float64 {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	if bwe.maxBW == 0 || bwe.minRTT == 0 {
		return 0
	}
	return bwe.maxBW * bwe.minRTT.Seconds()
}

// PacingRate returns the recommended pacing rate in bytes/sec.
// Returns 0 if no bandwidth estimate is available yet.
func (bwe *BandwidthEstimator) PacingRate(gain float64) float64 {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	return bwe.maxBW * gain
}

// HasEstimate reports whether at least one delivery rate sample has been collected.
func (bwe *BandwidthEstimator) HasEstimate() bool {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	return bwe.maxBW > 0 && bwe.minRTT > 0
}

// IsStable reports whether enough samples have been collected for the estimate
// to be trusted for cwnd reduction (not just growth).
func (bwe *BandwidthEstimator) IsStable() bool {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	return len(bwe.samples) >= minStableSamples
}

// Stats returns maxBW (bytes/sec), minRTT, and BDP (bytes) for diagnostics.
func (bwe *BandwidthEstimator) Stats() (maxBW float64, minRTT time.Duration, bdp float64) {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()
	maxBW = bwe.maxBW
	minRTT = bwe.minRTT
	if maxBW > 0 && minRTT > 0 {
		bdp = maxBW * minRTT.Seconds()
	}
	return
}

// CwndFromBDP calculates the target congestion window in packets from the
// current bandwidth estimate: cwnd = BDP / packetSize * gain.
func (bwe *BandwidthEstimator) CwndFromBDP(packetSize int, gain float64) int {
	bwe.mu.Lock()
	defer bwe.mu.Unlock()

	if bwe.maxBW == 0 || bwe.minRTT == 0 {
		return 0
	}

	bdp := bwe.maxBW * bwe.minRTT.Seconds()
	cwnd := int(math.Ceil(bdp / float64(packetSize) * gain))
	if cwnd < 1 {
		cwnd = 1
	}
	return cwnd
}
