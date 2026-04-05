package arq

import (
	"testing"
	"time"
)

func TestBandwidthEstimatorBasic(t *testing.T) {
	bwe := NewBandwidthEstimator()

	if bwe.HasEstimate() {
		t.Fatal("should not have estimate initially")
	}

	snap := bwe.TakeSnapshot()
	time.Sleep(10 * time.Millisecond)

	// Simulate delivering 100KB
	bwe.RecordDelivered(100 * 1024)
	bwe.SampleRate(snap, 60*time.Millisecond)

	if !bwe.HasEstimate() {
		t.Fatal("should have estimate after delivery")
	}

	maxBW := bwe.MaxBW()
	if maxBW < 1000000 { // at least 1 MB/s
		t.Fatalf("expected maxBW > 1MB/s, got %.0f bytes/s", maxBW)
	}

	minRTT := bwe.MinRTT()
	if minRTT != 60*time.Millisecond {
		t.Fatalf("expected minRTT=60ms, got %v", minRTT)
	}
}

func TestBandwidthEstimatorMinRTTTracking(t *testing.T) {
	bwe := NewBandwidthEstimator()

	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(1000)
	bwe.SampleRate(snap, 100*time.Millisecond)

	snap = bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(1000)
	bwe.SampleRate(snap, 50*time.Millisecond)

	if bwe.MinRTT() != 50*time.Millisecond {
		t.Fatalf("expected minRTT=50ms, got %v", bwe.MinRTT())
	}

	// Higher RTT should not replace minRTT
	snap = bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(1000)
	bwe.SampleRate(snap, 200*time.Millisecond)

	if bwe.MinRTT() != 50*time.Millisecond {
		t.Fatalf("expected minRTT still 50ms, got %v", bwe.MinRTT())
	}
}

func TestBandwidthEstimatorMaxBWSlidingWindow(t *testing.T) {
	bwe := NewBandwidthEstimator()

	// Fill with high-BW samples
	for i := 0; i < bwWindowSize; i++ {
		snap := bwe.TakeSnapshot()
		time.Sleep(time.Millisecond)
		bwe.RecordDelivered(100000)
		bwe.SampleRate(snap, 60*time.Millisecond)
	}

	highBW := bwe.MaxBW()

	// Fill with low-BW samples to push out the high ones
	for i := 0; i < bwWindowSize+10; i++ {
		snap := bwe.TakeSnapshot()
		time.Sleep(time.Millisecond)
		bwe.RecordDelivered(100)
		bwe.SampleRate(snap, 60*time.Millisecond)
	}

	lowBW := bwe.MaxBW()
	if lowBW >= highBW {
		t.Fatalf("expected maxBW to decrease after low-BW samples, high=%.0f low=%.0f", highBW, lowBW)
	}
}

func TestBandwidthEstimatorCwndFromBDP(t *testing.T) {
	bwe := NewBandwidthEstimator()

	// No estimate yet
	cwnd := bwe.CwndFromBDP(1340, 2.0)
	if cwnd != 0 {
		t.Fatalf("expected cwnd=0 without estimate, got %d", cwnd)
	}

	// Simulate 5 MB/s, 60ms RTT
	snap := bwe.TakeSnapshot()
	time.Sleep(10 * time.Millisecond)
	bwe.RecordDelivered(5 * 1024 * 1024 / 10)
	bwe.SampleRate(snap, 60*time.Millisecond)

	cwnd = bwe.CwndFromBDP(1340, 2.0)
	if cwnd < 100 {
		t.Fatalf("expected cwnd > 100 for 5 MB/s * 60ms, got %d", cwnd)
	}
}

func TestBandwidthEstimatorPacingRate(t *testing.T) {
	bwe := NewBandwidthEstimator()

	if bwe.PacingRate(1.25) != 0 {
		t.Fatal("expected pacing rate 0 without estimate")
	}

	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(100000)
	bwe.SampleRate(snap, 60*time.Millisecond)

	rate := bwe.PacingRate(1.25)
	if rate <= 0 {
		t.Fatal("expected positive pacing rate after delivery")
	}

	maxBW := bwe.MaxBW()
	expected := maxBW * 1.25
	if rate != expected {
		t.Fatalf("expected pacingRate=%.0f (maxBW*1.25), got %.0f", expected, rate)
	}
}

func TestBandwidthEstimatorZeroRTT(t *testing.T) {
	bwe := NewBandwidthEstimator()

	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)

	// Deliver with rtt=0 (retransmitted packet — no RTT sample)
	bwe.RecordDelivered(1000)
	bwe.SampleRate(snap, 0)

	// Should have BW estimate but no RTT
	if bwe.MaxBW() <= 0 {
		t.Fatal("expected positive maxBW even without RTT sample")
	}
	if bwe.MinRTT() != 0 {
		t.Fatalf("expected minRTT=0 with no RTT samples, got %v", bwe.MinRTT())
	}
	if bwe.HasEstimate() {
		t.Fatal("HasEstimate should be false without RTT")
	}
}

func TestBandwidthEstimatorRecordDeliveredAccumulates(t *testing.T) {
	bwe := NewBandwidthEstimator()

	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)

	// Record multiple deliveries before sampling
	bwe.RecordDelivered(10000)
	bwe.RecordDelivered(20000)
	bwe.RecordDelivered(30000)
	bwe.SampleRate(snap, 60*time.Millisecond)

	// Rate should reflect all 60000 bytes, not just the last 30000
	maxBW := bwe.MaxBW()
	// 60000 bytes / ~5ms = ~12 MB/s (at minimum)
	if maxBW < 1000000 {
		t.Fatalf("expected maxBW > 1MB/s from accumulated delivery, got %.0f", maxBW)
	}
}
