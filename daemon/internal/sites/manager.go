package sites

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Manager is the daemon-side composite that wires KeyStore + SyncClient +
// Cache and exposes the operations the HTTP API needs. It is the only
// type the api package interacts with for sites work.
type Manager struct {
	keyStore *KeyStore
	client   *SyncClient
	cache    *Cache

	mu              sync.Mutex
	stopRefresh     chan struct{}
	onCacheReplaced func() // fired after every cache.Replace, nil-safe
}

func NewManager(serverURL string, keyStore *KeyStore) *Manager {
	key := keyStore.Load()
	return &Manager{
		keyStore: keyStore,
		client:   NewSyncClient(serverURL, key),
		cache:    NewCache(),
	}
}

// Cache exposes the read-only cache for /sites/match.
func (m *Manager) Cache() *Cache { return m.cache }

// SetKey is called from the api server when the user (re)connects via
// the desktop client. Updates both the persisted key and the sync client.
func (m *Manager) SetKey(key string) {
	if err := m.keyStore.Save(key); err != nil {
		log.Printf("[sites] persist key: %v", err)
	}
	m.client.SetKey(key)
}

// Refresh fetches the user's full my_sites snapshot and replaces the cache.
// Returns an error if the device key is missing or the server is unreachable.
func (m *Manager) Refresh() error {
	resp, err := m.client.SyncOps(nil)
	if err != nil {
		return err
	}
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return nil
}

// AddSite enqueues an "add" op to the server, refreshes the cache on
// success, and returns the assigned site_id.
func (m *Manager) AddSite(primaryDomain, label string) (int, bool, error) {
	resp, err := m.client.SyncOps([]map[string]interface{}{
		{
			"op":       "add",
			"local_id": -1,
			"site":     map[string]string{"primary_domain": primaryDomain, "label": label},
			"at":       time.Now().Unix(),
		},
	})
	if err != nil {
		return 0, false, err
	}
	if len(resp.OpResults) == 0 {
		return 0, false, fmt.Errorf("no op_results in response")
	}
	r := resp.OpResults[0]
	if r.Status != "ok" {
		return 0, false, fmt.Errorf("server: %s", r.Message)
	}
	m.cache.Replace(resp.MySites)
	m.fireOnCacheReplaced()
	return r.SiteID, r.Deduped, nil
}

// AddDomains enqueues add_domain ops for the given domains.
func (m *Manager) AddDomains(siteID int, domains []string) (int, int, error) {
	now := time.Now().Unix()
	ops := make([]map[string]interface{}, 0, len(domains))
	for _, d := range domains {
		ops = append(ops, map[string]interface{}{
			"op":      "add_domain",
			"site_id": siteID,
			"domain":  d,
			"at":      now,
		})
	}
	resp, err := m.client.SyncOps(ops)
	if err != nil {
		return 0, 0, err
	}
	added, deduped := 0, 0
	for _, r := range resp.OpResults {
		if r.Status == "ok" {
			if r.Deduped {
				deduped++
			} else {
				added++
			}
		}
	}
	if added > 0 {
		m.cache.Replace(resp.MySites)
		m.fireOnCacheReplaced()
	}
	return added, deduped, nil
}

// StartBackgroundRefresh starts a goroutine that calls Refresh every
// `interval`. Stops when StopBackgroundRefresh is called.
func (m *Manager) StartBackgroundRefresh(interval time.Duration) {
	m.mu.Lock()
	if m.stopRefresh != nil {
		m.mu.Unlock()
		return
	}
	m.stopRefresh = make(chan struct{})
	stop := m.stopRefresh
	m.mu.Unlock()

	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if err := m.Refresh(); err != nil {
					log.Printf("[sites] background refresh: %v", err)
				}
			}
		}
	}()
}

func (m *Manager) StopBackgroundRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopRefresh != nil {
		close(m.stopRefresh)
		m.stopRefresh = nil
	}
}

// SetOnCacheReplaced registers a callback that fires after cache.Replace
// (and only after the lock has been released to avoid deadlock).
func (m *Manager) SetOnCacheReplaced(fn func()) {
	m.mu.Lock()
	m.onCacheReplaced = fn
	m.mu.Unlock()
}

func (m *Manager) fireOnCacheReplaced() {
	m.mu.Lock()
	cb := m.onCacheReplaced
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// EnabledDomains returns the flat expanded domain list for all sites
// where Enabled == true. Used to feed pacSites in Server.RebuildPAC.
func (m *Manager) EnabledDomains() []string {
	snapshot := m.cache.Snapshot()
	raw := make([]string, 0, len(snapshot))
	for _, s := range snapshot {
		if !s.Enabled {
			continue
		}
		raw = append(raw, s.Domains...)
	}
	return ExpandDomains(raw)
}
