package db

import (
	"database/sql"
	"fmt"
)

// SiteWithStats is the shape returned by ListSitesWithStats for the admin
// /admin/api/sites endpoint.
type SiteWithStats struct {
	ID                int    `json:"id"`
	Slug              string `json:"slug"`
	Label             string `json:"label"`
	PrimaryDomain     string `json:"primary_domain"`
	Approved          bool   `json:"approved"`
	CreatedByUserID   *int   `json:"created_by_user_id"`
	CreatedByUserName string `json:"created_by_user_name"`
	UsersCount        int    `json:"users_count"`
	DomainsCount      int    `json:"domains_count"`
	CreatedAt         string `json:"created_at"`
}

// SiteDetail is the shape returned by GetSiteDetail for the admin
// /admin/api/sites/{id} endpoint.
type SiteDetail struct {
	ID                int             `json:"id"`
	Slug              string          `json:"slug"`
	Label             string          `json:"label"`
	PrimaryDomain     string          `json:"primary_domain"`
	Approved          bool            `json:"approved"`
	CreatedByUserID   *int            `json:"created_by_user_id"`
	CreatedByUserName string          `json:"created_by_user_name"`
	CreatedAt         string          `json:"created_at"`
	Domains           []SiteDomainRow `json:"domains"`
	Users             []SiteUserRow   `json:"users"`
}

// SiteDomainRow is a single domain entry in SiteDetail.
type SiteDomainRow struct {
	Domain    string `json:"domain"`
	IsPrimary bool   `json:"is_primary"`
}

// SiteUserRow is a single user entry in SiteDetail.
type SiteUserRow struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updated_at"`
}

// ListSitesWithStats returns all sites with user/domain counts and creator name.
func (d *DB) ListSitesWithStats() ([]SiteWithStats, error) {
	rows, err := d.sql.Query(`
		SELECT s.id, s.slug, s.label, s.primary_domain, s.approved, s.created_by_user_id,
		       COALESCE(u.name, '') AS created_by_user_name,
		       (SELECT COUNT(*) FROM user_sites WHERE site_id=s.id) AS users_count,
		       (SELECT COUNT(*) FROM site_domains WHERE site_id=s.id) AS domains_count,
		       s.created_at
		FROM sites s
		LEFT JOIN users u ON u.id = s.created_by_user_id
		ORDER BY s.id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list sites with stats: %w", err)
	}
	defer rows.Close()

	var out []SiteWithStats
	for rows.Next() {
		var s SiteWithStats
		if err := rows.Scan(
			&s.ID, &s.Slug, &s.Label, &s.PrimaryDomain, &s.Approved,
			&s.CreatedByUserID, &s.CreatedByUserName,
			&s.UsersCount, &s.DomainsCount, &s.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan site with stats: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSiteDetail returns detailed info for one site including domains and users.
// Returns (nil, sql.ErrNoRows) if not found.
func (d *DB) GetSiteDetail(siteID int) (*SiteDetail, error) {
	var s SiteDetail
	err := d.sql.QueryRow(`
		SELECT s.id, s.slug, s.label, s.primary_domain, s.approved, s.created_by_user_id,
		       COALESCE(u.name, '') AS created_by_user_name, s.created_at
		FROM sites s
		LEFT JOIN users u ON u.id = s.created_by_user_id
		WHERE s.id = $1
	`, siteID).Scan(
		&s.ID, &s.Slug, &s.Label, &s.PrimaryDomain, &s.Approved,
		&s.CreatedByUserID, &s.CreatedByUserName, &s.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("get site detail: %w", err)
	}

	// Fetch domains ordered by is_primary DESC, domain ASC
	domRows, err := d.sql.Query(`
		SELECT domain, is_primary FROM site_domains
		WHERE site_id = $1
		ORDER BY is_primary DESC, domain ASC
	`, siteID)
	if err != nil {
		return nil, fmt.Errorf("get site domains: %w", err)
	}
	defer domRows.Close()

	s.Domains = []SiteDomainRow{}
	for domRows.Next() {
		var row SiteDomainRow
		if err := domRows.Scan(&row.Domain, &row.IsPrimary); err != nil {
			return nil, fmt.Errorf("scan domain row: %w", err)
		}
		s.Domains = append(s.Domains, row)
	}
	if err := domRows.Err(); err != nil {
		return nil, err
	}

	// Fetch users ordered by name ASC
	userRows, err := d.sql.Query(`
		SELECT u.id, u.name, us.enabled, us.updated_at
		FROM user_sites us
		JOIN users u ON u.id = us.user_id
		WHERE us.site_id = $1
		ORDER BY u.name ASC
	`, siteID)
	if err != nil {
		return nil, fmt.Errorf("get site users: %w", err)
	}
	defer userRows.Close()

	s.Users = []SiteUserRow{}
	for userRows.Next() {
		var row SiteUserRow
		if err := userRows.Scan(&row.ID, &row.Name, &row.Enabled, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user row: %w", err)
		}
		s.Users = append(s.Users, row)
	}
	if err := userRows.Err(); err != nil {
		return nil, err
	}

	return &s, nil
}

// DeleteSite deletes a site by ID. Returns sql.ErrNoRows if not found.
// Cascades to site_domains, site_ips, user_sites via foreign keys.
func (d *DB) DeleteSite(siteID int) error {
	res, err := d.sql.Exec(`DELETE FROM sites WHERE id = $1`, siteID)
	if err != nil {
		return fmt.Errorf("delete site: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteSiteDomain deletes a single domain from site_domains.
// Returns sql.ErrNoRows if no such row, or an error "cannot delete primary domain"
// if the domain is the primary domain.
func (d *DB) DeleteSiteDomain(siteID int, domain string) error {
	var isPrimary bool
	err := d.sql.QueryRow(
		`SELECT is_primary FROM site_domains WHERE site_id = $1 AND domain = $2`,
		siteID, domain,
	).Scan(&isPrimary)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return fmt.Errorf("check domain: %w", err)
	}
	if isPrimary {
		return fmt.Errorf("cannot delete primary domain")
	}
	_, err = d.sql.Exec(
		`DELETE FROM site_domains WHERE site_id = $1 AND domain = $2`,
		siteID, domain,
	)
	if err != nil {
		return fmt.Errorf("delete domain: %w", err)
	}
	return nil
}
