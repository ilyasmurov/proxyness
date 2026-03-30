package stats

import (
	"sync"
	"sync/atomic"
	"time"

	pkgstats "smurov-proxy/pkg/stats"
)

type ConnInfo struct {
	DeviceID   int       `json:"device_id"`
	DeviceName string    `json:"device_name"`
	UserName   string    `json:"user_name"`
	Version    string    `json:"version,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

type Tracker struct {
	mu     sync.RWMutex
	conns  map[int64]*ConnInfo
	nextID int64

	bufMu         sync.RWMutex
	deviceBuffers map[int]*pkgstats.RingBuffer
	stop          chan struct{}
}

func New() *Tracker {
	t := &Tracker{
		conns:         make(map[int64]*ConnInfo),
		deviceBuffers: make(map[int]*pkgstats.RingBuffer),
		stop:          make(chan struct{}),
	}
	go t.rateTicker()
	return t
}

func (t *Tracker) Stop() {
	close(t.stop)
}

func (t *Tracker) rateTicker() {
	prev := make(map[int64][2]int64) // connID -> [prevBytesIn, prevBytesOut]
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.computeRates(prev)
		case <-t.stop:
			return
		}
	}
}

func (t *Tracker) computeRates(prev map[int64][2]int64) {
	type delta struct{ in, out int64 }
	deltas := make(map[int]delta)
	seen := make(map[int64]bool)

	t.mu.RLock()
	for id, c := range t.conns {
		seen[id] = true
		curIn := atomic.LoadInt64(&c.BytesIn)
		curOut := atomic.LoadInt64(&c.BytesOut)
		p := prev[id]
		d := deltas[c.DeviceID]
		d.in += curIn - p[0]
		d.out += curOut - p[1]
		deltas[c.DeviceID] = d
		prev[id] = [2]int64{curIn, curOut}
	}
	t.mu.RUnlock()

	for id := range prev {
		if !seen[id] {
			delete(prev, id)
		}
	}

	now := time.Now().Unix()
	t.bufMu.Lock()
	for deviceID, d := range deltas {
		buf := t.deviceBuffers[deviceID]
		if buf == nil {
			buf = pkgstats.NewRingBuffer()
			t.deviceBuffers[deviceID] = buf
		}
		buf.Add(pkgstats.RatePoint{Timestamp: now, BytesIn: d.in, BytesOut: d.out})
	}
	t.bufMu.Unlock()
}

type DeviceRate struct {
	DeviceID    int                  `json:"device_id"`
	DeviceName  string               `json:"device_name"`
	UserName    string               `json:"user_name"`
	Version     string               `json:"version,omitempty"`
	Download    int64                `json:"download"`
	Upload      int64                `json:"upload"`
	TotalBytes  int64                `json:"total_bytes"`
	Connections int                  `json:"connections"`
	History     []pkgstats.RatePoint `json:"history"`
}

func (t *Tracker) Rates() []DeviceRate {
	type devInfo struct {
		name       string
		userName   string
		version    string
		totalBytes int64
		connCount  int
	}
	devices := make(map[int]*devInfo)

	t.mu.RLock()
	for _, c := range t.conns {
		d := devices[c.DeviceID]
		if d == nil {
			d = &devInfo{name: c.DeviceName, userName: c.UserName, version: c.Version}
			devices[c.DeviceID] = d
		}
		d.totalBytes += atomic.LoadInt64(&c.BytesIn) + atomic.LoadInt64(&c.BytesOut)
		d.connCount++
	}
	t.mu.RUnlock()

	t.bufMu.RLock()
	defer t.bufMu.RUnlock()

	result := make([]DeviceRate, 0, len(devices))
	for id, info := range devices {
		dr := DeviceRate{
			DeviceID:    id,
			DeviceName:  info.name,
			UserName:    info.userName,
			Version:     info.version,
			TotalBytes:  info.totalBytes,
			Connections: info.connCount,
			History:     []pkgstats.RatePoint{},
		}
		if buf := t.deviceBuffers[id]; buf != nil {
			dr.History = buf.Slice()
			if len(dr.History) > 0 {
				last := dr.History[len(dr.History)-1]
				dr.Download = last.BytesIn
				dr.Upload = last.BytesOut
			}
		}
		result = append(result, dr)
	}
	return result
}

func (t *Tracker) Add(deviceID int, deviceName, userName, version string) int64 {
	id := atomic.AddInt64(&t.nextID, 1)
	t.mu.Lock()
	t.conns[id] = &ConnInfo{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		UserName:   userName,
		Version:    version,
		StartedAt:  time.Now(),
	}
	t.mu.Unlock()
	return id
}

func (t *Tracker) AddBytes(id, bytesIn, bytesOut int64) {
	t.mu.RLock()
	c, ok := t.conns[id]
	t.mu.RUnlock()
	if ok {
		atomic.AddInt64(&c.BytesIn, bytesIn)
		atomic.AddInt64(&c.BytesOut, bytesOut)
	}
}

func (t *Tracker) Remove(id int64) *ConnInfo {
	t.mu.Lock()
	c, ok := t.conns[id]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	delete(t.conns, id)

	// Check if any connections remain for this device
	deviceID := c.DeviceID
	hasMore := false
	for _, other := range t.conns {
		if other.DeviceID == deviceID {
			hasMore = true
			break
		}
	}
	t.mu.Unlock()

	if !hasMore {
		t.bufMu.Lock()
		delete(t.deviceBuffers, deviceID)
		t.bufMu.Unlock()
	}

	return &ConnInfo{
		DeviceID:   c.DeviceID,
		DeviceName: c.DeviceName,
		UserName:   c.UserName,
		Version:    c.Version,
		StartedAt:  c.StartedAt,
		BytesIn:    atomic.LoadInt64(&c.BytesIn),
		BytesOut:   atomic.LoadInt64(&c.BytesOut),
	}
}

func (t *Tracker) Active() []ConnInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]ConnInfo, 0, len(t.conns))
	for _, c := range t.conns {
		result = append(result, ConnInfo{
			DeviceID: c.DeviceID, DeviceName: c.DeviceName,
			UserName: c.UserName, Version: c.Version, StartedAt: c.StartedAt,
			BytesIn: atomic.LoadInt64(&c.BytesIn), BytesOut: atomic.LoadInt64(&c.BytesOut),
		})
	}
	return result
}

func (t *Tracker) ActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.conns)
}
