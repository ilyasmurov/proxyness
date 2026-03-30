package stats

import "sync"

// RatePoint is a single rate measurement (bytes per second).
type RatePoint struct {
	Timestamp int64 `json:"t"`
	BytesIn   int64 `json:"down"`
	BytesOut  int64 `json:"up"`
}

const RingSize = 300

// RingBuffer is a fixed-size circular buffer of RatePoints.
type RingBuffer struct {
	mu    sync.RWMutex
	buf   [RingSize]RatePoint
	write int
	count int
}

func NewRingBuffer() *RingBuffer {
	return &RingBuffer{}
}

func (r *RingBuffer) Add(p RatePoint) {
	r.mu.Lock()
	r.buf[r.write] = p
	r.write = (r.write + 1) % RingSize
	if r.count < RingSize {
		r.count++
	}
	r.mu.Unlock()
}

// Slice returns a copy of all points, oldest first.
func (r *RingBuffer) Slice() []RatePoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil
	}
	result := make([]RatePoint, r.count)
	if r.count < RingSize {
		copy(result, r.buf[:r.count])
	} else {
		n := copy(result, r.buf[r.write:])
		copy(result[n:], r.buf[:r.write])
	}
	return result
}
