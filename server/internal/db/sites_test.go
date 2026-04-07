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
