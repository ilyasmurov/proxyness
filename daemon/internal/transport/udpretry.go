package transport

import (
	"sync"
	"time"
)

// Walking back from the TLS fallback to UDP costs one handshake plus a probe
// when it fails, and drops every live stream when it succeeds — so attempts
// are spaced out: UDPRetryBaseDelay after a clean fallback, doubling up to
// UDPRetryMaxDelay while UDP keeps refusing. An upgrade that survives less
// than UDPUpgradeFlapWindow counts as a failure too, otherwise a UDP path
// that probes fine but carries no bulk data would flap every few minutes.
const (
	UDPRetryBaseDelay    = 5 * time.Minute
	UDPRetryMaxDelay     = 60 * time.Minute
	UDPUpgradeFlapWindow = 2 * time.Minute
)

// UDPRetryPlan schedules TLS→UDP upgrade attempts. The engine and the tunnel
// each own one — same policy, separate transports.
//
// Zero value is valid and unarmed: nothing is attempted until Schedule runs.
type UDPRetryPlan struct {
	mu         sync.Mutex
	at         time.Time
	delay      time.Duration
	upgradedAt time.Time
}

// Armed reports whether an attempt has been scheduled at all. A transport that
// landed on TLS at connect time (UDP blocked from the start) never went
// through a fallback, so the caller arms the plan the first time it notices.
func (p *UDPRetryPlan) Armed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.at.IsZero()
}

// Due reports whether the scheduled attempt has come around.
func (p *UDPRetryPlan) Due(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.at.IsZero() && !now.Before(p.at)
}

// Schedule arms the next attempt and returns the delay used. backoff=false
// means "fresh start" (clean fallback, first time on TLS).
func (p *UDPRetryPlan) Schedule(now time.Time, backoff bool) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delay = nextUDPRetryDelay(p.delay, backoff)
	p.at = now.Add(p.delay)
	return p.delay
}

// MarkUpgraded records a successful upgrade and disarms the plan — we're on
// UDP now, so the next fallback decides when to try again. The delay is kept
// so that a fallback landing inside UDPUpgradeFlapWindow can double it.
func (p *UDPRetryPlan) MarkUpgraded(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.upgradedAt = now
	p.at = time.Time{}
}

// Flapped reports whether the last successful upgrade died too fast to count
// as a working UDP path.
func (p *UDPRetryPlan) Flapped(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.upgradedAt.IsZero() && now.Sub(p.upgradedAt) < UDPUpgradeFlapWindow
}

func nextUDPRetryDelay(prev time.Duration, backoff bool) time.Duration {
	if !backoff || prev <= 0 {
		return UDPRetryBaseDelay
	}
	next := prev * 2
	if next > UDPRetryMaxDelay {
		return UDPRetryMaxDelay
	}
	return next
}
