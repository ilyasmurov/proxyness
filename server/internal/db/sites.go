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
		WHERE us.user_id = $1
		ORDER BY s.label
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user_sites: %w", err)
	}
	defer rows.Close()

	out := []UserSite{}
	for rows.Next() {
		var s UserSite
		if err := rows.Scan(&s.ID, &s.Slug, &s.Label, &s.Enabled, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_sites: %w", err)
		}
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
			`SELECT domain FROM site_domains WHERE site_id = $1 ORDER BY is_primary DESC, domain`,
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
			`SELECT cidr FROM site_ips WHERE site_id = $1 ORDER BY cidr`,
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

// CatalogSite is a lightweight search result from the global sites catalog.
type CatalogSite struct {
	ID            int    `json:"id"`
	Label         string `json:"label"`
	PrimaryDomain string `json:"primary_domain"`
}

// SearchCatalog searches all sites by label or primary_domain (case-insensitive
// substring match). Used by the client's "add site" modal to show suggestions.
func (d *DB) SearchCatalog(q string, limit int) ([]CatalogSite, error) {
	q = "%" + strings.ToLower(strings.TrimSpace(q)) + "%"
	if limit <= 0 {
		limit = 20
	}
	rows, err := d.sql.Query(`
		SELECT id, label, primary_domain FROM sites
		WHERE LOWER(label) LIKE $1 OR LOWER(primary_domain) LIKE $2
		ORDER BY label
		LIMIT $3
	`, q, q, limit)
	if err != nil {
		return nil, fmt.Errorf("search catalog: %w", err)
	}
	defer rows.Close()

	var out []CatalogSite
	for rows.Next() {
		var s CatalogSite
		if err := rows.Scan(&s.ID, &s.Label, &s.PrimaryDomain); err != nil {
			return nil, fmt.Errorf("scan catalog: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
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
	err := tx.QueryRow(`SELECT id FROM sites WHERE primary_domain = $1`, primaryDomain).Scan(&existingID)
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

	var siteID int
	err = tx.QueryRow(
		`INSERT INTO sites (slug, label, primary_domain, approved, created_by_user_id)
		 VALUES ($1, $2, $3, TRUE, $4) RETURNING id`,
		slug, label, primaryDomain, userID,
	).Scan(&siteID)
	if err != nil {
		return AddOpResult{}, err
	}

	if _, err := tx.Exec(
		`INSERT INTO site_domains (site_id, domain, is_primary) VALUES ($1, $2, TRUE)`,
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
		`INSERT INTO user_sites (user_id, site_id, enabled, updated_at)
		 VALUES ($1, $2, TRUE, $3) ON CONFLICT (user_id, site_id) DO NOTHING`,
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
		err := tx.QueryRow(`SELECT id FROM sites WHERE slug = $1`, attempt).Scan(&existing)
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

// AddDomainOpResult captures what happened for a single "add_domain" sync op.
type AddDomainOpResult struct {
	Deduped bool
}

// ApplyAddDomainOp adds a domain to an existing site, only if the user is
// linked to that site via user_sites. Used by the discovery flow to enrich
// the global catalog with subdomains/CDN hosts a user encounters while
// browsing a site they have enabled.
func (d *DB) ApplyAddDomainOp(tx *sql.Tx, userID, siteID int, domain string, at int64) (AddDomainOpResult, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return AddDomainOpResult{}, fmt.Errorf("invalid domain: %q", domain)
	}

	// Auth: only users who have the site enabled can enrich its domains.
	var dummy int
	err := tx.QueryRow(
		`SELECT 1 FROM user_sites WHERE user_id=$1 AND site_id=$2`,
		userID, siteID,
	).Scan(&dummy)
	if err == sql.ErrNoRows {
		return AddDomainOpResult{}, fmt.Errorf("user %d not linked to site %d", userID, siteID)
	}
	if err != nil {
		return AddDomainOpResult{}, err
	}

	res, err := tx.Exec(
		`INSERT INTO site_domains (site_id, domain, is_primary) VALUES ($1, $2, FALSE) ON CONFLICT (site_id, domain) DO NOTHING`,
		siteID, domain,
	)
	if err != nil {
		return AddDomainOpResult{}, err
	}
	rows, _ := res.RowsAffected()
	return AddDomainOpResult{Deduped: rows == 0}, nil
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
		`SELECT updated_at FROM user_sites WHERE user_id=$1 AND site_id=$2`,
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

	if _, err := tx.Exec(
		`UPDATE user_sites SET enabled=$1, updated_at=$2 WHERE user_id=$3 AND site_id=$4`,
		enabled, at, userID, siteID,
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
		`DELETE FROM user_sites WHERE user_id=$1 AND site_id=$2`,
		userID, siteID,
	)
	return err
}
