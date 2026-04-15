package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const sqliteTimeLayout = "2006-01-02 15:04:05"

func parseSQLiteTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	return time.ParseInLocation(sqliteTimeLayout, s, time.UTC)
}

func copyUsers(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, name, created_at FROM users ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id int64
		var name, createdAt string
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			return err
		}
		ts, err := parseSQLiteTime(createdAt)
		if err != nil {
			return fmt.Errorf("users.created_at %q: %w", createdAt, err)
		}
		data = append(data, []any{id, name, ts})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"users"}, []string{"id", "name", "created_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("users: copied %d\n", n)
	return nil
}

func copyDevices(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, user_id, name, key, active, created_at FROM devices ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id, userID, active int64
		var name, key, createdAt string
		if err := rows.Scan(&id, &userID, &name, &key, &active, &createdAt); err != nil {
			return err
		}
		ts, err := parseSQLiteTime(createdAt)
		if err != nil {
			return fmt.Errorf("devices.created_at %q: %w", createdAt, err)
		}
		data = append(data, []any{id, userID, name, key, active != 0, ts})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"devices"}, []string{"id", "user_id", "name", "key", "active", "created_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("devices: copied %d\n", n)
	return nil
}

func copyTrafficStats(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, device_id, hour, bytes_in, bytes_out, connections FROM traffic_stats ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id, deviceID, bIn, bOut, conns int64
		var hour string
		if err := rows.Scan(&id, &deviceID, &hour, &bIn, &bOut, &conns); err != nil {
			return err
		}
		ts, err := parseSQLiteTime(hour)
		if err != nil {
			return fmt.Errorf("traffic_stats.hour %q: %w", hour, err)
		}
		data = append(data, []any{id, deviceID, ts, bIn, bOut, conns})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"traffic_stats"}, []string{"id", "device_id", "hour", "bytes_in", "bytes_out", "connections"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("traffic_stats: copied %d\n", n)
	return nil
}

func copyChangelog(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, title, description, type, created_at FROM changelog`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id, title, description, typ, createdAt string
		if err := rows.Scan(&id, &title, &description, &typ, &createdAt); err != nil {
			return err
		}
		data = append(data, []any{id, title, description, typ, createdAt})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"changelog"}, []string{"id", "title", "description", "type", "created_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("changelog: copied %d\n", n)
	return nil
}

func copyLogs(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, level, message, created_at FROM logs ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id int64
		var level, message, createdAt string
		if err := rows.Scan(&id, &level, &message, &createdAt); err != nil {
			return err
		}
		ts, err := parseSQLiteTime(createdAt)
		if err != nil {
			return fmt.Errorf("logs.created_at %q: %w", createdAt, err)
		}
		data = append(data, []any{id, level, message, ts})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"logs"}, []string{"id", "level", "message", "created_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("logs: copied %d\n", n)
	return nil
}

func copySites(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT id, slug, label, primary_domain, approved, created_by_user_id, created_at FROM sites ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var id, approved int64
		var slug, label, primaryDomain, createdAt string
		var createdBy sql.NullInt64
		if err := rows.Scan(&id, &slug, &label, &primaryDomain, &approved, &createdBy, &createdAt); err != nil {
			return err
		}
		ts, err := parseSQLiteTime(createdAt)
		if err != nil {
			return fmt.Errorf("sites.created_at %q: %w", createdAt, err)
		}
		var createdByV any = nil
		if createdBy.Valid {
			createdByV = createdBy.Int64
		}
		data = append(data, []any{id, slug, label, primaryDomain, approved != 0, createdByV, ts})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"sites"}, []string{"id", "slug", "label", "primary_domain", "approved", "created_by_user_id", "created_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("sites: copied %d\n", n)
	return nil
}

func copySiteDomains(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT site_id, domain, is_primary FROM site_domains`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var siteID, isPrimary int64
		var domain string
		if err := rows.Scan(&siteID, &domain, &isPrimary); err != nil {
			return err
		}
		data = append(data, []any{siteID, domain, isPrimary != 0})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"site_domains"}, []string{"site_id", "domain", "is_primary"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("site_domains: copied %d\n", n)
	return nil
}

func copySiteIPs(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT site_id, cidr FROM site_ips`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var siteID int64
		var cidr string
		if err := rows.Scan(&siteID, &cidr); err != nil {
			return err
		}
		data = append(data, []any{siteID, cidr})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"site_ips"}, []string{"site_id", "cidr"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("site_ips: copied %d\n", n)
	return nil
}

func copyUserSites(ctx context.Context, src *sql.DB, tx pgx.Tx) error {
	rows, err := src.QueryContext(ctx, `SELECT user_id, site_id, enabled, updated_at FROM user_sites`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var data [][]any
	for rows.Next() {
		var userID, siteID, enabled, updatedAt int64
		if err := rows.Scan(&userID, &siteID, &enabled, &updatedAt); err != nil {
			return err
		}
		data = append(data, []any{userID, siteID, enabled != 0, updatedAt})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	n, err := tx.CopyFrom(ctx, pgx.Identifier{"user_sites"}, []string{"user_id", "site_id", "enabled", "updated_at"}, pgx.CopyFromRows(data))
	if err != nil {
		return err
	}
	fmt.Printf("user_sites: copied %d\n", n)
	return nil
}

func advanceSequences(ctx context.Context, tx pgx.Tx) error {
	tables := []string{"users", "devices", "traffic_stats", "logs", "sites"}
	for _, t := range tables {
		q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s','id'), COALESCE((SELECT MAX(id) FROM %s), 1), (SELECT COUNT(*) FROM %s) > 0)`, t, t, t)
		if _, err := tx.Exec(ctx, q); err != nil {
			return fmt.Errorf("setval %s: %w", t, err)
		}
	}
	return nil
}
