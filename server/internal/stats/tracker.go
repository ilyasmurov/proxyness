package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

type ConnInfo struct {
	DeviceID   int       `json:"device_id"`
	DeviceName string    `json:"device_name"`
	UserName   string    `json:"user_name"`
	StartedAt  time.Time `json:"started_at"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

type Tracker struct {
	mu     sync.RWMutex
	conns  map[int64]*ConnInfo
	nextID int64
}

func New() *Tracker {
	return &Tracker{conns: make(map[int64]*ConnInfo)}
}

func (t *Tracker) Add(deviceID int, deviceName, userName string) int64 {
	id := atomic.AddInt64(&t.nextID, 1)
	t.mu.Lock()
	t.conns[id] = &ConnInfo{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		UserName:   userName,
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
	if ok {
		delete(t.conns, id)
	}
	t.mu.Unlock()
	if !ok {
		return nil
	}
	return &ConnInfo{
		DeviceID: c.DeviceID, DeviceName: c.DeviceName,
		UserName: c.UserName, StartedAt: c.StartedAt,
		BytesIn: atomic.LoadInt64(&c.BytesIn), BytesOut: atomic.LoadInt64(&c.BytesOut),
	}
}

func (t *Tracker) Active() []ConnInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]ConnInfo, 0, len(t.conns))
	for _, c := range t.conns {
		result = append(result, ConnInfo{
			DeviceID: c.DeviceID, DeviceName: c.DeviceName,
			UserName: c.UserName, StartedAt: c.StartedAt,
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
