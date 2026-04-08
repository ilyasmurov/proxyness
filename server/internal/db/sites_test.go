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

func TestApplyAddOpNewSite(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := d.ApplyAddOp(tx, userID, "example.com", "Example", 1000)
	if err != nil {
		t.Fatalf("ApplyAddOp: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if res.Deduped {
		t.Fatalf("new site should not be deduped")
	}
	if res.SiteID == 0 {
		t.Fatalf("SiteID should be set")
	}

	var slug, primary string
	if err := d.sql.QueryRow(`SELECT slug, primary_domain FROM sites WHERE id=?`, res.SiteID).Scan(&slug, &primary); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if slug != "example" || primary != "example.com" {
		t.Fatalf("row = %q/%q, want example/example.com", slug, primary)
	}

	// user_sites row created with enabled=true
	var enabled int
	if err := d.sql.QueryRow(
		`SELECT enabled FROM user_sites WHERE user_id=? AND site_id=?`, userID, res.SiteID,
	).Scan(&enabled); err != nil {
		t.Fatalf("user_sites lookup: %v", err)
	}
	if enabled != 1 {
		t.Fatalf("enabled = %d, want 1", enabled)
	}

	// site_domains has primary row
	var isPrimary int
	if err := d.sql.QueryRow(
		`SELECT is_primary FROM site_domains WHERE site_id=? AND domain=?`, res.SiteID, "example.com",
	).Scan(&isPrimary); err != nil {
		t.Fatalf("site_domains: %v", err)
	}
	if isPrimary != 1 {
		t.Fatalf("primary flag wrong")
	}

	// sites.created_by_user_id is set
	var createdBy int
	if err := d.sql.QueryRow(
		`SELECT created_by_user_id FROM sites WHERE id=?`, res.SiteID,
	).Scan(&createdBy); err != nil {
		t.Fatalf("created_by: %v", err)
	}
	if createdBy != userID {
		t.Fatalf("created_by = %d, want %d", createdBy, userID)
	}
}

func TestApplyAddOpDedupExisting(t *testing.T) {
	d := tempDB(t)
	userA := seedUser(t, d)
	userB, _ := d.CreateUser("b")

	tx, _ := d.sql.Begin()
	resA, _ := d.ApplyAddOp(tx, userA, "example.com", "Example", 1000)
	tx.Commit()

	tx2, _ := d.sql.Begin()
	resB, err := d.ApplyAddOp(tx2, userB.ID, "example.com", "ExampleUnused", 2000)
	if err != nil {
		t.Fatalf("ApplyAddOp B: %v", err)
	}
	tx2.Commit()

	if resB.SiteID != resA.SiteID {
		t.Fatalf("dedup failed: A=%d B=%d", resA.SiteID, resB.SiteID)
	}
	if !resB.Deduped {
		t.Fatalf("should be marked deduped")
	}

	// Label is not overwritten
	var label string
	d.sql.QueryRow(`SELECT label FROM sites WHERE id=?`, resA.SiteID).Scan(&label)
	if label != "Example" {
		t.Fatalf("label = %q, want Example", label)
	}

	// Both users have user_sites rows
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM user_sites WHERE site_id=?`, resA.SiteID).Scan(&count)
	if count != 2 {
		t.Fatalf("user_sites count = %d, want 2", count)
	}
}

func TestApplyAddOpExistingUserSiteIsNoop(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	res1, _ := d.ApplyAddOp(tx, userID, "example.com", "Example", 1000)
	tx.Commit()

	// Disable it manually
	d.sql.Exec(`UPDATE user_sites SET enabled=0, updated_at=1500 WHERE user_id=? AND site_id=?`, userID, res1.SiteID)

	// Re-add should be a no-op: enabled stays 0, updated_at stays 1500
	tx2, _ := d.sql.Begin()
	res2, _ := d.ApplyAddOp(tx2, userID, "example.com", "Example", 3000)
	tx2.Commit()

	if res2.SiteID != res1.SiteID {
		t.Fatalf("site id mismatch")
	}

	var enabled int
	var updatedAt int64
	d.sql.QueryRow(
		`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, res2.SiteID,
	).Scan(&enabled, &updatedAt)
	if enabled != 0 || updatedAt != 1500 {
		t.Fatalf("re-add mutated state: enabled=%d updated_at=%d", enabled, updatedAt)
	}
}

func TestApplyAddOpSlugCollision(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	// Add two different primary_domains that produce the same base slug
	res1, _ := d.ApplyAddOp(tx, userID, "foo.com", "Foo", 1000)
	res2, err := d.ApplyAddOp(tx, userID, "foo.net", "Foo Net", 1001)
	if err != nil {
		t.Fatalf("ApplyAddOp 2: %v", err)
	}
	tx.Commit()

	var slug1, slug2 string
	d.sql.QueryRow(`SELECT slug FROM sites WHERE id=?`, res1.SiteID).Scan(&slug1)
	d.sql.QueryRow(`SELECT slug FROM sites WHERE id=?`, res2.SiteID).Scan(&slug2)
	if slug1 != "foo" {
		t.Fatalf("slug1 = %q, want foo", slug1)
	}
	if slug2 != "foo2" {
		t.Fatalf("slug2 = %q, want foo2", slug2)
	}
}

func TestApplyAddOpInvalidDomain(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	defer tx.Rollback()
	_, err := d.ApplyAddOp(tx, userID, "NOT A DOMAIN", "Bad", 1000)
	if err == nil {
		t.Fatalf("expected error for invalid domain")
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

func TestApplyToggleOpLWW(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	// Attach site id=1 at t=1000 enabled
	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`, userID)

	// Older disable op should be stale
	tx, _ := d.sql.Begin()
	status := d.ApplyToggleOp(tx, userID, 1, false, 500)
	tx.Commit()
	if status != ToggleStale {
		t.Fatalf("old op status = %v, want stale", status)
	}
	var enabled int
	var updatedAt int64
	d.sql.QueryRow(`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&enabled, &updatedAt)
	if enabled != 1 || updatedAt != 1000 {
		t.Fatalf("row mutated: enabled=%d updated_at=%d", enabled, updatedAt)
	}

	// Newer disable op wins
	tx2, _ := d.sql.Begin()
	status = d.ApplyToggleOp(tx2, userID, 1, false, 2000)
	tx2.Commit()
	if status != ToggleOK {
		t.Fatalf("new op status = %v, want ok", status)
	}
	d.sql.QueryRow(`SELECT enabled, updated_at FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&enabled, &updatedAt)
	if enabled != 0 || updatedAt != 2000 {
		t.Fatalf("updated row wrong: enabled=%d updated_at=%d", enabled, updatedAt)
	}
}

func TestApplyToggleOpMissingRow(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	tx, _ := d.sql.Begin()
	status := d.ApplyToggleOp(tx, userID, 999, true, 1000)
	tx.Rollback()
	if status != ToggleNotFound {
		t.Fatalf("missing row status = %v, want not_found", status)
	}
}

func TestApplyAddDomainOpAddsAndDedupes(t *testing.T) {
	d := tempDB(t)
	user, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	addRes, err := d.ApplyAddOp(tx, user.ID, "habr.com", "Habr", 1000)
	if err != nil {
		t.Fatal(err)
	}
	siteID := addRes.SiteID

	// First insert: added.
	r, err := d.ApplyAddDomainOp(tx, user.ID, siteID, "habrcdn.io", 2000)
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if r.Deduped {
		t.Fatalf("expected added, got deduped")
	}

	// Second insert of same domain: dedup.
	r2, err := d.ApplyAddDomainOp(tx, user.ID, siteID, "habrcdn.io", 3000)
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if !r2.Deduped {
		t.Fatalf("expected deduped, got added")
	}

	tx.Commit()

	// Verify row exists.
	var count int
	d.sql.QueryRow(
		`SELECT COUNT(*) FROM site_domains WHERE site_id=? AND domain=?`,
		siteID, "habrcdn.io",
	).Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 row, got %d", count)
	}
}

func TestApplyAddDomainOpRejectsNonLinkedUser(t *testing.T) {
	d := tempDB(t)
	owner, _ := d.CreateUser("owner")
	stranger, _ := d.CreateUser("stranger")

	tx, _ := d.sql.Begin()
	addRes, _ := d.ApplyAddOp(tx, owner.ID, "habr.com", "Habr", 1000)

	// Stranger should NOT be allowed to add domains to owner's site.
	_, err := d.ApplyAddDomainOp(tx, stranger.ID, addRes.SiteID, "habrcdn.io", 2000)
	if err == nil {
		t.Fatalf("expected error for non-linked user, got nil")
	}
	tx.Rollback()
}

func TestApplyAddDomainOpRejectsInvalidDomain(t *testing.T) {
	d := tempDB(t)
	user, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	addRes, _ := d.ApplyAddOp(tx, user.ID, "habr.com", "Habr", 1000)

	_, err := d.ApplyAddDomainOp(tx, user.ID, addRes.SiteID, "NOT A DOMAIN", 2000)
	if err == nil {
		t.Fatalf("expected error for invalid domain")
	}
	tx.Rollback()
}

func TestApplyRemoveOp(t *testing.T) {
	d := tempDB(t)
	userID := seedUser(t, d)

	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, 1, 1, 1000)`, userID)

	tx, _ := d.sql.Begin()
	if err := d.ApplyRemoveOp(tx, userID, 1); err != nil {
		t.Fatalf("ApplyRemoveOp: %v", err)
	}
	tx.Commit()

	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM user_sites WHERE user_id=? AND site_id=1`, userID).Scan(&count)
	if count != 0 {
		t.Fatalf("row not removed: %d", count)
	}

	// sites row untouched
	d.sql.QueryRow(`SELECT COUNT(*) FROM sites WHERE id=1`).Scan(&count)
	if count != 1 {
		t.Fatalf("catalog row was touched: %d", count)
	}
}
