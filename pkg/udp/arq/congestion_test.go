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

	// In congestion avoidance, window should grow slowly
	prev := cwndAfterLoss
	for i := 0; i < 50; i++ {
		cc.OnAck(1)
	}

	after := cc.Window()
	if after <= prev {
		t.Fatalf("expected cwnd to grow in congestion avoidance, prev=%d after=%d", prev, after)
	}

	// Growth should be slow: less than 50 (not slow-start-like)
	growth := after - prev
	if growth >= 50 {
		t.Fatalf("expected slow growth in congestion avoidance, got growth=%d", growth)
	}
}

func TestCongestionCanSend(t *testing.T) {
	cc := NewCongestionControl()

	// Initially inFlight=0, cwnd=10 → CanSend=true
	if !cc.CanSend() {
		t.Fatal("expected CanSend=true initially")
	}

	// Fill window
	for i := 0; i < cc.Window(); i++ {
		cc.OnSend()
	}

	if cc.CanSend() {
		t.Fatal("expected CanSend=false when window full")
	}

	// Ack one → CanSend=true
	cc.OnAck(1)
	if !cc.CanSend() {
		t.Fatal("expected CanSend=true after ack")
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

// Ensure OnLoss sets lastLoss time
func TestCongestionLastLoss(t *testing.T) {
	cc := NewCongestionControl()

	before := time.Now()
	cc.OnLoss()
	after := time.Now()

	if cc.lastLoss.Before(before) || cc.lastLoss.After(after) {
		t.Fatalf("lastLoss not set correctly: %v", cc.lastLoss)
	}
}

func TestCongestionInFlight(t *testing.T) {
	cc := NewCongestionControl()

	if cc.InFlight() != 0 {
		t.Fatalf("expected inFlight=0, got %d", cc.InFlight())
	}

	cc.OnSend()
	cc.OnSend()
	if cc.InFlight() != 2 {
		t.Fatalf("expected inFlight=2, got %d", cc.InFlight())
	}

	cc.OnAck(1)
	if cc.InFlight() != 1 {
		t.Fatalf("expected inFlight=1 after ack, got %d", cc.InFlight())
	}
}
