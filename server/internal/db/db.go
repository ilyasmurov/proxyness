package db

import (
	"database/sql"
	"fmt"
	"time"

	"smurov-proxy/pkg/auth"

	_ "modernc.org/sqlite"
)

// DB wraps an SQLite connection.
type DB struct {
	sql *sql.DB
}

// User represents a proxy user.
type User struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	DeviceCount int       `json:"device_count"`
}

// Device represents a client device belonging to a user.
type Device struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Active    bool      `json:"active"`
	Version   string    `json:"version,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// TrafficStat is an aggregated traffic row per device.
type TrafficStat struct {
	DeviceID    int    `json:"device_id"`
	DeviceName  string `json:"device_name"`
	UserName    string `json:"user_name"`
	BytesIn     int64  `json:"bytes_in"`
	BytesOut    int64  `json:"bytes_out"`
	Connections int64  `json:"connections"`
}

// Overview holds today's aggregate totals.
type Overview struct {
	TotalBytesIn      int64 `json:"total_bytes_in"`
	TotalBytesOut     int64 `json:"total_bytes_out"`
	ActiveConnections int   `json:"active_connections"`
	TotalDevices      int   `json:"total_devices"`
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS devices (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    key        TEXT UNIQUE NOT NULL,
    active     INTEGER DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS traffic_stats (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id   INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    hour        TIMESTAMP NOT NULL,
    bytes_in    INTEGER DEFAULT 0,
    bytes_out   INTEGER DEFAULT 0,
    connections INTEGER DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_traffic_device_hour ON traffic_stats(device_id, hour);
`

// Open opens (or creates) the SQLite database at path and runs migrations.
func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Apply PRAGMAs
	if _, err := sqlDB.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;`); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("pragma: %w", err)
	}

	// Run migrations
	if _, err := sqlDB.Exec(schema); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	// Migrations (ignore errors — columns may already exist)
	sqlDB.Exec(`ALTER TABLE devices ADD COLUMN client_version TEXT DEFAULT ''`)

	return &DB{sql: sqlDB}, nil
}

// Close closes the database connection.
func (d *DB) Close() {
	d.sql.Close()
}

// ---- Users ----

// CreateUser inserts a new user and returns the created record.
func (d *DB) CreateUser(name string) (User, error) {
	res, err := d.sql.Exec(`INSERT INTO users (name) VALUES (?)`, name)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	var u User
	row := d.sql.QueryRow(`SELECT id, name, created_at FROM users WHERE id = ?`, id)
	var createdAt string
	if err := row.Scan(&u.ID, &u.Name, &createdAt); err != nil {
		return User{}, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt = parseTime(createdAt)
	return u, nil
}

// ListUsers returns all users with their device counts.
func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.sql.Query(`
		SELECT u.id, u.name, u.created_at, COUNT(d.id) AS device_count
		FROM users u
		LEFT JOIN devices d ON d.user_id = u.id
		GROUP BY u.id
		ORDER BY u.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var createdAt string
		if err := rows.Scan(&u.ID, &u.Name, &createdAt, &u.DeviceCount); err != nil {
			return nil, err
		}
		u.CreatedAt = parseTime(createdAt)
		users = append(users, u)
	}
	return users, rows.Err()
}

// DeleteUser deletes a user by ID (devices are cascade-deleted).
func (d *DB) DeleteUser(id int) error {
	_, err := d.sql.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// ---- Devices ----

// CreateDevice creates a new device for the given user with an auto-generated key.
func (d *DB) CreateDevice(userID int, name string) (Device, error) {
	key := auth.GenerateKey()
	res, err := d.sql.Exec(
		`INSERT INTO devices (user_id, name, key) VALUES (?, ?, ?)`,
		userID, name, key,
	)
	if err != nil {
		return Device{}, fmt.Errorf("create device: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Device{}, err
	}
	return d.deviceByID(int(id))
}

// ListDevices returns all devices for the given user.
func (d *DB) ListDevices(userID int) ([]Device, error) {
	rows, err := d.sql.Query(`
		SELECT id, user_id, name, key, active, client_version, created_at
		FROM devices
		WHERE user_id = ?
		ORDER BY id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()

	var devs []Device
	for rows.Next() {
		dev, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devs = append(devs, dev)
	}
	return devs, rows.Err()
}

// SetDeviceActive enables or disables a device.
func (d *DB) SetDeviceActive(id int, active bool) error {
	val := 0
	if active {
		val = 1
	}
	_, err := d.sql.Exec(`UPDATE devices SET active = ? WHERE id = ?`, val, id)
	if err != nil {
		return fmt.Errorf("set device active: %w", err)
	}
	return nil
}

// DeleteDevice deletes a device by ID (traffic stats are cascade-deleted).
func (d *DB) DeleteDevice(id int) error {
	_, err := d.sql.Exec(`DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	return nil
}

// GetActiveKeys returns the keys of all active devices.
func (d *DB) GetActiveKeys() ([]string, error) {
	rows, err := d.sql.Query(`SELECT key FROM devices WHERE active = 1`)
	if err != nil {
		return nil, fmt.Errorf("get active keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// GetDeviceByKey looks up a device by its key, populating UserName via JOIN.
func (d *DB) GetDeviceByKey(key string) (Device, error) {
	row := d.sql.QueryRow(`
		SELECT d.id, d.user_id, u.name, d.name, d.key, d.active, d.client_version, d.created_at
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.key = ?
	`, key)

	var dev Device
	var activeInt int
	var createdAt string
	err := row.Scan(&dev.ID, &dev.UserID, &dev.UserName, &dev.Name, &dev.Key, &activeInt, &dev.Version, &createdAt)
	if err == sql.ErrNoRows {
		return Device{}, fmt.Errorf("device not found for key %q", key)
	}
	if err != nil {
		return Device{}, fmt.Errorf("get device by key: %w", err)
	}
	dev.Active = activeInt != 0
	dev.CreatedAt = parseTime(createdAt)
	return dev, nil
}

// ---- Traffic ----

// RecordTraffic records or accumulates traffic stats for a device/hour bucket.
func (d *DB) RecordTraffic(deviceID int, hour time.Time, bytesIn, bytesOut int64, connections int) error {
	h := hour.UTC().Format("2006-01-02 15:04:05")
	_, err := d.sql.Exec(`
		INSERT INTO traffic_stats (device_id, hour, bytes_in, bytes_out, connections)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(device_id, hour) DO UPDATE SET
			bytes_in    = bytes_in    + excluded.bytes_in,
			bytes_out   = bytes_out   + excluded.bytes_out,
			connections = connections + excluded.connections
	`, deviceID, h, bytesIn, bytesOut, connections)
	if err != nil {
		return fmt.Errorf("record traffic: %w", err)
	}
	return nil
}

// GetTraffic returns aggregated traffic per device for the given period ("day", "week", "month").
func (d *DB) GetTraffic(period string) ([]TrafficStat, error) {
	var since time.Time
	now := time.Now().UTC()
	switch period {
	case "week":
		since = now.AddDate(0, 0, -7)
	case "month":
		since = now.AddDate(0, -1, 0)
	default: // "day"
		since = now.Truncate(24 * time.Hour)
	}

	rows, err := d.sql.Query(`
		SELECT d.id, d.name, u.name,
		       SUM(t.bytes_in), SUM(t.bytes_out), SUM(t.connections)
		FROM traffic_stats t
		JOIN devices d ON d.id = t.device_id
		JOIN users   u ON u.id = d.user_id
		WHERE t.hour >= ?
		GROUP BY d.id
		ORDER BY d.id
	`, since.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("get traffic: %w", err)
	}
	defer rows.Close()

	var stats []TrafficStat
	for rows.Next() {
		var s TrafficStat
		if err := rows.Scan(&s.DeviceID, &s.DeviceName, &s.UserName,
			&s.BytesIn, &s.BytesOut, &s.Connections); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}

// GetOverview returns today's traffic totals and active device count.
func (d *DB) GetOverview() (Overview, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour).Format("2006-01-02 15:04:05")

	var ov Overview
	row := d.sql.QueryRow(`
		SELECT COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0)
		FROM traffic_stats
		WHERE hour >= ?
	`, today)
	if err := row.Scan(&ov.TotalBytesIn, &ov.TotalBytesOut); err != nil {
		return Overview{}, fmt.Errorf("overview traffic: %w", err)
	}

	row = d.sql.QueryRow(`SELECT COUNT(*) FROM devices WHERE active = 1`)
	if err := row.Scan(&ov.TotalDevices); err != nil {
		return Overview{}, fmt.Errorf("overview devices: %w", err)
	}

	return ov, nil
}

// GetTrafficByDay returns daily aggregated traffic for a device over the last N days.
func (d *DB) GetTrafficByDay(deviceID int, days int) ([]map[string]interface{}, error) {
	since := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02 15:04:05")

	rows, err := d.sql.Query(`
		SELECT DATE(hour) AS day,
		       SUM(bytes_in), SUM(bytes_out), SUM(connections)
		FROM traffic_stats
		WHERE device_id = ? AND hour >= ?
		GROUP BY DATE(hour)
		ORDER BY day
	`, deviceID, since)
	if err != nil {
		return nil, fmt.Errorf("get traffic by day: %w", err)
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var day string
		var bytesIn, bytesOut, connections int64
		if err := rows.Scan(&day, &bytesIn, &bytesOut, &connections); err != nil {
			return nil, err
		}
		result = append(result, map[string]interface{}{
			"day":         day,
			"bytes_in":    bytesIn,
			"bytes_out":   bytesOut,
			"connections": connections,
		})
	}
	return result, rows.Err()
}

// ---- helpers ----

func (d *DB) deviceByID(id int) (Device, error) {
	row := d.sql.QueryRow(`
		SELECT id, user_id, name, key, active, client_version, created_at
		FROM devices WHERE id = ?
	`, id)
	return scanDeviceRow(row)
}

// UpdateDeviceVersion updates the client_version for a device identified by key.
func (d *DB) UpdateDeviceVersion(key string, version string) error {
	_, err := d.sql.Exec(`UPDATE devices SET client_version = ? WHERE key = ?`, version, key)
	return err
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanDeviceRow(row *sql.Row) (Device, error) {
	var dev Device
	var activeInt int
	var createdAt string
	if err := row.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Key, &activeInt, &dev.Version, &createdAt); err != nil {
		return Device{}, fmt.Errorf("scan device: %w", err)
	}
	dev.Active = activeInt != 0
	dev.CreatedAt = parseTime(createdAt)
	return dev, nil
}

func scanDevice(rows *sql.Rows) (Device, error) {
	var dev Device
	var activeInt int
	var createdAt string
	if err := rows.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Key, &activeInt, &dev.Version, &createdAt); err != nil {
		return Device{}, fmt.Errorf("scan device row: %w", err)
	}
	dev.Active = activeInt != 0
	dev.CreatedAt = parseTime(createdAt)
	return dev, nil
}

// parseTime handles SQLite's CURRENT_TIMESTAMP format "2006-01-02 15:04:05".
func parseTime(s string) time.Time {
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
	}
	for _, f := range formats {
		t, err := time.Parse(f, s)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}
