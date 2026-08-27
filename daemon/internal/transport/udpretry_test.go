package transport

import (
	"testing"
	"time"
)

func TestNextUDPRetryDelay(t *testing.T) {
	tests := []struct {
		name    string
		prev    time.Duration
		backoff bool
		want    time.Duration
	}{
		{"first attempt starts at base", 0, false, UDPRetryBaseDelay},
		{"clean fallback resets to base", 40 * time.Minute, false, UDPRetryBaseDelay},
		{"backoff doubles", UDPRetryBaseDelay, true, 2 * UDPRetryBaseDelay},
		{"backoff caps at max", UDPRetryMaxDelay, true, UDPRetryMaxDelay},
		{"backoff from zero is base", 0, true, UDPRetryBaseDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextUDPRetryDelay(tt.prev, tt.backoff); got != tt.want {
				t.Fatalf("nextUDPRetryDelay(%v, %v) = %v, want %v", tt.prev, tt.backoff, got, tt.want)
			}
		})
	}
}

func TestUDPRetryPlanLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	var p UDPRetryPlan

	if p.Armed() {
		t.Fatal("zero plan reports armed")
	}
	if p.Due(now) {
		t.Fatal("unarmed plan is due")
	}

	if got := p.Schedule(now, false); got != UDPRetryBaseDelay {
		t.Fatalf("Schedule(clean) = %v, want %v", got, UDPRetryBaseDelay)
	}
	if !p.Armed() {
		t.Fatal("plan not armed after Schedule")
	}
	if p.Due(now.Add(UDPRetryBaseDelay - time.Second)) {
		t.Fatal("plan due before its delay elapsed")
	}
	if !p.Due(now.Add(UDPRetryBaseDelay)) {
		t.Fatal("plan not due once the delay elapsed")
	}

	// A failed attempt backs off.
	if got := p.Schedule(now, true); got != 2*UDPRetryBaseDelay {
		t.Fatalf("Schedule(backoff) = %v, want %v", got, 2*UDPRetryBaseDelay)
	}

	// A successful upgrade disarms the plan.
	upgraded := now.Add(time.Hour)
	p.MarkUpgraded(upgraded)
	if p.Armed() {
		t.Fatal("plan still armed after MarkUpgraded")
	}
	if !p.Flapped(upgraded.Add(UDPUpgradeFlapWindow - time.Second)) {
		t.Fatal("Flapped() = false inside the flap window")
	}
	if p.Flapped(upgraded.Add(UDPUpgradeFlapWindow)) {
		t.Fatal("Flapped() = true outside the flap window")
	}
}
