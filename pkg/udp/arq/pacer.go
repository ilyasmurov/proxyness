package arq

import (
	"sync"
	"time"
)

// Pacer spreads packet sends over time to prevent burst losses.
// Uses burst-based pacing to work around OS sleep granularity (~1ms on macOS):
// sends a burst of packets, then sleeps once, instead of sleeping per-packet.
type Pacer struct {
	mu        sync.Mutex
	lastSend  time.Time
	count     int
	burstSize int
}

// NewPacer creates a new Pacer.
func NewPacer() *Pacer {
	return &Pacer{}
}

const (
	minSleep = time.Millisecond
	maxBurst = 8 // cap burst to avoid overwhelming shallow ISP UDP buffers
)

// Pace rate-limits sends. For sub-millisecond intervals, sends a burst of
// packets (burstSize = minSleep / interval) then sleeps once for ~1ms.
func (p *Pacer) Pace(interval time.Duration) {
	if interval < 100*time.Microsecond {
		return
	}

	p.mu.Lock()

	// For sub-ms intervals, batch into bursts so we sleep once per ms.
	// For >= 1ms intervals, send one packet per sleep (normal pacing).
	if interval >= minSleep {
		wait := interval - time.Since(p.lastSend)
		p.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
		p.mu.Lock()
		p.lastSend = time.Now()
		p.mu.Unlock()
		return
	}

	// Round UP to avoid systematic underdelivery from truncation.
	// E.g. interval=335µs: int(1ms/335µs)=2 but we need 3 to hit target rate.
	p.burstSize = int((minSleep + interval - 1) / interval)
	if p.burstSize > maxBurst {
		p.burstSize = maxBurst
	}
	p.count++
	if p.count < p.burstSize {
		p.mu.Unlock()
		return
	}
	p.count = 0

	wait := minSleep - time.Since(p.lastSend)
	p.mu.Unlock()

	if wait > 0 {
		time.Sleep(wait)
	}

	p.mu.Lock()
	p.lastSend = time.Now()
	p.mu.Unlock()
}
