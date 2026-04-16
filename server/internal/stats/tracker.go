package stats

import (
	"sync"
	"sync/atomic"
	"time"

	pkgstats "proxyness/pkg/stats"
)

type ConnInfo struct {
	DeviceID   int       `json:"device_id"`
	DeviceName string    `json:"device_name"`
	UserName   string    `json:"user_name"`
	Version    string    `json:"version,omitempty"`
	TLS        bool      `json:"tls"`
	StartedAt  time.Time `json:"started_at"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

type deviceMeta struct {
	name     string
	userName string
	version  string
	lastSeen int64 // unix timestamp of last connection activity
}

type Tracker struct {
	mu    sync.RWMutex
	conns map[int64]*ConnInfo

	nextID int64

	bufMu         sync.RWMutex
	deviceBuffers map[int]*pkgstats.RingBuffer
	deviceMetas   map[int]*deviceMeta
	stop          chan struct{}
}

// devicePresenceTimeout is how long a device stays visible after its last
// connection closes. With TLS transport every HTTP request is a separate
// short-lived TCP connection, so there are natural gaps between requests.
const devicePresenceTimeout = 60 // seconds

func New() *Tracker {
	t := &Tracker{
		conns:         make(map[int64]*ConnInfo),
		deviceBuffers: make(map[int]*pkgstats.RingBuffer),
		deviceMetas:   make(map[int]*deviceMeta),
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
	// Cleanup stale devices (no activity for 1 hour)
	const staleTimeout int64 = 3600
	for id, m := range t.deviceMetas {
		if now-m.lastSeen > staleTimeout {
			delete(t.deviceMetas, id)
			delete(t.deviceBuffers, id)
		}
	}
	t.bufMu.Unlock()
}

const rateSmoothWindow = 5

func smoothRate(history []pkgstats.RatePoint, window int) (down, up int64) {
	n := len(history)
	if n == 0 || window <= 0 {
		return 0, 0
	}
	start := n - window
	if start < 0 {
		start = 0
	}
	var sumIn, sumOut int64
	for i := start; i < n; i++ {
		sumIn += history[i].BytesIn
		sumOut += history[i].BytesOut
	}
	samples := int64(n - start)
	return sumIn / samples, sumOut / samples
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
	TLSConns    int                  `json:"tls_conns"`
	RawConns    int                  `json:"raw_conns"`
	History     []pkgstats.RatePoint `json:"history"`
}

func (t *Tracker) Rates() []DeviceRate {
	type devInfo struct {
		name       string
		userName   string
		version    string
		totalBytes int64
		connCount  int
		tlsConns   int
		rawConns   int
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
		if c.TLS {
			d.tlsConns++
		} else {
			d.rawConns++
		}
	}
	t.mu.RUnlock()

	t.bufMu.RLock()
	defer t.bufMu.RUnlock()

	// Include devices that have no active connections right now but were
	// seen recently (within devicePresenceTimeout). This keeps TLS-only
	// devices visible — their connections are short-lived HTTP requests
	// that flash in and out of t.conns in milliseconds.
	now := time.Now().Unix()
	for id, m := range t.deviceMetas {
		if devices[id] != nil {
			continue // already tracked via active connection
		}
		if now-m.lastSeen > devicePresenceTimeout {
			continue // stale
		}
		devices[id] = &devInfo{name: m.name, userName: m.userName, version: m.version}
	}

	result := make([]DeviceRate, 0, len(devices))
	for id, info := range devices {
		dr := DeviceRate{
			DeviceID:    id,
			DeviceName:  info.name,
			UserName:    info.userName,
			Version:     info.version,
			TotalBytes:  info.totalBytes,
			Connections: info.connCount,
			TLSConns:    info.tlsConns,
			RawConns:    info.rawConns,
			History:     []pkgstats.RatePoint{},
		}
		if buf := t.deviceBuffers[id]; buf != nil {
			dr.History = buf.Slice()
			// Smooth the "current rate" across the last few seconds.
			// Single-second samples legitimately hit zero between packet
			// bursts (HTTP keep-alive gaps, video chunk pauses), so the
			// display was flickering to 0 ↓ during active use. History on
			// the graph is unchanged — only the headline number is smoothed.
			dr.Download, dr.Upload = smoothRate(dr.History, rateSmoothWindow)
		}
		result = append(result, dr)
	}
	return result
}

func (t *Tracker) Add(deviceID int, deviceName, userName, version string, isTLS bool) int64 {
	id := atomic.AddInt64(&t.nextID, 1)
	now := time.Now()
	t.mu.Lock()
	t.conns[id] = &ConnInfo{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		UserName:   userName,
		Version:    version,
		TLS:        isTLS,
		StartedAt:  now,
	}
	t.mu.Unlock()

	// Update device presence metadata
	t.bufMu.Lock()
	m := t.deviceMetas[deviceID]
	if m == nil {
		m = &deviceMeta{}
		t.deviceMetas[deviceID] = m
	}
	m.name = deviceName
	m.userName = userName
	m.version = version
	m.lastSeen = now.Unix()
	t.bufMu.Unlock()
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
	t.mu.Unlock()

	// Update lastSeen so the device stays visible in Rates() after
	// its last connection closes (important for TLS transport where
	// every HTTP request is a separate short-lived TCP connection).
	t.bufMu.Lock()
	if m := t.deviceMetas[c.DeviceID]; m != nil {
		m.lastSeen = time.Now().Unix()
	}
	t.bufMu.Unlock()

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
			UserName: c.UserName, Version: c.Version, TLS: c.TLS, StartedAt: c.StartedAt,
			BytesIn: atomic.LoadInt64(&c.BytesIn), BytesOut: atomic.LoadInt64(&c.BytesOut),
		})
	}
	return result
}

func (t *Tracker) ActiveCount() int {
	t.mu.RLock()
	n := len(t.conns)
	t.mu.RUnlock()
	if n > 0 {
		return n
	}
	// Count recently-seen devices (TLS presence)
	now := time.Now().Unix()
	t.bufMu.RLock()
	defer t.bufMu.RUnlock()
	for _, m := range t.deviceMetas {
		if now-m.lastSeen <= devicePresenceTimeout {
			n++
		}
	}
	return n
}
