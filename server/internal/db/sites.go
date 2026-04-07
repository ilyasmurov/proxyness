package db

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
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

// AddOpResult captures what happened for a single "add" sync op.
type AddOpResult struct {
	SiteID  int
	Deduped bool // true if the primary_domain was already in the catalog
}

// domainRE is a permissive lowercase domain matcher used to reject
// obviously bogus user input like "NOT A DOMAIN" or "foo". Requires at
// least one dot and standard DNS label characters.
var domainRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

// ApplyAddOp handles an "add" sync operation inside the given transaction.
func (d *DB) ApplyAddOp(tx *sql.Tx, userID int, primaryDomain, label string, at int64) (AddOpResult, error) {
	primaryDomain = strings.ToLower(strings.TrimSpace(primaryDomain))
	if !domainRE.MatchString(primaryDomain) {
		return AddOpResult{}, fmt.Errorf("invalid domain: %q", primaryDomain)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = labelFromDomain(primaryDomain)
	}

	var existingID int
	err := tx.QueryRow(`SELECT id FROM sites WHERE primary_domain = ?`, primaryDomain).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return AddOpResult{}, err
	}

	if existingID != 0 {
		if err := ensureUserSite(tx, userID, existingID, at); err != nil {
			return AddOpResult{}, err
		}
		return AddOpResult{SiteID: existingID, Deduped: true}, nil
	}

	slug, err := pickSlug(tx, primaryDomain)
	if err != nil {
		return AddOpResult{}, err
	}

	res, err := tx.Exec(
		`INSERT INTO sites (slug, label, primary_domain, approved, created_by_user_id)
		 VALUES (?, ?, ?, 1, ?)`,
		slug, label, primaryDomain, userID,
	)
	if err != nil {
		return AddOpResult{}, err
	}
	siteID64, err := res.LastInsertId()
	if err != nil {
		return AddOpResult{}, err
	}
	siteID := int(siteID64)

	if _, err := tx.Exec(
		`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 1)`,
		siteID, primaryDomain,
	); err != nil {
		return AddOpResult{}, err
	}

	if err := ensureUserSite(tx, userID, siteID, at); err != nil {
		return AddOpResult{}, err
	}
	return AddOpResult{SiteID: siteID, Deduped: false}, nil
}

func ensureUserSite(tx *sql.Tx, userID, siteID int, at int64) error {
	_, err := tx.Exec(
		`INSERT OR IGNORE INTO user_sites (user_id, site_id, enabled, updated_at)
		 VALUES (?, ?, 1, ?)`,
		userID, siteID, at,
	)
	return err
}

func pickSlug(tx *sql.Tx, primaryDomain string) (string, error) {
	base := primaryDomain
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	var cleaned strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cleaned.WriteRune(r)
		}
	}
	slug := cleaned.String()
	if slug == "" {
		slug = "site"
	}

	attempt := slug
	suffix := 2
	for {
		var existing int
		err := tx.QueryRow(`SELECT id FROM sites WHERE slug = ?`, attempt).Scan(&existing)
		if err == sql.ErrNoRows {
			return attempt, nil
		}
		if err != nil {
			return "", err
		}
		attempt = fmt.Sprintf("%s%d", slug, suffix)
		suffix++
		if suffix > 1000 {
			return "", fmt.Errorf("too many slug collisions for %q", slug)
		}
	}
}

func labelFromDomain(domain string) string {
	base := domain
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	if base == "" {
		return domain
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

// ToggleStatus enumerates the outcomes of an ApplyToggleOp call.
type ToggleStatus int

const (
	ToggleOK       ToggleStatus = iota // the row was updated
	ToggleStale                        // op's at was older than updated_at, no change
	ToggleNotFound                     // user_sites row didn't exist
)

// ApplyToggleOp updates enabled/updated_at on an existing user_sites row
// only if the incoming op's at is >= current updated_at (last-write-wins).
// Returns a status explaining what happened.
func (d *DB) ApplyToggleOp(tx *sql.Tx, userID, siteID int, enabled bool, at int64) ToggleStatus {
	var currentAt int64
	err := tx.QueryRow(
		`SELECT updated_at FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, siteID,
	).Scan(&currentAt)
	if err == sql.ErrNoRows {
		return ToggleNotFound
	}
	if err != nil {
		return ToggleNotFound
	}
	if at < currentAt {
		return ToggleStale
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	if _, err := tx.Exec(
		`UPDATE user_sites SET enabled=?, updated_at=? WHERE user_id=? AND site_id=?`,
		enabledInt, at, userID, siteID,
	); err != nil {
		return ToggleNotFound
	}
	return ToggleOK
}

// ApplyRemoveOp deletes the user_sites row for (user, site). The catalog
// row is not touched. Returns an error only for unexpected DB errors;
// deleting a missing row is a silent no-op.
func (d *DB) ApplyRemoveOp(tx *sql.Tx, userID, siteID int) error {
	_, err := tx.Exec(
		`DELETE FROM user_sites WHERE user_id=? AND site_id=?`,
		userID, siteID,
	)
	return err
}
