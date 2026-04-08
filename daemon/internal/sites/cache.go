package sites

import (
	"strings"
	"sync"
)

// Cache holds the user's full my_sites snapshot in memory. Refreshed on
// daemon start, every 5 minutes, and immediately after a write op.
//
// Match() does a substring + suffix match across all known domains for
// every cached site. The total domain count is small (low hundreds even
// for power users), so a linear scan is fine and avoids needing a trie.
type Cache struct {
	mu    sync.RWMutex
	sites []MySite
}

func NewCache() *Cache {
	return &Cache{}
}

// Replace swaps the entire cache contents.
func (c *Cache) Replace(sites []MySite) {
	c.mu.Lock()
	c.sites = sites
	c.mu.Unlock()
}

// Snapshot returns a shallow copy of the current site list.
func (c *Cache) Snapshot() []MySite {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]MySite, len(c.sites))
	copy(out, c.sites)
	return out
}

// Match returns the first site whose domain list contains the given host
// (or any parent of the host). Returns nil if no match.
func (c *Cache) Match(host string) *MySite {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	for i := range c.sites {
		s := &c.sites[i]
		for _, d := range s.Domains {
			if hostMatches(host, d) {
				return s
			}
		}
	}
	return nil
}

// hostMatches reports whether host equals pattern or is a sub-domain of it.
// "news.habr.com" matches pattern "habr.com"; "habr.com" matches "habr.com";
// "evilhabr.com" does NOT match "habr.com".
func hostMatches(host, pattern string) bool {
	if host == pattern {
		return true
	}
	return strings.HasSuffix(host, "."+pattern)
}
