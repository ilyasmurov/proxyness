package db

import (
	"sync"
	"time"
)

const deviceCacheTTL = 60 * time.Second

type cachedDevice struct {
	dev Device
	err error
	exp time.Time
}

type deviceCache struct {
	mu sync.RWMutex
	m  map[string]cachedDevice
}

func newDeviceCache() *deviceCache {
	return &deviceCache{m: make(map[string]cachedDevice)}
}

func (c *deviceCache) get(key string) (Device, error, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.exp) {
		return Device{}, nil, false
	}
	return e.dev, e.err, true
}

func (c *deviceCache) put(key string, d Device, err error) {
	c.mu.Lock()
	c.m[key] = cachedDevice{dev: d, err: err, exp: time.Now().Add(deviceCacheTTL)}
	c.mu.Unlock()
}

func (c *deviceCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}
