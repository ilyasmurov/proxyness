package db

import (
	"database/sql"
	"testing"
)

// seedSiteForAdmin inserts a new site + primary domain and returns the site ID.
func seedSiteForAdmin(t *testing.T, d *DB, userID *int, primaryDomain, label string) int {
	t.Helper()
	tx, err := d.sql.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	uid := 0
	if userID != nil {
		uid = *userID
	}
	res, err := tx.Exec(
		`INSERT INTO sites (slug, label, primary_domain, approved, created_by_user_id) VALUES (?, ?, ?, 1, ?)`,
		primaryDomain, label, primaryDomain, nullableInt(uid),
	)
	if err != nil {
		tx.Rollback()
		t.Fatalf("insert site: %v", err)
	}
	id64, _ := res.LastInsertId()
	siteID := int(id64)
	if _, err := tx.Exec(
		`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 1)`,
		siteID, primaryDomain,
	); err != nil {
		tx.Rollback()
		t.Fatalf("insert primary domain: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return siteID
}

func nullableInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func TestListSitesWithStats(t *testing.T) {
	d := tempDB(t)

	// Create a user
	u, err := d.CreateUser("alice")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create a site via ApplyAddOp so it has proper user_sites + site_domains
	tx, _ := d.sql.Begin()
	res, err := d.ApplyAddOp(tx, u.ID, "example.com", "Example", 1000)
	if err != nil {
		t.Fatalf("ApplyAddOp: %v", err)
	}
	// Add an extra domain
	_, err = d.ApplyAddDomainOp(tx, u.ID, res.SiteID, "example.net", 1001)
	if err != nil {
		t.Fatalf("ApplyAddDomainOp: %v", err)
	}
	tx.Commit()

	// Create a second user and link to same site
	u2, _ := d.CreateUser("bob")
	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, ?, 1, 2000)`, u2.ID, res.SiteID)

	sites, err := d.ListSitesWithStats()
	if err != nil {
		t.Fatalf("ListSitesWithStats: %v", err)
	}

	// Find our site (there are also seed sites)
	var found *SiteWithStats
	for i := range sites {
		if sites[i].Slug == "example" {
			found = &sites[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("site 'example' not found in list")
	}

	if found.Label != "Example" {
		t.Errorf("label = %q, want Example", found.Label)
	}
	if found.PrimaryDomain != "example.com" {
		t.Errorf("primary_domain = %q, want example.com", found.PrimaryDomain)
	}
	if !found.Approved {
		t.Errorf("approved should be true")
	}
	if found.CreatedByUserID == nil || *found.CreatedByUserID != u.ID {
		t.Errorf("created_by_user_id = %v, want %d", found.CreatedByUserID, u.ID)
	}
	if found.CreatedByUserName != "alice" {
		t.Errorf("created_by_user_name = %q, want alice", found.CreatedByUserName)
	}
	if found.UsersCount != 2 {
		t.Errorf("users_count = %d, want 2", found.UsersCount)
	}
	if found.DomainsCount != 2 {
		t.Errorf("domains_count = %d, want 2 (primary + extra)", found.DomainsCount)
	}
}

func TestListSitesWithStatsNullCreator(t *testing.T) {
	d := tempDB(t)

	// The seed sites have NULL created_by_user_id
	sites, err := d.ListSitesWithStats()
	if err != nil {
		t.Fatalf("ListSitesWithStats: %v", err)
	}
	if len(sites) == 0 {
		t.Fatalf("expected seed sites, got empty list")
	}
	// Seed sites should have empty creator name and nil user id
	for _, s := range sites {
		if s.CreatedByUserID == nil {
			if s.CreatedByUserName != "" {
				t.Errorf("seed site %q: expected empty creator name, got %q", s.Slug, s.CreatedByUserName)
			}
			break
		}
	}
}

func TestGetSiteDetail(t *testing.T) {
	d := tempDB(t)

	u, _ := d.CreateUser("alice")
	u2, _ := d.CreateUser("bob")

	tx, _ := d.sql.Begin()
	res, _ := d.ApplyAddOp(tx, u.ID, "detail.com", "Detail", 1000)
	siteID := res.SiteID
	d.ApplyAddDomainOp(tx, u.ID, siteID, "detail.net", 1001)
	tx.Commit()

	// Add bob to the site
	d.sql.Exec(`INSERT INTO user_sites (user_id, site_id, enabled, updated_at) VALUES (?, ?, 0, 2000)`, u2.ID, siteID)

	detail, err := d.GetSiteDetail(siteID)
	if err != nil {
		t.Fatalf("GetSiteDetail: %v", err)
	}
	if detail.ID != siteID {
		t.Errorf("id = %d, want %d", detail.ID, siteID)
	}
	if detail.Slug != "detail" {
		t.Errorf("slug = %q, want detail", detail.Slug)
	}
	if detail.Label != "Detail" {
		t.Errorf("label = %q, want Detail", detail.Label)
	}
	if detail.PrimaryDomain != "detail.com" {
		t.Errorf("primary_domain = %q, want detail.com", detail.PrimaryDomain)
	}
	if !detail.Approved {
		t.Errorf("approved should be true")
	}
	if detail.CreatedByUserName != "alice" {
		t.Errorf("created_by_user_name = %q, want alice", detail.CreatedByUserName)
	}

	// Domains: primary first, then alphabetical
	if len(detail.Domains) != 2 {
		t.Fatalf("domains len = %d, want 2", len(detail.Domains))
	}
	if detail.Domains[0].Domain != "detail.com" || !detail.Domains[0].IsPrimary {
		t.Errorf("domains[0] = %+v, want {detail.com, true}", detail.Domains[0])
	}
	if detail.Domains[1].Domain != "detail.net" || detail.Domains[1].IsPrimary {
		t.Errorf("domains[1] = %+v, want {detail.net, false}", detail.Domains[1])
	}

	// Users: ordered by name ASC: alice, bob
	if len(detail.Users) != 2 {
		t.Fatalf("users len = %d, want 2", len(detail.Users))
	}
	if detail.Users[0].Name != "alice" || !detail.Users[0].Enabled {
		t.Errorf("users[0] = %+v, want {alice, enabled}", detail.Users[0])
	}
	if detail.Users[1].Name != "bob" || detail.Users[1].Enabled {
		t.Errorf("users[1] = %+v, want {bob, disabled}", detail.Users[1])
	}
}

func TestGetSiteDetailNotFound(t *testing.T) {
	d := tempDB(t)

	_, err := d.GetSiteDetail(99999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestDeleteSite(t *testing.T) {
	d := tempDB(t)

	u, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	res, _ := d.ApplyAddOp(tx, u.ID, "todelete.com", "ToDelete", 1000)
	siteID := res.SiteID
	d.ApplyAddDomainOp(tx, u.ID, siteID, "todelete.net", 1001)
	tx.Commit()

	// Verify row exists before delete
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM sites WHERE id=?`, siteID).Scan(&count)
	if count != 1 {
		t.Fatalf("site should exist before delete")
	}

	if err := d.DeleteSite(siteID); err != nil {
		t.Fatalf("DeleteSite: %v", err)
	}

	// Verify cascade: site gone
	d.sql.QueryRow(`SELECT COUNT(*) FROM sites WHERE id=?`, siteID).Scan(&count)
	if count != 0 {
		t.Errorf("site still exists after delete")
	}

	// Verify cascade: site_domains gone
	d.sql.QueryRow(`SELECT COUNT(*) FROM site_domains WHERE site_id=?`, siteID).Scan(&count)
	if count != 0 {
		t.Errorf("site_domains rows still exist: %d", count)
	}

	// Verify cascade: user_sites gone
	d.sql.QueryRow(`SELECT COUNT(*) FROM user_sites WHERE site_id=?`, siteID).Scan(&count)
	if count != 0 {
		t.Errorf("user_sites rows still exist: %d", count)
	}
}

func TestDeleteSiteNotFound(t *testing.T) {
	d := tempDB(t)

	err := d.DeleteSite(99999)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestDeleteSiteDomainSecondary(t *testing.T) {
	d := tempDB(t)

	u, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	res, _ := d.ApplyAddOp(tx, u.ID, "multi.com", "Multi", 1000)
	siteID := res.SiteID
	d.ApplyAddDomainOp(tx, u.ID, siteID, "multi.net", 1001)
	tx.Commit()

	// Delete the secondary domain
	if err := d.DeleteSiteDomain(siteID, "multi.net"); err != nil {
		t.Fatalf("DeleteSiteDomain: %v", err)
	}

	// Verify it's gone
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM site_domains WHERE site_id=? AND domain=?`, siteID, "multi.net").Scan(&count)
	if count != 0 {
		t.Errorf("secondary domain still exists after delete")
	}

	// Primary domain should still be there
	d.sql.QueryRow(`SELECT COUNT(*) FROM site_domains WHERE site_id=? AND domain=?`, siteID, "multi.com").Scan(&count)
	if count != 1 {
		t.Errorf("primary domain missing after deleting secondary")
	}
}

func TestDeleteSiteDomainPrimaryRejected(t *testing.T) {
	d := tempDB(t)

	u, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	res, _ := d.ApplyAddOp(tx, u.ID, "primary.com", "Primary", 1000)
	tx.Commit()

	err := d.DeleteSiteDomain(res.SiteID, "primary.com")
	if err == nil {
		t.Fatalf("expected error when deleting primary domain, got nil")
	}
	if err.Error() != "cannot delete primary domain" {
		t.Errorf("error = %q, want 'cannot delete primary domain'", err.Error())
	}
}

func TestDeleteSiteDomainNotFound(t *testing.T) {
	d := tempDB(t)

	u, _ := d.CreateUser("alice")
	tx, _ := d.sql.Begin()
	res, _ := d.ApplyAddOp(tx, u.ID, "foo.com", "Foo", 1000)
	tx.Commit()

	err := d.DeleteSiteDomain(res.SiteID, "nonexistent.com")
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}
