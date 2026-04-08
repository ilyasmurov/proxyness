package sites

import (
	"testing"
)

func TestCacheMatch(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com", "habrcdn.io"}, Enabled: true},
		{ID: 2, PrimaryDomain: "youtube.com", Domains: []string{"youtube.com", "ytimg.com"}, Enabled: false},
	})

	// Direct match by primary domain.
	if m := cache.Match("habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected match site 1, got %+v", m)
	}

	// Direct match by secondary domain.
	if m := cache.Match("habrcdn.io"); m == nil || m.ID != 1 {
		t.Fatalf("expected match site 1 by habrcdn.io, got %+v", m)
	}

	// Subdomain match (news.habr.com → habr.com).
	if m := cache.Match("news.habr.com"); m == nil || m.ID != 1 {
		t.Fatalf("expected subdomain match, got %+v", m)
	}

	// No match.
	if m := cache.Match("wikipedia.org"); m != nil {
		t.Fatalf("expected no match, got %+v", m)
	}
}

func TestCacheEnabledFlag(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{
		{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true},
		{ID: 2, PrimaryDomain: "vk.com", Domains: []string{"vk.com"}, Enabled: false},
	})

	if m := cache.Match("habr.com"); !m.Enabled {
		t.Fatalf("habr should be enabled")
	}
	if m := cache.Match("vk.com"); m.Enabled {
		t.Fatalf("vk should be disabled")
	}
}

func TestCacheConcurrency(t *testing.T) {
	cache := NewCache()
	cache.Replace([]MySite{{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true}})

	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			cache.Match("habr.com")
		}
		done <- true
	}()
	for i := 0; i < 100; i++ {
		cache.Replace([]MySite{{ID: 1, PrimaryDomain: "habr.com", Domains: []string{"habr.com"}, Enabled: true}})
	}
	<-done
}
