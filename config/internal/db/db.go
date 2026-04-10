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
	CreatedAt string          `json:"created_at"`
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
	`)
	return err
}

func (d *DB) Close() { d.db.Close() }

// --- Notifications ---

func (d *DB) ListNotifications() ([]Notification, error) {
	rows, err := d.db.Query(`SELECT id, type, title, message, action, active, created_at FROM notifications ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var msg, act sql.NullString
		var active int
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &msg, &act, &active, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Message = msg.String
		if act.Valid {
			n.Action = json.RawMessage(act.String)
		}
		n.Active = active == 1
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

func (d *DB) CreateNotification(typ, title, message string, action json.RawMessage) (Notification, error) {
	id := uuid.New().String()
	now := time.Now().UTC().Format(time.RFC3339)
	var actStr *string
	if len(action) > 0 {
		s := string(action)
		actStr = &s
	}
	_, err := d.db.Exec(`INSERT INTO notifications (id, type, title, message, action, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, typ, title, message, actStr, now)
	if err != nil {
		return Notification{}, err
	}
	return Notification{ID: id, Type: typ, Title: title, Message: message, Action: action, Active: true, CreatedAt: now}, nil
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
