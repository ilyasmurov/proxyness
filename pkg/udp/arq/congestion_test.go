package arq

import (
	"testing"
)

func TestFixedWindow(t *testing.T) {
	cc := NewCongestionControl()

	if cc.Window() != fixedCwnd {
		t.Fatalf("expected cwnd=%d, got %d", fixedCwnd, cc.Window())
	}

	// OnAck doesn't change window
	cc.OnAck(10)
	if cc.Window() != fixedCwnd {
		t.Fatalf("expected cwnd=%d after acks, got %d", fixedCwnd, cc.Window())
	}

	// OnLoss doesn't change window
	cc.OnLoss()
	if cc.Window() != fixedCwnd {
		t.Fatalf("expected cwnd=%d after loss, got %d", fixedCwnd, cc.Window())
	}
}

func TestAcquireSlot(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// AcquireSlot should succeed initially
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true initially")
	}

	// Fill entire window
	for i := 1; i < fixedCwnd; i++ {
		if !cc.AcquireSlot(done) {
			t.Fatalf("expected AcquireSlot=true at i=%d", i)
		}
	}

	// Window full
	_, inFlight, avail := cc.Stats()
	if avail != 0 {
		t.Fatalf("expected 0 available slots, got %d", avail)
	}
	if inFlight != fixedCwnd {
		t.Fatalf("expected inFlight=%d, got %d", fixedCwnd, inFlight)
	}

	// Ack one → slot available
	cc.OnAck(1)
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true after ack")
	}
}

func TestOnDrop(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Acquire 5 slots
	for i := 0; i < 5; i++ {
		cc.AcquireSlot(done)
	}

	if cc.InFlight() != 5 {
		t.Fatalf("expected inFlight=5, got %d", cc.InFlight())
	}

	// Drop 3
	cc.OnDrop(3)
	if cc.InFlight() != 2 {
		t.Fatalf("expected inFlight=2 after drop, got %d", cc.InFlight())
	}

	// Window unchanged
	if cc.Window() != fixedCwnd {
		t.Fatalf("expected cwnd=%d after drop, got %d", fixedCwnd, cc.Window())
	}
}

func TestDoneClosesAcquire(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	// Fill window
	for i := 0; i < fixedCwnd; i++ {
		cc.AcquireSlot(done)
	}

	// AcquireSlot should return false when done is closed
	result := make(chan bool, 1)
	go func() {
		result <- cc.AcquireSlot(done)
	}()

	close(done)
	if got := <-result; got {
		t.Fatal("expected AcquireSlot=false after done closed")
	}
}
