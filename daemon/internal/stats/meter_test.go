package stats

import (
	"testing"
	"time"
)

func TestRateMeterAdd(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	m.Add(1000, 500)
	m.Add(2000, 300)

	// Wait for at least one tick
	time.Sleep(1200 * time.Millisecond)

	snap := m.Snapshot()
	if snap.Download != 3000 {
		t.Fatalf("expected download 3000, got %d", snap.Download)
	}
	if snap.Upload != 800 {
		t.Fatalf("expected upload 800, got %d", snap.Upload)
	}
	if len(snap.History) != 1 {
		t.Fatalf("expected 1 history point, got %d", len(snap.History))
	}
}

func TestRateMeterMultipleTicks(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	m.Add(1000, 100)
	time.Sleep(1200 * time.Millisecond)

	m.Add(2000, 200)
	time.Sleep(1200 * time.Millisecond)

	snap := m.Snapshot()
	if len(snap.History) != 2 {
		t.Fatalf("expected 2 history points, got %d", len(snap.History))
	}
	// Latest tick should have the second batch
	if snap.Download != 2000 {
		t.Fatalf("expected download 2000, got %d", snap.Download)
	}
}

func TestRateMeterSnapshotWhenEmpty(t *testing.T) {
	m := NewRateMeter()
	defer m.Stop()

	snap := m.Snapshot()
	if snap.Download != 0 || snap.Upload != 0 {
		t.Fatalf("expected zeros, got %d/%d", snap.Download, snap.Upload)
	}
	if len(snap.History) != 0 {
		t.Fatalf("expected empty history, got %d", len(snap.History))
	}
}
