package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"proxyness/pkg/auth"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// DB wraps a Postgres connection pool.
type DB struct {
	sql   *sql.DB
	cache *deviceCache
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

// Open connects to Postgres using the given URL (e.g.
// postgres://user:pw@host:5432/dbname?sslmode=disable). The schema is expected
// to already exist — this process does not create or migrate tables.
func Open(url string) (*DB, error) {
	sqlDB, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	d := &DB{sql: sqlDB, cache: newDeviceCache()}

	if err := d.SeedSitesIfEmpty(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("seed sites: %w", err)
	}

	d.syncChangelog()
	d.cleanOldLogs()
	return d, nil
}

// Close closes the database connection.
func (d *DB) Close() {
	d.sql.Close()
}

// SQL returns the underlying *sql.DB. Used by handlers that need to
// drive a transaction manually.
func (d *DB) SQL() *sql.DB {
	return d.sql
}

// ---- Users ----

// CreateUser inserts a new user and returns the created record.
func (d *DB) CreateUser(name string) (User, error) {
	var u User
	err := d.sql.QueryRow(
		`INSERT INTO users (name) VALUES ($1) RETURNING id, name, created_at`,
		name,
	).Scan(&u.ID, &u.Name, &u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
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
		if err := rows.Scan(&u.ID, &u.Name, &u.CreatedAt, &u.DeviceCount); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// DeleteUser deletes a user by ID (devices are cascade-deleted).
func (d *DB) DeleteUser(id int) error {
	keys, _ := d.userDeviceKeys(id)
	if _, err := d.sql.Exec(`DELETE FROM users WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	for _, k := range keys {
		d.cache.invalidate(k)
	}
	return nil
}

func (d *DB) userDeviceKeys(userID int) ([]string, error) {
	rows, err := d.sql.Query(`SELECT key FROM devices WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
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

// ---- Devices ----

// CreateDevice creates a new device for the given user with an auto-generated key.
func (d *DB) CreateDevice(userID int, name string) (Device, error) {
	key := auth.GenerateKey()
	var id int
	err := d.sql.QueryRow(
		`INSERT INTO devices (user_id, name, key) VALUES ($1, $2, $3) RETURNING id`,
		userID, name, key,
	).Scan(&id)
	if err != nil {
		return Device{}, fmt.Errorf("create device: %w", err)
	}
	return d.deviceByID(id)
}

// ListDevices returns all devices for the given user.
func (d *DB) ListDevices(userID int) ([]Device, error) {
	rows, err := d.sql.Query(`
		SELECT id, user_id, name, key, active, client_version, created_at
		FROM devices
		WHERE user_id = $1
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
	var key string
	d.sql.QueryRow(`SELECT key FROM devices WHERE id = $1`, id).Scan(&key)
	if _, err := d.sql.Exec(`UPDATE devices SET active = $1 WHERE id = $2`, active, id); err != nil {
		return fmt.Errorf("set device active: %w", err)
	}
	if key != "" {
		d.cache.invalidate(key)
	}
	return nil
}

// DeleteDevice deletes a device by ID (traffic stats are cascade-deleted).
func (d *DB) DeleteDevice(id int) error {
	var key string
	d.sql.QueryRow(`SELECT key FROM devices WHERE id = $1`, id).Scan(&key)
	if _, err := d.sql.Exec(`DELETE FROM devices WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if key != "" {
		d.cache.invalidate(key)
	}
	return nil
}

// GetActiveKeys returns the keys of all active devices.
func (d *DB) GetActiveKeys() ([]string, error) {
	rows, err := d.sql.Query(`SELECT key FROM devices WHERE active = TRUE`)
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
// Results (including not-found) are cached for deviceCacheTTL to avoid the
// 85ms WG round-trip on Timeweb's hot path for every new connection.
func (d *DB) GetDeviceByKey(key string) (Device, error) {
	if dev, err, ok := d.cache.get(key); ok {
		return dev, err
	}
	dev, err := d.getDeviceByKeyDB(key)
	d.cache.put(key, dev, err)
	return dev, err
}

func (d *DB) getDeviceByKeyDB(key string) (Device, error) {
	row := d.sql.QueryRow(`
		SELECT d.id, d.user_id, u.name, d.name, d.key, d.active, d.client_version, d.created_at
		FROM devices d
		JOIN users u ON u.id = d.user_id
		WHERE d.key = $1
	`, key)

	var dev Device
	err := row.Scan(&dev.ID, &dev.UserID, &dev.UserName, &dev.Name, &dev.Key, &dev.Active, &dev.Version, &dev.CreatedAt)
	if err == sql.ErrNoRows {
		return Device{}, fmt.Errorf("device not found for key %q", key)
	}
	if err != nil {
		return Device{}, fmt.Errorf("get device by key: %w", err)
	}
	return dev, nil
}

// ---- Traffic ----

// RecordTraffic records or accumulates traffic stats for a device/hour bucket.
// On conflict with the (device_id, hour) unique index, the new values are
// SUMMED into the existing row — this matches the prior SQLite behaviour
// (ON CONFLICT ... DO UPDATE SET bytes_in = bytes_in + excluded.bytes_in).
func (d *DB) RecordTraffic(deviceID int, hour time.Time, bytesIn, bytesOut int64, connections int) error {
	h := hour.UTC().Truncate(time.Hour)
	_, err := d.sql.Exec(`
		INSERT INTO traffic_stats (device_id, hour, bytes_in, bytes_out, connections)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (device_id, hour) DO UPDATE SET
			bytes_in    = traffic_stats.bytes_in    + EXCLUDED.bytes_in,
			bytes_out   = traffic_stats.bytes_out   + EXCLUDED.bytes_out,
			connections = traffic_stats.connections + EXCLUDED.connections
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
		WHERE t.hour >= $1
		GROUP BY d.id, d.name, u.name
		ORDER BY d.id
	`, since)
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
	today := time.Now().UTC().Truncate(24 * time.Hour)

	var ov Overview
	if err := d.sql.QueryRow(`
		SELECT COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0)
		FROM traffic_stats
		WHERE hour >= $1
	`, today).Scan(&ov.TotalBytesIn, &ov.TotalBytesOut); err != nil {
		return Overview{}, fmt.Errorf("overview traffic: %w", err)
	}

	if err := d.sql.QueryRow(`SELECT COUNT(*) FROM devices WHERE active = TRUE`).Scan(&ov.TotalDevices); err != nil {
		return Overview{}, fmt.Errorf("overview devices: %w", err)
	}

	return ov, nil
}

// GetTrafficByDay returns daily aggregated traffic for a device over the last N days.
func (d *DB) GetTrafficByDay(deviceID int, days int) ([]map[string]interface{}, error) {
	since := time.Now().UTC().AddDate(0, 0, -days)

	rows, err := d.sql.Query(`
		SELECT to_char(hour::date, 'YYYY-MM-DD') AS day,
		       SUM(bytes_in), SUM(bytes_out), SUM(connections)
		FROM traffic_stats
		WHERE device_id = $1 AND hour >= $2
		GROUP BY hour::date
		ORDER BY hour::date
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
		FROM devices WHERE id = $1
	`, id)
	return scanDeviceRow(row)
}

// UpdateDeviceVersion updates the client_version for a device identified by key.
func (d *DB) UpdateDeviceVersion(key string, version string) error {
	_, err := d.sql.Exec(`UPDATE devices SET client_version = $1 WHERE key = $2`, version, key)
	if err == nil {
		d.cache.invalidate(key)
	}
	return err
}

// GetDeviceMachineID returns the stored machine_id for a device.
func (d *DB) GetDeviceMachineID(deviceID int) (string, error) {
	var mid string
	err := d.sql.QueryRow(`SELECT COALESCE(machine_id, '') FROM devices WHERE id = $1`, deviceID).Scan(&mid)
	return mid, err
}

// SetDeviceMachineID stores the machine_id for a device (first-time binding).
func (d *DB) SetDeviceMachineID(deviceID int, machineID string) error {
	var key string
	d.sql.QueryRow(`SELECT key FROM devices WHERE id = $1`, deviceID).Scan(&key)
	_, err := d.sql.Exec(`UPDATE devices SET machine_id = $1 WHERE id = $2`, machineID, deviceID)
	if err == nil && key != "" {
		d.cache.invalidate(key)
	}
	return err
}

func scanDeviceRow(row *sql.Row) (Device, error) {
	var dev Device
	if err := row.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Key, &dev.Active, &dev.Version, &dev.CreatedAt); err != nil {
		return Device{}, fmt.Errorf("scan device: %w", err)
	}
	return dev, nil
}

func scanDevice(rows *sql.Rows) (Device, error) {
	var dev Device
	if err := rows.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Key, &dev.Active, &dev.Version, &dev.CreatedAt); err != nil {
		return Device{}, fmt.Errorf("scan device row: %w", err)
	}
	return dev, nil
}

// ---- Changelog ----

type ChangelogEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	CreatedAt   string `json:"createdAt"`
}

func (d *DB) GetChangelog(page, perPage int) ([]ChangelogEntry, int, error) {
	var total int
	d.sql.QueryRow(`SELECT COUNT(*) FROM changelog`).Scan(&total)

	offset := (page - 1) * perPage
	rows, err := d.sql.Query(
		`SELECT id, title, COALESCE(description,''), type, created_at FROM changelog ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		perPage, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		if err := rows.Scan(&e.ID, &e.Title, &e.Description, &e.Type, &e.CreatedAt); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, nil
}

func (d *DB) GetChangelogUnseenCount(since string) (int, error) {
	var count int
	var err error
	if since == "" {
		err = d.sql.QueryRow(`SELECT COUNT(*) FROM changelog`).Scan(&count)
	} else {
		err = d.sql.QueryRow(`SELECT COUNT(*) FROM changelog WHERE created_at > $1`, since).Scan(&count)
	}
	return count, err
}

// ---- Logs ----

// LogEntry represents a server log line.
type LogEntry struct {
	ID        int    `json:"id"`
	Level     string `json:"level"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// WriteLog inserts a log entry.
func (d *DB) WriteLog(level, message string) {
	d.sql.Exec(`INSERT INTO logs (level, message) VALUES ($1, $2)`, level, message)
}

// GetLogs returns recent log entries. limit=0 defaults to 200.
func (d *DB) GetLogs(limit, offset int, level string) ([]LogEntry, int, error) {
	if limit <= 0 {
		limit = 200
	}

	where := ""
	var args []interface{}
	if level != "" {
		where = " WHERE level = $1"
		args = append(args, level)
	}

	var total int
	d.sql.QueryRow(`SELECT COUNT(*) FROM logs`+where, args...).Scan(&total)

	// Build query with correctly numbered placeholders for LIMIT/OFFSET.
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	query := fmt.Sprintf(`SELECT id, level, message, created_at FROM logs%s ORDER BY id DESC LIMIT $%d OFFSET $%d`, where, limitPos, offsetPos)
	args = append(args, limit, offset)
	rows, err := d.sql.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var createdAt time.Time
		if err := rows.Scan(&e.ID, &e.Level, &e.Message, &createdAt); err != nil {
			return nil, 0, err
		}
		e.CreatedAt = createdAt.Format(time.RFC3339)
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// cleanOldLogs deletes logs older than 7 days.
func (d *DB) cleanOldLogs() {
	cutoff := time.Now().UTC().AddDate(0, 0, -7)
	res, _ := d.sql.Exec(`DELETE FROM logs WHERE created_at < $1`, cutoff)
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("[db] cleaned %d old log entries", n)
	}
}

// DBWriter implements io.Writer for use with log.SetOutput.
// It writes to both the database and stdout.
type DBWriter struct {
	DB *DB
}

func (w *DBWriter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	if msg == "" {
		return len(p), nil
	}
	clean := msg
	if len(msg) > 20 && msg[4] == '/' && msg[7] == '/' && msg[10] == ' ' {
		clean = msg[20:]
	}

	level := "info"
	lower := strings.ToLower(clean)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
		level = "error"
	case strings.Contains(lower, "warn"):
		level = "warn"
	}

	w.DB.WriteLog(level, clean)
	os.Stdout.Write(p)
	return len(p), nil
}

func (d *DB) syncChangelog() {
	data, err := os.ReadFile("changelog.json")
	if err != nil {
		log.Printf("[db] changelog.json not found, skipping sync")
		return
	}

	var entries []ChangelogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("[db] changelog.json parse error: %v", err)
		return
	}

	updated := 0
	for _, e := range entries {
		res, err := d.sql.Exec(
			`INSERT INTO changelog (id, title, description, type, created_at) VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, description=EXCLUDED.description, type=EXCLUDED.type, created_at=EXCLUDED.created_at`,
			e.ID, e.Title, e.Description, e.Type, e.CreatedAt,
		)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			updated++
		}
	}
	if updated > 0 {
		log.Printf("[db] changelog synced: %d entries", updated)
	}
}
