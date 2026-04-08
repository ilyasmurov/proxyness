package sites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// SyncResponse mirrors the server's syncResponse type from
// server/internal/admin/sync.go. Kept minimal — only fields the daemon
// actually uses.
type SyncResponse struct {
	OpResults  []OpResult `json:"op_results"`
	MySites    []MySite   `json:"my_sites"`
	ServerTime int64      `json:"server_time"`
}

type OpResult struct {
	LocalID *int   `json:"local_id,omitempty"`
	SiteID  int    `json:"site_id,omitempty"`
	Status  string `json:"status"`
	Deduped bool   `json:"deduped,omitempty"`
	Message string `json:"message,omitempty"`
}

// MySite mirrors db.UserSite, exposing the fields the daemon needs for
// its in-memory cache. Names match the server's JSON.
type MySite struct {
	ID            int      `json:"id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	PrimaryDomain string   `json:"primary_domain"`
	Domains       []string `json:"domains"`
	IPs           []string `json:"ips"`
	Enabled       bool     `json:"enabled"`
	UpdatedAt     int64    `json:"updated_at"`
}

// SyncClient is the daemon's HTTP client to the server's POST /api/sync.
// It is online-only — it does NOT buffer offline.
type SyncClient struct {
	baseURL string
	mu      sync.RWMutex
	key     string
	http    *http.Client
}

func NewSyncClient(baseURL, key string) *SyncClient {
	return &SyncClient{
		baseURL: baseURL,
		key:     key,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// SetKey updates the cached device key. Called when the desktop client
// connects (and thus persists the key).
func (c *SyncClient) SetKey(key string) {
	c.mu.Lock()
	c.key = key
	c.mu.Unlock()
}

func (c *SyncClient) hasKey() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.key != ""
}

func (c *SyncClient) authHeader() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return "Bearer " + c.key
}

// SyncOps posts the given ops batch to /api/sync and returns the server
// response. last_sync_at is always 0 — the daemon does not track partial
// state, it always asks for the full snapshot.
func (c *SyncClient) SyncOps(ops []map[string]interface{}) (*SyncResponse, error) {
	if !c.hasKey() {
		return nil, fmt.Errorf("no device key set")
	}

	body, _ := json.Marshal(map[string]interface{}{
		"last_sync_at": 0,
		"ops":          ops,
	})

	req, err := http.NewRequest("POST", c.baseURL+"/api/sync", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sync %d: %s", resp.StatusCode, string(buf))
	}

	var sr SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &sr, nil
}

// CatalogSite is a lightweight search result from the global sites catalog.
type CatalogSite struct {
	ID            int    `json:"id"`
	Label         string `json:"label"`
	PrimaryDomain string `json:"primary_domain"`
}

// SearchCatalog queries GET /api/sites/search?q=... on the server.
func (c *SyncClient) SearchCatalog(q string) ([]CatalogSite, error) {
	if !c.hasKey() {
		return nil, fmt.Errorf("no device key set")
	}

	req, err := http.NewRequest("GET", c.baseURL+"/api/sites/search?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", c.authHeader())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		buf, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("search %d: %s", resp.StatusCode, string(buf))
	}

	var sites []CatalogSite
	if err := json.NewDecoder(resp.Body).Decode(&sites); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return sites, nil
}
