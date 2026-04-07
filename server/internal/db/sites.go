package db

import (
	"fmt"
)

// UserSite is a single row of the user's current catalog snapshot as
// returned by GetMySites. It joins sites + user_sites + site_domains + site_ips.
type UserSite struct {
	ID        int      `json:"id"`
	Slug      string   `json:"slug"`
	Label     string   `json:"label"`
	Domains   []string `json:"domains"`
	IPs       []string `json:"ips"`
	Enabled   bool     `json:"enabled"`
	UpdatedAt int64    `json:"updated_at"`
}

// GetMySites returns all catalog rows attached to the given user via
// user_sites, joined with their domains and ips. Result is ordered by label.
func (d *DB) GetMySites(userID int) ([]UserSite, error) {
	rows, err := d.sql.Query(`
		SELECT s.id, s.slug, s.label, us.enabled, us.updated_at
		FROM user_sites us
		JOIN sites s ON us.site_id = s.id
		WHERE us.user_id = ?
		ORDER BY s.label
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user_sites: %w", err)
	}
	defer rows.Close()

	out := []UserSite{}
	for rows.Next() {
		var s UserSite
		var enabledInt int
		if err := rows.Scan(&s.ID, &s.Slug, &s.Label, &enabledInt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_sites: %w", err)
		}
		s.Enabled = enabledInt != 0
		s.Domains = []string{}
		s.IPs = []string{}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attach domains and ips with two follow-up queries per site.
	// Phase A has ≤100 rows per user, so N+1 is fine; optimize later if needed.
	for i := range out {
		domRows, err := d.sql.Query(
			`SELECT domain FROM site_domains WHERE site_id = ? ORDER BY is_primary DESC, domain`,
			out[i].ID,
		)
		if err != nil {
			return nil, fmt.Errorf("query domains: %w", err)
		}
		for domRows.Next() {
			var dom string
			if err := domRows.Scan(&dom); err != nil {
				domRows.Close()
				return nil, err
			}
			out[i].Domains = append(out[i].Domains, dom)
		}
		domRows.Close()

		ipRows, err := d.sql.Query(
			`SELECT cidr FROM site_ips WHERE site_id = ? ORDER BY cidr`,
			out[i].ID,
		)
		if err != nil {
			return nil, fmt.Errorf("query ips: %w", err)
		}
		for ipRows.Next() {
			var c string
			if err := ipRows.Scan(&c); err != nil {
				ipRows.Close()
				return nil, err
			}
			out[i].IPs = append(out[i].IPs, c)
		}
		ipRows.Close()
	}

	return out, nil
}
