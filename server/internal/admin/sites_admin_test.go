package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"proxyness/server/internal/db"
)

// seedSiteViaOp creates a site via ApplyAddOp and returns the site ID.
func seedSiteViaOp(t *testing.T, d *db.DB, userID int, primaryDomain, label string) int {
	t.Helper()
	sqlDB := d.SQL()
	tx, err := sqlDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := d.ApplyAddOp(tx, userID, primaryDomain, label, 1000)
	if err != nil {
		tx.Rollback()
		t.Fatalf("ApplyAddOp: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res.SiteID
}

func TestListSitesAdminEndpoint(t *testing.T) {
	h := setup(t)

	// Create a user and a site
	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", jsonBody(`{"name":"alice"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create user: %d — %s", rr.Code, rr.Body.String())
	}
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	seedSiteViaOp(t, h.db, user.ID, "listtest.com", "ListTest")

	req = authed(httptest.NewRequest(http.MethodGet, "/admin/api/sites", nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list sites: expected 200, got %d — %s", rr.Code, rr.Body.String())
	}

	var sites []db.SiteWithStats
	if err := json.NewDecoder(rr.Body).Decode(&sites); err != nil {
		t.Fatalf("decode sites: %v", err)
	}
	if len(sites) == 0 {
		t.Fatalf("expected at least one site (including seed sites), got 0")
	}

	// Find our site
	var found *db.SiteWithStats
	for i := range sites {
		if sites[i].Slug == "listtest" {
			found = &sites[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("created site 'listtest' not found in response")
	}
	if found.UsersCount != 1 {
		t.Errorf("users_count = %d, want 1", found.UsersCount)
	}
	if found.DomainsCount != 1 {
		t.Errorf("domains_count = %d, want 1", found.DomainsCount)
	}
	if found.CreatedByUserName != "alice" {
		t.Errorf("created_by_user_name = %q, want alice", found.CreatedByUserName)
	}
}

func TestGetSiteAdminEndpoint(t *testing.T) {
	h := setup(t)

	// Create user and site
	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", jsonBody(`{"name":"bob"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	siteID := seedSiteViaOp(t, h.db, user.ID, "gettest.com", "GetTest")

	// Add a secondary domain
	sqlDB := h.db.SQL()
	sqlDB.Exec(`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`, siteID, "gettest.net")

	// GET /admin/api/sites/{id}
	url := "/admin/api/sites/" + itoa(siteID)
	req = authed(httptest.NewRequest(http.MethodGet, url, nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get site: expected 200, got %d — %s", rr.Code, rr.Body.String())
	}

	var detail db.SiteDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode site detail: %v", err)
	}
	if detail.ID != siteID {
		t.Errorf("id = %d, want %d", detail.ID, siteID)
	}
	if detail.PrimaryDomain != "gettest.com" {
		t.Errorf("primary_domain = %q, want gettest.com", detail.PrimaryDomain)
	}
	if len(detail.Domains) != 2 {
		t.Errorf("domains len = %d, want 2", len(detail.Domains))
	}
	if len(detail.Users) != 1 {
		t.Errorf("users len = %d, want 1", len(detail.Users))
	}

	// 404 for non-existent site
	req = authed(httptest.NewRequest(http.MethodGet, "/admin/api/sites/99999", nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("non-existent site: expected 404, got %d", rr.Code)
	}
}

func TestDeleteSiteAdminEndpoint(t *testing.T) {
	h := setup(t)

	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", jsonBody(`{"name":"carol"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	siteID := seedSiteViaOp(t, h.db, user.ID, "delsite.com", "DelSite")

	url := "/admin/api/sites/" + itoa(siteID)
	req = authed(httptest.NewRequest(http.MethodDelete, url, nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete site: expected 204, got %d — %s", rr.Code, rr.Body.String())
	}

	// Second delete should return 404
	req = authed(httptest.NewRequest(http.MethodDelete, url, nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("second delete: expected 404, got %d", rr.Code)
	}
}

func TestDeleteSiteDomainAdminEndpoint(t *testing.T) {
	h := setup(t)

	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", jsonBody(`{"name":"dave"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	siteID := seedSiteViaOp(t, h.db, user.ID, "ytimg.com", "YTImg")

	// Add secondary domain
	h.db.SQL().Exec(`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`, siteID, "ytimg.net")

	url := "/admin/api/sites/" + itoa(siteID) + "/domains/ytimg.net"
	req = authed(httptest.NewRequest(http.MethodDelete, url, nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete domain: expected 204, got %d — %s", rr.Code, rr.Body.String())
	}

	// Verify domain gone
	var count int
	h.db.SQL().QueryRow(`SELECT COUNT(*) FROM site_domains WHERE site_id=? AND domain=?`, siteID, "ytimg.net").Scan(&count)
	if count != 0 {
		t.Errorf("domain still exists after delete")
	}
}

func TestDeleteSiteDomainPrimaryRejected(t *testing.T) {
	h := setup(t)

	req := authed(httptest.NewRequest(http.MethodPost, "/admin/api/users", jsonBody(`{"name":"eve"}`)))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var user db.User
	json.NewDecoder(rr.Body).Decode(&user)

	siteID := seedSiteViaOp(t, h.db, user.ID, "primary.com", "Primary")

	url := "/admin/api/sites/" + itoa(siteID) + "/domains/primary.com"
	req = authed(httptest.NewRequest(http.MethodDelete, url, nil))
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete primary domain: expected 400, got %d — %s", rr.Code, rr.Body.String())
	}
}

// ---- helpers ----

func jsonBody(s string) *strings.Reader {
	return strings.NewReader(s)
}
