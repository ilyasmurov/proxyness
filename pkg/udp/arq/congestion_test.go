package arq

import (
	"testing"
	"time"
)

func TestCongestionSlowStart(t *testing.T) {
	cc := NewCongestionControl()

	if cc.Window() != initCwnd {
		t.Fatalf("expected initial cwnd=%d, got %d", initCwnd, cc.Window())
	}

	// Each OnAck(1) during slow start increases cwnd by 1
	for i := 0; i < 11; i++ {
		cc.OnAck(1)
	}

	if cc.Window() != initCwnd+11 {
		t.Fatalf("expected cwnd=%d after 11 acks, got %d", initCwnd+11, cc.Window())
	}
}

func TestCongestionOnLoss(t *testing.T) {
	cc := NewCongestionControl()

	// Grow to cwnd=100 via slow start
	for cc.Window() < 100 {
		cc.OnAck(1)
	}

	cwnd := cc.Window()
	cc.OnLoss()

	expected := int(float64(cwnd) * cubicBeta)
	if cc.Window() != expected {
		t.Fatalf("expected cwnd=%d after loss, got %d", expected, cc.Window())
	}
}

func TestCongestionAvoidanceCubic(t *testing.T) {
	cc := NewCongestionControl()

	// Grow to cwnd ~100
	for cc.Window() < 100 {
		cc.OnAck(1)
	}

	cc.OnLoss()
	cwndAfterLoss := cc.Window() // ~70

	// Wait for real wall-clock time so CUBIC has a non-zero t value.
	time.Sleep(50 * time.Millisecond)

	// Send ACKs in congestion avoidance; window should not decrease.
	prev := cwndAfterLoss
	for i := 0; i < 50; i++ {
		cc.OnAck(1)
	}

	after := cc.Window()
	if after < prev {
		t.Fatalf("expected cwnd not to decrease in congestion avoidance, prev=%d after=%d", prev, after)
	}
}

func TestCongestionAcquireSlot(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Initially inFlight=0, cwnd=10 → AcquireSlot should succeed
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
	_, inFlight, avail := cc.Stats()
	if avail != 0 {
		t.Fatalf("expected 0 available slots when window full, got %d", avail)
	}
	if inFlight != initCwnd {
		t.Fatalf("expected inFlight=%d, got %d", initCwnd, inFlight)
	}

	// Ack one → slot available
	cc.OnAck(1)
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true after ack")
	}
}

func TestCongestionMaxWindow(t *testing.T) {
	cc := NewCongestionControl()

	// Send 2000 acks
	for i := 0; i < 2000; i++ {
		cc.OnAck(1)
	}

	if cc.Window() > maxCwnd {
		t.Fatalf("expected cwnd capped at %d, got %d", maxCwnd, cc.Window())
	}

	if cc.Window() != maxCwnd {
		t.Fatalf("expected cwnd=%d after 2000 acks, got %d", maxCwnd, cc.Window())
	}
}

