package db

import (
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) *DB {
	t.Helper()
	dir := t.TempDir()
	d, err := Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		d.Close()
	})
	return d
}

func TestSeedSitesInsertsOnEmpty(t *testing.T) {
	d := tempDB(t)

	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		t.Fatalf("count sites: %v", err)
	}
	if count != len(seedSites) {
		t.Fatalf("expected %d seed sites, got %d", len(seedSites), count)
	}

	// Spot-check first seed row has id=1
	var id int
	var slug string
	if err := d.sql.QueryRow(`SELECT id, slug FROM sites ORDER BY id LIMIT 1`).Scan(&id, &slug); err != nil {
		t.Fatalf("first row: %v", err)
	}
	if id != 1 {
		t.Fatalf("first seed id = %d, want 1", id)
	}
	if slug != seedSites[0].Slug {
		t.Fatalf("first seed slug = %q, want %q", slug, seedSites[0].Slug)
	}

	// Spot-check primary domain has is_primary=1
	var isPrimary int
	if err := d.sql.QueryRow(
		`SELECT is_primary FROM site_domains WHERE site_id=1 AND domain=?`,
		seedSites[0].PrimaryDomain,
	).Scan(&isPrimary); err != nil {
		t.Fatalf("primary domain row: %v", err)
	}
	if isPrimary != 1 {
		t.Fatalf("primary domain is_primary = %d, want 1", isPrimary)
	}
}

func TestSeedSitesIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")

	d, err := Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	d.Close()

	d2, err := Open(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer d2.Close()

	var count int
	if err := d2.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != len(seedSites) {
		t.Fatalf("after reopen: expected %d sites, got %d (seed ran twice?)", len(seedSites), count)
	}
}

func seedUser(t *testing.T, d *DB) int {
	t.Helper()
	u, err := d.CreateUser("test")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return u.ID
}

func TestGetMySitesEmpty(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	sites, err := d.GetMySites(userID)
	if err != nil {
		t.Fatalf("GetMySites: %v", err)
	}
	if len(sites) != 0 {
		t.Fatalf("expected empty, got %d sites", len(sites))
	}
}

func TestGetMySitesJoinsDomainsAndIPs(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	// Attach seed site id=1 (youtube) to this user as enabled
	if _, err := d.sql.Exec(
		`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`,
		userID,
	); err != nil {
		t.Fatalf("insert user_sites: %v", err)
	}

	sites, err := d.GetMySites(userID)
	if err != nil {
		t.Fatalf("GetMySites: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}

	s := sites[0]
	if s.ID != 1 || s.Slug != "youtube" || !s.Enabled {
		t.Fatalf("unexpected site: %+v", s)
	}
	// The primary domain plus all extras should be present
	wantDomains := append([]string{"youtube.com"}, seedSites[0].Domains...)
	if len(s.Domains) != len(wantDomains) {
		t.Fatalf("domains len = %d, want %d", len(s.Domains), len(wantDomains))
	}
	for _, want := range wantDomains {
		found := false
		for _, got := range s.Domains {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing domain %q in %v", want, s.Domains)
		}
	}
}
