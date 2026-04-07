package stats

import (
	"sync"
	"sync/atomic"
	"time"

	pkgstats "smurov-proxy/pkg/stats"
)

type RateMeter struct {
	bytesIn    atomic.Int64
	bytesOut   atomic.Int64
	ring       *pkgstats.RingBuffer
	stop       chan struct{}
	lastByteMu sync.Mutex
	lastByteAt time.Time
}

func NewRateMeter() *RateMeter {
	m := &RateMeter{
		ring: pkgstats.NewRingBuffer(),
		stop: make(chan struct{}),
	}
	go m.tick()
	return m
}

func (m *RateMeter) Add(in, out int64) {
	if in == 0 && out == 0 {
		return
	}
	m.bytesIn.Add(in)
	m.bytesOut.Add(out)
	m.lastByteMu.Lock()
	m.lastByteAt = time.Now()
	m.lastByteMu.Unlock()
}

func (m *RateMeter) tick() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			in := m.bytesIn.Swap(0)
			out := m.bytesOut.Swap(0)
			m.ring.Add(pkgstats.RatePoint{
				Timestamp: time.Now().Unix(),
				BytesIn:   in,
				BytesOut:  out,
			})
		case <-m.stop:
			return
		}
	}
}

type Snapshot struct {
	Download int64                `json:"download"`
	Upload   int64                `json:"upload"`
	History  []pkgstats.RatePoint `json:"history"`
}

func (m *RateMeter) Snapshot() Snapshot {
	history := m.ring.Slice()
	var dl, ul int64
	if len(history) > 0 {
		last := history[len(history)-1]
		dl = last.BytesIn
		ul = last.BytesOut
	}
	return Snapshot{
		Download: dl,
		Upload:   ul,
		History:  history,
	}
}

func (m *RateMeter) Stop() {
	close(m.stop)
}

// LastByteAt returns the wall-clock time of the most recent non-zero Add.
// Returns the zero Time if no traffic has flowed yet.
func (m *RateMeter) LastByteAt() time.Time {
	m.lastByteMu.Lock()
	defer m.lastByteMu.Unlock()
	return m.lastByteAt
}

// SeedLastByteAt sets lastByteAt to now. Called from Start() so the
// first stall-detector tick after a fresh connect doesn't trip on a
// zero timestamp.
func (m *RateMeter) SeedLastByteAt() {
	m.lastByteMu.Lock()
	m.lastByteAt = time.Now()
	m.lastByteMu.Unlock()
}

// SeedLastByteAtForTest is a test-only helper that lets unit tests
// force lastByteAt into the past or future. Do not call from production code.
func (m *RateMeter) SeedLastByteAtForTest(at time.Time) {
	m.lastByteMu.Lock()
	m.lastByteAt = at
	m.lastByteMu.Unlock()
}
