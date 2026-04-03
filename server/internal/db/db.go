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
CREATE TABLE IF NOT EXISTS changelog (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    version TEXT NOT NULL UNIQUE,
    date    TEXT NOT NULL,
    changes TEXT NOT NULL
);
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
	sqlDB.Exec(`ALTER TABLE devices ADD COLUMN machine_id TEXT DEFAULT ''`)

	d := &DB{sql: sqlDB}
	d.seedChangelog()
	return d, nil
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

// GetDeviceMachineID returns the stored machine_id for a device.
func (d *DB) GetDeviceMachineID(deviceID int) (string, error) {
	var mid string
	err := d.sql.QueryRow(`SELECT COALESCE(machine_id, '') FROM devices WHERE id = ?`, deviceID).Scan(&mid)
	return mid, err
}

// SetDeviceMachineID stores the machine_id for a device (first-time binding).
func (d *DB) SetDeviceMachineID(deviceID int, machineID string) error {
	_, err := d.sql.Exec(`UPDATE devices SET machine_id = ? WHERE id = ?`, machineID, deviceID)
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

// ---- Changelog ----

type ChangelogEntry struct {
	ID      int    `json:"id"`
	Version string `json:"version"`
	Date    string `json:"date"`
	Changes string `json:"changes"`
}

func (d *DB) GetChangelog(page, perPage int) ([]ChangelogEntry, int, error) {
	var total int
	d.sql.QueryRow(`SELECT COUNT(*) FROM changelog`).Scan(&total)

	offset := (page - 1) * perPage
	rows, err := d.sql.Query(
		`SELECT id, version, date, changes FROM changelog ORDER BY id DESC LIMIT ? OFFSET ?`,
		perPage, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		if err := rows.Scan(&e.ID, &e.Version, &e.Date, &e.Changes); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, nil
}

func (d *DB) seedChangelog() {
	var count int
	d.sql.QueryRow(`SELECT COUNT(*) FROM changelog`).Scan(&count)
	if count > 0 {
		return
	}

	releases := []struct {
		version, date, changes string
	}{
		{"v1.22.0", "2026-04-04", "UDP транспорт с шифрованием XChaCha20-Poly1305 и обменом ключами X25519\nМаскировка под QUIC трафик для обхода DPI\nМультиплексирование потоков через единый UDP канал\nАвтовыбор транспорта: UDP с фолбеком на TLS\nПереключение транспорта в настройках клиента (Auto / UDP / TLS)\nИндикатор активного транспорта в статусбаре"},
		{"v1.21.0", "2026-04-03", "Переход на GitHub Releases для автообновления (вместо VPS)\nУлучшенная страница релизов в админке с иконками платформ и счётчиком скачиваний"},
		{"v1.20.0", "2026-04-02", "Вкладка «Releases» в админ-панели с загрузкой данных с GitHub API\nАвторизация на SPA страницах админки"},
		{"v1.19.1", "2026-03-30", "Убрана сборка для Intel macOS — только Apple Silicon (arm64)"},
		{"v1.19.0", "2026-03-28", "Миграция на новый VPS — Timeweb NL (4 CPU, 8 GB RAM, 1 Gbps)\nЗащита от запуска нескольких экземпляров клиента\nУлучшения интерфейса"},
		{"v1.18.5", "2026-03-25", "SVG иконки приложений и сайтов\nДобавлен Telegram Web в список сайтов для проксирования"},
		{"v1.18.4", "2026-03-24", "Обновлён список сайтов: добавлены Claude и YouTrack"},
		{"v1.18.3", "2026-03-22", "Автоскрытие ошибок в интерфейсе через 15 секунд\nФикс: lock-device больше не перезаписывает machine_id"},
		{"v1.18.2", "2026-03-21", "Анимированная кнопка Connecting / Reconnecting\nУлучшенный стиль состояния переподключения"},
		{"v1.18.1", "2026-03-20", "Отключение и сброс ключа при отказе по Machine ID"},
		{"v1.18.0", "2026-03-19", "Привязка устройств к железу (hardware fingerprint)\nОдин ключ = одна машина, попытка использовать на другой — отказ"},
		{"v1.17.1", "2026-03-17", "Ограничение heap Go до 512 МБ для стабильности на VPS\nУменьшение TCP буферов до 64 КБ"},
		{"v1.17.0", "2026-03-16", "Raw UDP bypass — обход gVisor для UDP трафика приложений\nNAT таблица с автоочисткой для маршрутизации ответов"},
		{"v1.16.5", "2026-03-14", "Ограничение TCP буферов gVisor до 128 КБ — предотвращает автотюнинг до 4 МБ на соединение"},
		{"v1.16.4", "2026-03-13", "Уменьшен TCP idle timeout с 5 до 2 минут"},
		{"v1.16.3", "2026-03-12", "TCP idle timeout предотвращает утечку горутин и памяти на Windows"},
		{"v1.16.2", "2026-03-11", "Убран лишний WriteConnect из proxyUDP — сервер его не читал"},
		{"v1.16.1", "2026-03-10", "Показ количества TLS/raw соединений по устройствам в админке"},
		{"v1.16.0", "2026-03-09", "Обнаружение установленных приложений Windows через реестр\nУлучшенный поиск процессов Windows с кешированием"},
		{"v1.15.9", "2026-03-07", "Фикс IP_UNICAST_IF на Windows — корректная передача raw 4-байт значения"},
		{"v1.15.8", "2026-03-06", "Обход UDP для голосовых приложений: Discord, Telegram, Slack, Zoom, Teams"},
		{"v1.15.7", "2026-03-05", "Обход маршрутов через физический интерфейс на Windows"},
		{"v1.15.6", "2026-03-04", "Оптимизация TUN на Windows — кеш поиска процессов"},
		{"v1.15.5", "2026-03-03", "Фикс установки обновлений на Windows (EBUSY при замене файлов)"},
		{"v1.15.4", "2026-03-02", "DNS маршруты через шлюз на Windows для корректного TUN bypass"},
		{"v1.15.3", "2026-03-01", "Клик по всей строке приложений и чекбоксам сайтов"},
		{"v1.15.2", "2026-02-28", "Принудительное обновление PAC при изменении списка сайтов"},
		{"v1.15.1", "2026-02-27", "Расширенные домены для проксирования сайтов\nДобавлены заблокированные сайты в список"},
		{"v1.15.0", "2026-02-26", "Обход маршрутов через физический интерфейс на macOS (IP_BOUND_IF)"},
		{"v1.14.7", "2026-02-24", "Фикс сборки: стабы CachePhysicalInterface для Linux и Windows"},
		{"v1.14.6", "2026-02-23", "Весь трафик браузеров принудительно через SOCKS5 при активном TUN"},
		{"v1.14.5", "2026-02-22", "DNS серверы через шлюз для обхода TUN"},
		{"v1.14.4", "2026-02-21", "DNS (UDP 53) всегда обходит TUN для работы системного резолвера"},
		{"v1.14.3", "2026-02-20", "Браузеры используют SOCKS5/PAC вместо TUN — избегаем проблем с QUIC"},
		{"v1.14.2", "2026-02-19", "Фикс обнаружения Claude Code в списке приложений\nNo-cache заголовки для PAC файла"},
		{"v1.14.1", "2026-02-18", "Перезапуск TUN при повторном запуске приложения\nАвто-переподключение при потере связи\nСчётчик аптайма"},
		{"v1.14.0", "2026-02-17", "Сброс активных соединений при обновлении правил или PAC"},
		{"v1.13.0", "2026-02-15", "Проксирование по сайтам через PAC файл\nЕдиный переключатель «Браузеры» для SOCKS5\nПереключение режимов в рантайме"},
		{"v1.12.0", "2026-02-10", "TUN прозрачный прокси с gVisor netstack\nHelper для управления TUN устройством (macOS + Windows)\nPer-app правила маршрутизации\nБлокировка QUIC (UDP 443) для фолбека на TCP\nHybrid режим: TUN для приложений + SOCKS5 для браузеров"},
		{"v1.5.0", "2026-01-20", "Группировка активных соединений по устройствам\nNSIS установщик для Windows\nКнопка проверки обновлений"},
		{"v1.4.0", "2026-01-18", "PAC через HTTP API daemon вместо file:// URL"},
		{"v1.3.0", "2026-01-16", "Обновление настроек прокси Windows через InternetSetOption"},
		{"v1.2.0", "2026-01-14", "Динамическая лендинг страница с версионными ссылками"},
		{"v1.1.0", "2026-01-12", "Автообновление с VPS вместо GitHub Releases\nPAC файл вместо реестра для Windows SOCKS5 прокси"},
		{"v1.0.0", "2026-01-10", "Первый релиз SmurovProxy\nElectron клиент с управлением daemon и tray\nSOCKS5 прокси через TLS на порт 443\nАдмин-панель: пользователи, устройства, статистика трафика\nМультиплексирование прокси и HTTP на одном порту\nАвтообновление через GitHub Releases"},
	}

	for _, r := range releases {
		d.sql.Exec(`INSERT OR IGNORE INTO changelog (version, date, changes) VALUES (?, ?, ?)`,
			r.version, r.date, r.changes)
	}
}
