package arq

import (
	"testing"
	"time"
)

func TestPacerSpreadsSends(t *testing.T) {
	p := NewPacer()
	interval := 5 * time.Millisecond

	start := time.Now()
	for i := 0; i < 5; i++ {
		p.Pace(interval)
	}
	elapsed := time.Since(start)

	// 5 calls × 5ms ≈ 20ms; first may not wait, so expect >= 15ms
	if elapsed < 15*time.Millisecond {
		t.Fatalf("expected >= 15ms for 5 paced sends, got %v", elapsed)
	}
}

func TestPacerZeroIntervalNoDelay(t *testing.T) {
	p := NewPacer()

	start := time.Now()
	for i := 0; i < 100; i++ {
		p.Pace(0)
	}
	elapsed := time.Since(start)

	if elapsed > 5*time.Millisecond {
		t.Fatalf("zero interval should not sleep, got %v", elapsed)
	}
}
