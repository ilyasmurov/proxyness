package arq

import (
	"testing"
	"time"
)

func TestCongestionStartupCwnd(t *testing.T) {
	cc := NewCongestionControl()

	if cc.Window() != startupCwnd {
		t.Fatalf("expected initial cwnd=%d, got %d", startupCwnd, cc.Window())
	}
}

func TestCongestionAcquireSlot(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Initially inFlight=0, cwnd=startupCwnd → AcquireSlot should succeed
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true initially")
	}

	// Fill window (already acquired 1 above)
	for i := 1; i < cc.Window(); i++ {
		if !cc.AcquireSlot(done) {
			t.Fatalf("expected AcquireSlot=true at i=%d", i)
		}
	}

	// Window full → AcquireSlot should block; verify via InFlight
	_, inFlight, avail, _ := cc.Stats()
	if avail != 0 {
		t.Fatalf("expected 0 available slots when window full, got %d", avail)
	}
	if inFlight != startupCwnd {
		t.Fatalf("expected inFlight=%d, got %d", startupCwnd, inFlight)
	}

	// Ack one → slot available
	cc.OnAck(1)
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true after ack")
	}
}

func TestCongestionDoneClosesSlot(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Fill window
	for i := 0; i < cc.Window(); i++ {
		cc.AcquireSlot(done)
	}

	// Close done → AcquireSlot should return false
	close(done)
	if cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=false after done closed")
	}
}

func TestCongestionMaxWindow(t *testing.T) {
	cc := NewCongestionControl()

	// Simulate high bandwidth estimate so cwnd would exceed maxCwnd
	bwe := cc.BWE()
	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	// Deliver a lot of bytes in short time to get high BW estimate
	bwe.RecordDelivered(10 * 1024 * 1024)
	bwe.SampleRate(snap, 50*time.Millisecond)

	// Trigger cwnd recalc
	cc.OnAck(0)

	if cc.Window() > maxCwnd {
		t.Fatalf("expected cwnd capped at %d, got %d", maxCwnd, cc.Window())
	}
}

func TestCongestionMinWindow(t *testing.T) {
	cc := NewCongestionControl()

	// Simulate very low bandwidth
	bwe := cc.BWE()
	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(1)
	bwe.SampleRate(snap, 50*time.Millisecond)

	cc.OnAck(0)

	if cc.Window() < minCwnd {
		t.Fatalf("expected cwnd >= %d, got %d", minCwnd, cc.Window())
	}
}

func TestCongestionBWDrivesCwnd(t *testing.T) {
	cc := NewCongestionControl()
	bwe := cc.BWE()

	// Simulate steady bandwidth: 5 MB/s, 60ms RTT
	// BDP = 5MB/s * 0.06s = 300KB = ~224 packets at 1340 bytes
	// cwnd = 224 * cwndGain(2.0) = ~448
	snap := bwe.TakeSnapshot()
	time.Sleep(10 * time.Millisecond)
	bwe.RecordDelivered(5 * 1024 * 1024 / 100 * 6)
	bwe.SampleRate(snap, 60*time.Millisecond)

	cc.OnAck(1)

	w := cc.Window()
	if w <= startupCwnd {
		t.Fatalf("expected cwnd to grow from BW estimate, got %d", w)
	}
}

func TestCongestionOnLossIsNoop(t *testing.T) {
	cc := NewCongestionControl()
	bwe := cc.BWE()

	// Build up some bandwidth estimate
	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(500000)
	bwe.SampleRate(snap, 60*time.Millisecond)
	cc.OnAck(1)

	cwndBefore := cc.Window()
	cc.OnLoss()
	cwndAfter := cc.Window()

	if cwndAfter != cwndBefore {
		t.Fatalf("OnLoss should not change cwnd, before=%d after=%d", cwndBefore, cwndAfter)
	}
}

func TestCongestionOnDrop(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Acquire some slots
	for i := 0; i < 5; i++ {
		cc.AcquireSlot(done)
	}

	_, inFlight, _, _ := cc.Stats()
	if inFlight != 5 {
		t.Fatalf("expected inFlight=5, got %d", inFlight)
	}

	cc.OnDrop(3)
	_, inFlight, _, _ = cc.Stats()
	if inFlight != 2 {
		t.Fatalf("expected inFlight=2 after drop, got %d", inFlight)
	}
}

func TestCongestionProbeRTTEntersAndPinsCwnd(t *testing.T) {
	cc := NewCongestionControl()
	bwe := cc.BWE()

	// Seed a big BW estimate so BDP-based cwnd would naturally be large,
	// then exit STARTUP (ProbeRTT only fires in STEADY).
	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(5 * 1024 * 1024)
	bwe.SampleRate(snap, 60*time.Millisecond)
	cc.mu.Lock()
	cc.inStartup = false
	cc.mu.Unlock()
	cc.OnAck(1)

	preCwnd := cc.Window()
	if preCwnd <= probeRTTCwnd {
		t.Fatalf("pre-probe cwnd must exceed probeRTTCwnd to test pin, got %d", preCwnd)
	}

	// Force the interval to be due.
	cc.mu.Lock()
	cc.probeRTTNext = time.Now().Add(-1 * time.Millisecond)
	cc.mu.Unlock()
	cc.OnAck(1)

	if !cc.InProbeRTT() {
		t.Fatal("expected InProbeRTT=true after interval elapsed")
	}
	if cc.Window() != probeRTTCwnd {
		t.Fatalf("expected cwnd=%d during ProbeRTT, got %d", probeRTTCwnd, cc.Window())
	}
}

func TestCongestionProbeRTTExitsAfterDuration(t *testing.T) {
	cc := NewCongestionControl()
	bwe := cc.BWE()

	// Seed BW / STEADY / in ProbeRTT
	snap := bwe.TakeSnapshot()
	time.Sleep(5 * time.Millisecond)
	bwe.RecordDelivered(5 * 1024 * 1024)
	bwe.SampleRate(snap, 60*time.Millisecond)
	cc.mu.Lock()
	cc.inStartup = false
	cc.inProbeRTT = true
	cc.probeRTTStart = time.Now().Add(-probeRTTDuration - 10*time.Millisecond)
	cc.mu.Unlock()

	cc.OnAck(1)

	if cc.InProbeRTT() {
		t.Fatal("expected ProbeRTT to exit after duration elapsed")
	}
	if cc.Window() <= probeRTTCwnd {
		t.Fatalf("expected cwnd to grow back after ProbeRTT exit, got %d", cc.Window())
	}
}

func TestCongestionProbeRTTSkippedDuringStartup(t *testing.T) {
	cc := NewCongestionControl()

	// Force probeRTTNext to be due while still in STARTUP.
	cc.mu.Lock()
	cc.probeRTTNext = time.Now().Add(-1 * time.Millisecond)
	cc.mu.Unlock()

	cc.OnAck(1)

	if cc.InProbeRTT() {
		t.Fatal("ProbeRTT should not fire during STARTUP — interrupting probe phase wastes a round")
	}
}
