package stats

import (
	"testing"
	"time"
)

func TestTrackerRates(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id1 := tr.Add(1, "MacBook", "ilya", "", false)
	id2 := tr.Add(1, "MacBook", "ilya", "", false)

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

// newManual returns a Tracker whose background ticker is already stopped, so a
// test can drive computeRates() by hand instead of sleeping on the 1s tick.
func newManual() *Tracker {
	tr := New()
	tr.Stop()
	return tr
}

// backdateLastSeen rewinds a device's presence timestamp to simulate the
// passage of time without making the test sleep for it.
func backdateLastSeen(tr *Tracker, deviceID int, secondsAgo int64) {
	tr.bufMu.Lock()
	if m := tr.deviceMetas[deviceID]; m != nil {
		m.lastSeen = time.Now().Unix() - secondsAgo
	}
	tr.bufMu.Unlock()
}

// Remove() deliberately does NOT evict the device from Rates(): with the TLS
// transport every HTTP request is its own short-lived connection, so a device
// stays visible for devicePresenceTimeout after its last connection closes.
func TestTrackerRatesAfterRemoveStaysInPresenceWindow(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id := tr.Add(1, "MacBook", "ilya", "", false)
	tr.AddBytes(id, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	tr.Remove(id)

	rates := tr.Rates()
	if len(rates) != 1 {
		t.Fatalf("expected device to stay visible inside the presence window, got %d rates", len(rates))
	}
	if rates[0].Connections != 0 {
		t.Fatalf("expected 0 live connections, got %d", rates[0].Connections)
	}
}

// ...and it does disappear once the presence window expires.
func TestTrackerRatesAfterPresenceWindowExpires(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id := tr.Add(1, "MacBook", "ilya", "", false)
	tr.AddBytes(id, 1000, 100)
	tr.Remove(id)

	backdateLastSeen(tr, 1, devicePresenceTimeout+1)

	if rates := tr.Rates(); len(rates) != 0 {
		t.Fatalf("expected 0 device rates after the presence window expired, got %d", len(rates))
	}
	if n := tr.ActiveCount(); n != 0 {
		t.Fatalf("expected ActiveCount 0 after the presence window expired, got %d", n)
	}
}

// A device idling inside the presence window must report a rate of 0, not the
// last rate it happened to have when its final connection closed.
func TestTrackerRateDecaysToZeroWhileIdle(t *testing.T) {
	tr := newManual()
	prev := make(map[int64][2]int64)

	id := tr.Add(1, "MacBook", "ilya", "", false)
	tr.AddBytes(id, 1000, 100)
	tr.computeRates(prev) // one second of real traffic

	tr.Remove(id)
	for i := 0; i < rateSmoothWindow; i++ {
		tr.computeRates(prev)
	}

	rates := tr.Rates()
	if len(rates) != 1 {
		t.Fatalf("device should still be inside the presence window, got %d rates", len(rates))
	}
	if rates[0].Download != 0 || rates[0].Upload != 0 {
		t.Fatalf("expected idle device rate to decay to 0, got down=%d up=%d",
			rates[0].Download, rates[0].Upload)
	}
}

// A UDP session holds a single tracker connection for its whole lifetime, so
// lastSeen would only ever be written at Add() time. The stale cleanup must not
// evict such a device — and wipe its rate history — while it is still connected.
func TestTrackerKeepsLiveDeviceAcrossStaleCleanup(t *testing.T) {
	tr := newManual()
	prev := make(map[int64][2]int64)

	id := tr.Add(1, "MacBook", "ilya", "", false)
	tr.AddBytes(id, 1000, 100)
	tr.computeRates(prev)

	// Simulate an hour of uptime on the still-open connection.
	backdateLastSeen(tr, 1, staleDeviceTimeout+1)
	tr.AddBytes(id, 2000, 200)
	tr.computeRates(prev)

	rates := tr.Rates()
	if len(rates) != 1 {
		t.Fatalf("expected the live device to survive stale cleanup, got %d rates", len(rates))
	}
	if len(rates[0].History) != 2 {
		t.Fatalf("expected rate history to survive stale cleanup, got %d points", len(rates[0].History))
	}
}

// A device that really has been gone for an hour is dropped entirely.
func TestTrackerDropsStaleDevice(t *testing.T) {
	tr := newManual()
	prev := make(map[int64][2]int64)

	id := tr.Add(1, "MacBook", "ilya", "", false)
	tr.AddBytes(id, 1000, 100)
	tr.computeRates(prev)
	tr.Remove(id)

	backdateLastSeen(tr, 1, staleDeviceTimeout+1)
	tr.computeRates(prev)

	tr.bufMu.RLock()
	metas, bufs := len(tr.deviceMetas), len(tr.deviceBuffers)
	tr.bufMu.RUnlock()
	if metas != 0 || bufs != 0 {
		t.Fatalf("expected stale device to be dropped, got %d metas / %d buffers", metas, bufs)
	}
}

func TestTrackerRatesMultipleDevices(t *testing.T) {
	tr := New()
	defer tr.Stop()

	id1 := tr.Add(1, "MacBook", "ilya", "", false)
	id2 := tr.Add(2, "iPhone", "ilya", "", false)

	tr.AddBytes(id1, 5000, 500)
	tr.AddBytes(id2, 1000, 100)

	time.Sleep(1200 * time.Millisecond)

	rates := tr.Rates()
	if len(rates) != 2 {
		t.Fatalf("expected 2 device rates, got %d", len(rates))
	}
}
