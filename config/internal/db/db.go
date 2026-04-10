package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	db *sql.DB
}

type Notification struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Message   string          `json:"message,omitempty"`
	Action    json.RawMessage `json:"action,omitempty"`
	Active    bool            `json:"active"`
	BetaOnly  bool            `json:"beta_only"`
	CreatedAt string          `json:"created_at"`
	ExpiresAt string          `json:"expires_at,omitempty"`
}

type Delivery struct {
	DeviceKey   string `json:"device_key"`
	DeliveredAt string `json:"delivered_at"`
}

type ServiceConfig struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite3", path+"?_journal=WAL&_fk=on")
	if err != nil {
		return nil, err
	}
	if err := migrate(d); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{db: d}, nil
}

func migrate(d *sql.DB) error {
	_, err := d.Exec(`
		CREATE TABLE IF NOT EXISTS notifications (
			id         TEXT PRIMARY KEY,
			type       TEXT NOT NULL CHECK(type IN ('update','migration','maintenance','info')),
			title      TEXT NOT NULL,
			message    TEXT,
			action     TEXT,
			active     INTEGER NOT NULL DEFAULT 1,
			beta_only  INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS service_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
		INSERT OR IGNORE INTO service_config (key, value) VALUES
			('proxy_server', '95.181.162.242:443'),
			('relay_url', ''),
			('config_url', '');
		CREATE TABLE IF NOT EXISTS device_seen (
			device_key    TEXT PRIMARY KEY,
			first_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE IF NOT EXISTS notification_deliveries (
			notification_id TEXT NOT NULL,
			device_key      TEXT NOT NULL,
			delivered_at    TEXT NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (notification_id, device_key)
		);
	`)
	if err != nil {
		return err
	}
	// Migrate: add beta_only column if missing (existing DBs)
	d.Exec(`ALTER TABLE notifications ADD COLUMN beta_only INTEGER NOT NULL DEFAULT 0`)
	d.Exec(`ALTER TABLE notifications ADD COLUMN expires_at TEXT`)
	return nil
}

func (d *DB) Close() { d.db.Close() }

// --- Notifications ---

func (d *DB) ListNotifications() ([]Notification, error) {
	rows, err := d.db.Query(`SELECT id, type, title, message, action, active, beta_only, created_at, expires_at FROM notifications ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var msg, act, exp sql.NullString
		var active, betaOnly int
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &msg, &act, &active, &betaOnly, &n.CreatedAt, &exp); err != nil {
			return nil, err
		}
		n.Message = msg.String
		if act.Valid {
			n.Action = json.RawMessage(act.String)
		}
		n.Active = active == 1
		n.BetaOnly = betaOnly == 1
		n.ExpiresAt = exp.String
		out = append(out, n)
	}
	return out, nil
}

func (d *DB) ActiveNotifications() ([]Notification, error) {
	all, err := d.ListNotifications()
	if err != nil {
		return nil, err
	}
	var out []Notification
	for _, n := range all {
		if n.Active {
			out = append(out, n)
		}
	}
	return out, nil
}

func (d *DB) CreateNotification(typ, title, message string, action json.RawMessage, betaOnly bool, expiresAt string) (Notification, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	var actStr *string
	if len(action) > 0 {
		s := string(action)
		actStr = &s
	}
	bo := 0
	if betaOnly {
		bo = 1
	}
	var expPtr *string
	if expiresAt != "" {
		expPtr = &expiresAt
	}
	_, err := d.db.Exec(`INSERT INTO notifications (id, type, title, message, action, beta_only, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, typ, title, message, actStr, bo, now, expPtr)
	if err != nil {
		return Notification{}, err
	}
	return Notification{ID: id, Type: typ, Title: title, Message: message, Action: action, Active: true, BetaOnly: betaOnly, CreatedAt: now, ExpiresAt: expiresAt}, nil
}

func (d *DB) DeleteNotification(id string) error {
	res, err := d.db.Exec(`DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

func (d *DB) UpdateNotification(id string, active *bool, title, message *string) error {
	if active != nil {
		v := 0
		if *active {
			v = 1
		}
		if _, err := d.db.Exec(`UPDATE notifications SET active = ? WHERE id = ?`, v, id); err != nil {
			return err
		}
	}
	if title != nil {
		if _, err := d.db.Exec(`UPDATE notifications SET title = ? WHERE id = ?`, *title, id); err != nil {
			return err
		}
	}
	if message != nil {
		if _, err := d.db.Exec(`UPDATE notifications SET message = ? WHERE id = ?`, *message, id); err != nil {
			return err
		}
	}
	return nil
}

// --- Service Config ---

func (d *DB) GetServiceConfig() (map[string]string, error) {
	rows, err := d.db.Query(`SELECT key, value FROM service_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, nil
}

func (d *DB) SetServiceConfig(key, value string) error {
	_, err := d.db.Exec(`INSERT INTO service_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// --- Device Seen & Delivery Tracking ---

func (d *DB) RecordDeviceSeen(deviceKey string) (string, error) {
	d.db.Exec(`INSERT OR IGNORE INTO device_seen (device_key) VALUES (?)`, deviceKey)
	var firstSeen string
	err := d.db.QueryRow(`SELECT first_seen_at FROM device_seen WHERE device_key = ?`, deviceKey).Scan(&firstSeen)
	return firstSeen, err
}

func (d *DB) RecordDeliveries(notifIDs []string, deviceKey string) {
	for _, id := range notifIDs {
		d.db.Exec(`INSERT OR IGNORE INTO notification_deliveries (notification_id, device_key) VALUES (?, ?)`, id, deviceKey)
	}
}

func (d *DB) GetDeliveries(notifID string) ([]Delivery, error) {
	rows, err := d.db.Query(`SELECT device_key, delivered_at FROM notification_deliveries WHERE notification_id = ? ORDER BY delivered_at`, notifID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var dl Delivery
		if err := rows.Scan(&dl.DeviceKey, &dl.DeliveredAt); err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, nil
}

func (d *DB) DeliveryCount(notifID string) int {
	var count int
	d.db.QueryRow(`SELECT COUNT(*) FROM notification_deliveries WHERE notification_id = ?`, notifID).Scan(&count)
	return count
}

func (d *DB) FilteredNotifications(firstSeenAt string) ([]Notification, error) {
	rows, err := d.db.Query(`
		SELECT id, type, title, message, action, active, beta_only, created_at, expires_at
		FROM notifications
		WHERE active = 1
		  AND created_at > ?
		  AND (expires_at IS NULL OR expires_at > datetime('now'))
		ORDER BY created_at DESC`,
		firstSeenAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var all []Notification
	for rows.Next() {
		var n Notification
		var msg, act, exp sql.NullString
		var active, betaOnly int
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &msg, &act, &active, &betaOnly, &n.CreatedAt, &exp); err != nil {
			return nil, err
		}
		n.Message = msg.String
		if act.Valid {
			n.Action = json.RawMessage(act.String)
		}
		n.Active = active == 1
		n.BetaOnly = betaOnly == 1
		n.ExpiresAt = exp.String
		all = append(all, n)
	}

	// Deduplicate: for update/maintenance/migration keep only latest (first in DESC order)
	seen := map[string]bool{}
	var out []Notification
	for _, n := range all {
		if n.Type != "info" {
			if seen[n.Type] {
				continue
			}
			seen[n.Type] = true
		}
		out = append(out, n)
	}
	return out, nil
}
