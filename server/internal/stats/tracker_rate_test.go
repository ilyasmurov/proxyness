package stats

import (
	"testing"
	"time"
)

func TestTrackerRates(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id1 := tr.Add(1, "MacBook", "ilya", "")
	id2 := tr.Add(1, "MacBook", "ilya", "")

	tr.AddBytes(id1, 1000, 100)
	tr.AddBytes(id2, 2000, 200)

	time.Sleep(1200 * time.Millisecond)

	rates := tr.Rates()
	if len(rates) != 1 {
		t.Fatalf("expected 1 device rate, got %d", len(rates))
	}
	r := rates[0]
	if r.DeviceID != 1 {
		t.Fatalf("expected device_id 1, got %d", r.DeviceID)
	}
	if r.Download != 3000 {
		t.Fatalf("expected download 3000, got %d", r.Download)
	}
	if r.Upload != 300 {
		t.Fatalf("expected upload 300, got %d", r.Upload)
	}
	if r.Connections != 2 {
		t.Fatalf("expected 2 connections, got %d", r.Connections)
	}
}

func TestTrackerRatesAfterRemove(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id := tr.Add(1, "MacBook", "ilya", "")
	tr.AddBytes(id, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	tr.Remove(id)

	rates := tr.Rates()
	if len(rates) != 0 {
		t.Fatalf("expected 0 device rates after remove, got %d", len(rates))
	}
}

func TestTrackerRatesMultipleDevices(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id1 := tr.Add(1, "MacBook", "ilya", "")
	id2 := tr.Add(2, "iPhone", "ilya", "")

	tr.AddBytes(id1, 5000, 500)
	tr.AddBytes(id2, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	rates := tr.Rates()
	if len(rates) != 2 {
		t.Fatalf("expected 2 device rates, got %d", len(rates))
	}
}
