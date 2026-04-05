package arq

import (
	"testing"
)

func TestSlowStartGrowth(t *testing.T) {
	cc := NewCongestionControl()

	if cc.Window() != initCwnd {
		t.Fatalf("expected initial cwnd=%d, got %d", initCwnd, cc.Window())
	}

	// Slow start: cwnd++ per ACK
	cc.OnAck(10)
	if cc.Window() != initCwnd+10 {
		t.Fatalf("expected cwnd=%d, got %d", initCwnd+10, cc.Window())
	}
}

func TestMaxWindow(t *testing.T) {
	cc := NewCongestionControl()

	cc.OnAck(maxCwnd * 2)
	if cc.Window() > maxCwnd {
		t.Fatalf("expected cwnd capped at %d, got %d", maxCwnd, cc.Window())
	}
}

func TestOnLossReduces(t *testing.T) {
	cc := NewCongestionControl()

	// Grow to 200
	cc.OnAck(200 - initCwnd)
	before := cc.Window()

	cc.OnLoss()
	expected := int(float64(before) * lossBeta)
	if cc.Window() != expected {
		t.Fatalf("expected cwnd=%d after loss, got %d", expected, cc.Window())
	}
}

func TestOnLossMinCwnd(t *testing.T) {
	cc := NewCongestionControl()

	// Repeated loss should floor at minCwnd
	for i := 0; i < 20; i++ {
		cc.OnLoss()
		cc.mu.Lock()
		cc.lastLoss = cc.lastLoss.Add(-recoveryEpoch) // bypass epoch
		cc.mu.Unlock()
	}

	if cc.Window() < minCwnd {
		t.Fatalf("expected cwnd >= %d, got %d", minCwnd, cc.Window())
	}
}

func TestRecoveryEpoch(t *testing.T) {
	cc := NewCongestionControl()
	cc.OnAck(200 - initCwnd) // grow to 200

	cc.OnLoss()
	after1 := cc.Window()

	// Second loss within epoch should be suppressed
	cc.OnLoss()
	after2 := cc.Window()

	if after1 != after2 {
		t.Fatalf("expected recovery epoch to suppress second loss, cwnd %d → %d", after1, after2)
	}
}

func TestAcquireSlot(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true initially")
	}

	// Fill window
	for i := 1; i < cc.Window(); i++ {
		if !cc.AcquireSlot(done) {
			t.Fatalf("expected AcquireSlot=true at i=%d", i)
		}
	}

	// Window full
	_, inFlight, avail := cc.Stats()
	if avail != 0 {
		t.Fatalf("expected 0 available slots, got %d", avail)
	}
	if inFlight != initCwnd {
		t.Fatalf("expected inFlight=%d, got %d", initCwnd, inFlight)
	}

	// Ack → slot available
	cc.OnAck(1)
	if !cc.AcquireSlot(done) {
		t.Fatal("expected AcquireSlot=true after ack")
	}
}

func TestDoneClosesAcquire(t *testing.T) {
	cc := NewCongestionControl()
	done := make(chan struct{})

	for i := 0; i < cc.Window(); i++ {
		cc.AcquireSlot(done)
	}

	result := make(chan bool, 1)
	go func() {
		result <- cc.AcquireSlot(done)
	}()

	close(done)
	if got := <-result; got {
		t.Fatal("expected AcquireSlot=false after done closed")
	}
}
