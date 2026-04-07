package db

// SeedSite describes a built-in catalog entry bundled with the server
// binary and the client release. The slice index (starting from 0)
// determines the site's id after SeedSitesIfEmpty runs: sites[i] → id=i+1.
// This guarantees the seed JSON shipped with the client matches the DB
// without a query-back step.
type SeedSite struct {
	Slug          string
	Label         string
	PrimaryDomain string   // dedup key, also inserted as an is_primary=1 row in site_domains
	Domains       []string // extra domains, NOT including PrimaryDomain
	IPs           []string // CIDRs (may be empty)
}

// seedSites is the authoritative source of built-in catalog entries.
// Order matters — the slice index becomes the site's DB id.
var seedSites = []SeedSite{
	{
		Slug:          "youtube",
		Label:         "YouTube",
		PrimaryDomain: "youtube.com",
		Domains: []string{
			"googlevideo.com", "ytimg.com", "ggpht.com",
			"youtube-nocookie.com", "youtu.be",
			"googleapis.com", "gstatic.com", "google.com",
		},
	},
	{
		Slug:          "instagram",
		Label:         "Instagram",
		PrimaryDomain: "instagram.com",
		Domains: []string{
			"cdninstagram.com", "fbcdn.net", "facebook.com",
			"fbsbx.com",
		},
	},
	{
		Slug:          "twitter",
		Label:         "Twitter / X",
		PrimaryDomain: "twitter.com",
		Domains: []string{
			"x.com", "twimg.com", "t.co", "abs.twimg.com",
		},
	},
	{
		Slug:          "facebook",
		Label:         "Facebook",
		PrimaryDomain: "facebook.com",
		Domains: []string{
			"fbcdn.net", "fbsbx.com", "facebook.net",
			"cdninstagram.com", "fb.com",
		},
	},
	{
		Slug:          "discord",
		Label:         "Discord (web)",
		PrimaryDomain: "discord.com",
		Domains: []string{
			"discordapp.com", "discordapp.net", "discord.gg",
			"discord.media",
		},
	},
	{
		Slug:          "linkedin",
		Label:         "LinkedIn",
		PrimaryDomain: "linkedin.com",
		Domains:       []string{"licdn.com", "linkedin.cn"},
	},
	{
		Slug:          "medium",
		Label:         "Medium",
		PrimaryDomain: "medium.com",
	},
	{
		Slug:          "claude",
		Label:         "Claude",
		PrimaryDomain: "claude.ai",
		Domains:       []string{"anthropic.com"},
	},
	{
		Slug:          "youtrack",
		Label:         "YouTrack",
		PrimaryDomain: "youtrack.cloud",
		Domains:       []string{"jetbrains.com"},
	},
	{
		Slug:          "telegram-web",
		Label:         "Telegram (web)",
		PrimaryDomain: "web.telegram.org",
		Domains:       []string{"telegram.org", "t.me", "telegram.me"},
	},
}

// SeedSitesIfEmpty populates the sites/site_domains/site_ips tables
// with the built-in catalog. It's a no-op if sites already has any row,
// so it runs once per fresh database and is safe to call on every start.
// Uses explicit ids (i+1) so the bundled seed JSON can predict them.
func (d *DB) SeedSitesIfEmpty() error {
	var count int
	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM sites`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, s := range seedSites {
		id := int64(i + 1)
		if _, err := tx.Exec(
			`INSERT INTO sites (id, slug, label, primary_domain, approved, created_by_user_id)
			 VALUES (?, ?, ?, ?, 1, NULL)`,
			id, s.Slug, s.Label, s.PrimaryDomain,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 1)`,
			id, s.PrimaryDomain,
		); err != nil {
			return err
		}
		for _, dom := range s.Domains {
			if _, err := tx.Exec(
				`INSERT INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`,
				id, dom,
			); err != nil {
				return err
			}
		}
		for _, cidr := range s.IPs {
			if _, err := tx.Exec(
				`INSERT INTO site_ips (site_id, cidr) VALUES (?, ?)`,
				id, cidr,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ExportSeedSites returns the seed slice for tools like cmd/export-seed.
// It's exported only because export-seed lives in a different package.
func ExportSeedSites() []SeedSite {
	return seedSites
}
