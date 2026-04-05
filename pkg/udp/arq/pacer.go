package arq

import (
	"sync"
	"time"
)

// Pacer spreads packet sends over time to prevent burst losses.
// Pacing interval = SRTT / cwnd, so total throughput ≈ cwnd × MSS / RTT
// but without the burst that causes correlated drops at intermediate buffers.
type Pacer struct {
	mu       sync.Mutex
	lastSend time.Time
}

// NewPacer creates a new Pacer.
func NewPacer() *Pacer {
	return &Pacer{}
}

// Pace sleeps until at least interval has elapsed since the last paced send.
func (p *Pacer) Pace(interval time.Duration) {
	if interval < 100*time.Microsecond {
		return
	}

	p.mu.Lock()
	wait := interval - time.Since(p.lastSend)
	p.mu.Unlock()

	if wait > 0 {
		time.Sleep(wait)
	}

	p.mu.Lock()
	p.lastSend = time.Now()
	p.mu.Unlock()
}
