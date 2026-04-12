# Admin Panel + Multi-User + Traffic Stats Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add SQLite-backed multi-user/device management with web admin panel and anonymous traffic statistics.

**Architecture:** Server gets a protocol multiplexer (custom protocol vs HTTP on port 443), SQLite DB for users/devices/traffic, REST API with Basic Auth, React+shadcn/ui admin SPA embedded via `go:embed`. Traffic tracked per-device with hourly aggregation, no destination logging.

**Tech Stack:** Go + modernc.org/sqlite (pure Go), React 19, TypeScript, Vite, shadcn/ui, Tailwind CSS, recharts, react-router-dom

---

**File structure (new/modified):**
```
pkg/auth/auth.go                    — add ValidateAuthMessageMulti
pkg/auth/auth_test.go               — add tests
pkg/proto/proto.go                  — add CountingRelay
pkg/proto/proto_test.go             — add tests
server/internal/db/db.go            — SQLite init + migrations
server/internal/db/db_test.go       — DB tests
server/internal/stats/tracker.go    — in-memory active connections
server/internal/stats/tracker_test.go
server/internal/admin/admin.go      — REST API handlers
server/internal/admin/admin_test.go
server/internal/admin/static.go     — go:embed SPA serving
server/internal/mux/mux.go          — protocol multiplexer
server/internal/mux/mux_test.go
server/cmd/main.go                  — rewrite with new wiring
server/admin-ui/                    — React SPA (shadcn/ui)
Dockerfile                          — multi-stage with Node + Go
.github/workflows/deploy.yml        — update env vars
```

---

## Phase 1: Backend

### Task 1: SQLite DB Package

**Files:**
- Create: `server/internal/db/db.go`
- Create: `server/internal/db/db_test.go`

- [ ] **Step 1: Add SQLite dependency**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy/server
go get modernc.org/sqlite
```

- [ ] **Step 2: Write DB tests**

Create `server/internal/db/db_test.go`:

```go
package db

import (
	"testing"
	"time"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// --- Users ---

func TestCreateUser(t *testing.T) {
	d := testDB(t)
	u, err := d.CreateUser("Alice")
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Alice" || u.ID == 0 {
		t.Fatalf("unexpected user: %+v", u)
	}
}

func TestListUsers(t *testing.T) {
	d := testDB(t)
	d.CreateUser("Alice")
	d.CreateUser("Bob")
	users, err := d.ListUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].DeviceCount != 0 {
		t.Fatalf("expected 0 devices, got %d", users[0].DeviceCount)
	}
}

func TestDeleteUser(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	d.CreateDevice(u.ID, "Phone")
	if err := d.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	users, _ := d.ListUsers()
	if len(users) != 0 {
		t.Fatal("user not deleted")
	}
	devices, _ := d.ListDevices(u.ID)
	if len(devices) != 0 {
		t.Fatal("cascade delete failed")
	}
}

// --- Devices ---

func TestCreateDevice(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, err := d.CreateDevice(u.ID, "MacBook")
	if err != nil {
		t.Fatal(err)
	}
	if dev.Name != "MacBook" || len(dev.Key) != 64 || !dev.Active {
		t.Fatalf("unexpected device: %+v", dev)
	}
}

func TestListDevices(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	d.CreateDevice(u.ID, "MacBook")
	d.CreateDevice(u.ID, "iPhone")
	devs, err := d.ListDevices(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}
}

func TestSetDeviceActive(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, _ := d.CreateDevice(u.ID, "MacBook")
	if err := d.SetDeviceActive(dev.ID, false); err != nil {
		t.Fatal(err)
	}
	devs, _ := d.ListDevices(u.ID)
	if devs[0].Active {
		t.Fatal("device should be inactive")
	}
}

func TestGetActiveKeys(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev1, _ := d.CreateDevice(u.ID, "MacBook")
	dev2, _ := d.CreateDevice(u.ID, "iPhone")
	d.SetDeviceActive(dev2.ID, false)
	keys, err := d.GetActiveKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != dev1.Key {
		t.Fatalf("expected 1 active key, got %v", keys)
	}
}

func TestGetDeviceByKey(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, _ := d.CreateDevice(u.ID, "MacBook")
	found, err := d.GetDeviceByKey(dev.Key)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != dev.ID || found.UserName != "Alice" {
		t.Fatalf("unexpected: %+v", found)
	}
}

func TestDeleteDevice(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, _ := d.CreateDevice(u.ID, "MacBook")
	if err := d.DeleteDevice(dev.ID); err != nil {
		t.Fatal(err)
	}
	devs, _ := d.ListDevices(u.ID)
	if len(devs) != 0 {
		t.Fatal("device not deleted")
	}
}

// --- Traffic Stats ---

func TestRecordTraffic(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, _ := d.CreateDevice(u.ID, "MacBook")
	now := time.Now().Truncate(time.Hour)

	if err := d.RecordTraffic(dev.ID, now, 1000, 2000, 1); err != nil {
		t.Fatal(err)
	}
	// Second call should UPSERT (add to existing)
	if err := d.RecordTraffic(dev.ID, now, 500, 300, 1); err != nil {
		t.Fatal(err)
	}

	stats, err := d.GetTraffic("day")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat row, got %d", len(stats))
	}
	if stats[0].BytesIn != 1500 || stats[0].BytesOut != 2300 || stats[0].Connections != 2 {
		t.Fatalf("unexpected stats: %+v", stats[0])
	}
}

func TestGetOverview(t *testing.T) {
	d := testDB(t)
	u, _ := d.CreateUser("Alice")
	dev, _ := d.CreateDevice(u.ID, "MacBook")
	now := time.Now().Truncate(time.Hour)
	d.RecordTraffic(dev.ID, now, 1000, 2000, 3)

	ov, err := d.GetOverview()
	if err != nil {
		t.Fatal(err)
	}
	if ov.TotalDevices != 1 || ov.TotalBytesIn != 1000 || ov.TotalBytesOut != 2000 {
		t.Fatalf("unexpected overview: %+v", ov)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
cd server && go test ./internal/db/
```

- [ ] **Step 4: Implement DB package**

Create `server/internal/db/db.go`:

```go
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"proxyness/pkg/auth"
)

type DB struct {
	db *sql.DB
}

type User struct {
	ID          int       `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	DeviceCount int       `json:"device_count"`
}

type Device struct {
	ID        int       `json:"id"`
	UserID    int       `json:"user_id"`
	UserName  string    `json:"user_name,omitempty"`
	Name      string    `json:"name"`
	Key       string    `json:"key"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

type TrafficStat struct {
	DeviceID    int    `json:"device_id"`
	DeviceName  string `json:"device_name"`
	UserName    string `json:"user_name"`
	BytesIn     int64  `json:"bytes_in"`
	BytesOut    int64  `json:"bytes_out"`
	Connections int64  `json:"connections"`
}

type Overview struct {
	TotalBytesIn      int64 `json:"total_bytes_in"`
	TotalBytesOut     int64 `json:"total_bytes_out"`
	ActiveConnections int   `json:"active_connections"`
	TotalDevices      int   `json:"total_devices"`
}

func Open(path string) (*DB, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sqlDB.Exec("PRAGMA journal_mode=WAL")
	sqlDB.Exec("PRAGMA foreign_keys=ON")

	d := &DB{db: sqlDB}
	if err := d.migrate(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS devices (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			key TEXT UNIQUE NOT NULL,
			active INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS traffic_stats (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
			hour TIMESTAMP NOT NULL,
			bytes_in INTEGER DEFAULT 0,
			bytes_out INTEGER DEFAULT 0,
			connections INTEGER DEFAULT 0
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_traffic_device_hour ON traffic_stats(device_id, hour);
	`)
	return err
}

// --- Users ---

func (d *DB) CreateUser(name string) (*User, error) {
	res, err := d.db.Exec("INSERT INTO users (name) VALUES (?)", name)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &User{ID: int(id), Name: name, CreatedAt: time.Now()}, nil
}

func (d *DB) ListUsers() ([]User, error) {
	rows, err := d.db.Query(`
		SELECT u.id, u.name, u.created_at, COUNT(d.id) as device_count
		FROM users u LEFT JOIN devices d ON d.user_id = u.id
		GROUP BY u.id ORDER BY u.id
	`)
	if err != nil {
		return nil, err
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
	return users, nil
}

func (d *DB) DeleteUser(id int) error {
	_, err := d.db.Exec("DELETE FROM users WHERE id = ?", id)
	return err
}

// --- Devices ---

func (d *DB) CreateDevice(userID int, name string) (*Device, error) {
	key := auth.GenerateKey()
	res, err := d.db.Exec("INSERT INTO devices (user_id, name, key) VALUES (?, ?, ?)", userID, name, key)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Device{ID: int(id), UserID: userID, Name: name, Key: key, Active: true, CreatedAt: time.Now()}, nil
}

func (d *DB) ListDevices(userID int) ([]Device, error) {
	rows, err := d.db.Query("SELECT id, user_id, name, key, active, created_at FROM devices WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devs []Device
	for rows.Next() {
		var dev Device
		var active int
		if err := rows.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Key, &active, &dev.CreatedAt); err != nil {
			return nil, err
		}
		dev.Active = active == 1
		devs = append(devs, dev)
	}
	return devs, nil
}

func (d *DB) SetDeviceActive(id int, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := d.db.Exec("UPDATE devices SET active = ? WHERE id = ?", v, id)
	return err
}

func (d *DB) DeleteDevice(id int) error {
	_, err := d.db.Exec("DELETE FROM devices WHERE id = ?", id)
	return err
}

func (d *DB) GetActiveKeys() ([]string, error) {
	rows, err := d.db.Query("SELECT key FROM devices WHERE active = 1")
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
	return keys, nil
}

func (d *DB) GetDeviceByKey(key string) (*Device, error) {
	var dev Device
	var active int
	err := d.db.QueryRow(`
		SELECT d.id, d.user_id, u.name, d.name, d.key, d.active, d.created_at
		FROM devices d JOIN users u ON u.id = d.user_id
		WHERE d.key = ?
	`, key).Scan(&dev.ID, &dev.UserID, &dev.UserName, &dev.Name, &dev.Key, &active, &dev.CreatedAt)
	if err != nil {
		return nil, err
	}
	dev.Active = active == 1
	return &dev, nil
}

// --- Traffic Stats ---

func (d *DB) RecordTraffic(deviceID int, hour time.Time, bytesIn, bytesOut, connections int64) error {
	_, err := d.db.Exec(`
		INSERT INTO traffic_stats (device_id, hour, bytes_in, bytes_out, connections)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(device_id, hour) DO UPDATE SET
			bytes_in = bytes_in + excluded.bytes_in,
			bytes_out = bytes_out + excluded.bytes_out,
			connections = connections + excluded.connections
	`, deviceID, hour, bytesIn, bytesOut, connections)
	return err
}

func (d *DB) GetTraffic(period string) ([]TrafficStat, error) {
	var since time.Time
	switch period {
	case "day":
		since = time.Now().Add(-24 * time.Hour)
	case "week":
		since = time.Now().Add(-7 * 24 * time.Hour)
	case "month":
		since = time.Now().Add(-30 * 24 * time.Hour)
	default:
		since = time.Now().Add(-24 * time.Hour)
	}

	rows, err := d.db.Query(`
		SELECT d.id, d.name, u.name,
			COALESCE(SUM(ts.bytes_in), 0), COALESCE(SUM(ts.bytes_out), 0), COALESCE(SUM(ts.connections), 0)
		FROM devices d
		JOIN users u ON u.id = d.user_id
		LEFT JOIN traffic_stats ts ON ts.device_id = d.id AND ts.hour >= ?
		GROUP BY d.id
		ORDER BY COALESCE(SUM(ts.bytes_in), 0) + COALESCE(SUM(ts.bytes_out), 0) DESC
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []TrafficStat
	for rows.Next() {
		var s TrafficStat
		if err := rows.Scan(&s.DeviceID, &s.DeviceName, &s.UserName, &s.BytesIn, &s.BytesOut, &s.Connections); err != nil {
			return nil, err
		}
		stats = append(stats, s)
	}
	return stats, nil
}

func (d *DB) GetOverview() (*Overview, error) {
	ov := &Overview{}
	today := time.Now().Truncate(24 * time.Hour)

	d.db.QueryRow("SELECT COUNT(*) FROM devices WHERE active = 1").Scan(&ov.TotalDevices)
	d.db.QueryRow(`
		SELECT COALESCE(SUM(bytes_in), 0), COALESCE(SUM(bytes_out), 0)
		FROM traffic_stats WHERE hour >= ?
	`, today).Scan(&ov.TotalBytesIn, &ov.TotalBytesOut)

	return ov, nil
}

func (d *DB) GetTrafficByDay(deviceID int, days int) ([]map[string]interface{}, error) {
	rows, err := d.db.Query(`
		SELECT DATE(hour) as day, SUM(bytes_in), SUM(bytes_out), SUM(connections)
		FROM traffic_stats
		WHERE device_id = ? AND hour >= ?
		GROUP BY DATE(hour) ORDER BY day
	`, deviceID, time.Now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var day string
		var bytesIn, bytesOut, conns int64
		if err := rows.Scan(&day, &bytesIn, &bytesOut, &conns); err != nil {
			return nil, err
		}
		result = append(result, map[string]interface{}{
			"day": day, "bytes_in": bytesIn, "bytes_out": bytesOut, "connections": conns,
		})
	}
	return result, nil
}
```

- [ ] **Step 5: Run tests**

```bash
cd server && go test ./internal/db/ -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/db/ server/go.mod server/go.sum
git commit -m "feat: add SQLite DB package with users, devices, traffic stats"
```

---

### Task 2: Multi-Key Auth

**Files:**
- Modify: `pkg/auth/auth.go`
- Modify: `pkg/auth/auth_test.go`

- [ ] **Step 1: Write tests**

Add to `pkg/auth/auth_test.go`:

```go
func TestValidateAuthMessageMulti_Match(t *testing.T) {
	key1 := GenerateKey()
	key2 := GenerateKey()
	key3 := GenerateKey()
	msg, _ := CreateAuthMessage(key2)

	matched, err := ValidateAuthMessageMulti([]string{key1, key2, key3}, msg)
	if err != nil {
		t.Fatalf("expected match, got: %v", err)
	}
	if matched != key2 {
		t.Fatalf("expected key2, got %s", matched)
	}
}

func TestValidateAuthMessageMulti_NoMatch(t *testing.T) {
	key1 := GenerateKey()
	key2 := GenerateKey()
	msg, _ := CreateAuthMessage(GenerateKey()) // different key

	_, err := ValidateAuthMessageMulti([]string{key1, key2}, msg)
	if err == nil {
		t.Fatal("expected error for no matching key")
	}
}

func TestValidateAuthMessageMulti_EmptyKeys(t *testing.T) {
	msg, _ := CreateAuthMessage(GenerateKey())
	_, err := ValidateAuthMessageMulti([]string{}, msg)
	if err == nil {
		t.Fatal("expected error for empty keys")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd pkg && go test ./auth/
```

- [ ] **Step 3: Implement**

Add to `pkg/auth/auth.go`:

```go
// ValidateAuthMessageMulti tries each key and returns the first match.
func ValidateAuthMessageMulti(keys []string, msg []byte) (string, error) {
	if len(keys) == 0 {
		return "", fmt.Errorf("no keys provided")
	}
	// Check format first (cheap checks before trying keys)
	if len(msg) != AuthMsgLen {
		return "", fmt.Errorf("invalid message length: %d", len(msg))
	}
	if msg[0] != Version {
		return "", fmt.Errorf("unsupported version: %d", msg[0])
	}

	for _, key := range keys {
		if err := ValidateAuthMessage(key, msg); err == nil {
			return key, nil
		}
	}
	return "", fmt.Errorf("no matching key found")
}
```

- [ ] **Step 4: Run tests**

```bash
cd pkg && go test ./auth/ -v
```

Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/auth/
git commit -m "feat: add ValidateAuthMessageMulti for multi-device auth"
```

---

### Task 3: CountingRelay

**Files:**
- Modify: `pkg/proto/proto.go`
- Modify: `pkg/proto/proto_test.go`

- [ ] **Step 1: Write test**

Add to `pkg/proto/proto_test.go`:

```go
func TestCountingRelay(t *testing.T) {
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()

	var totalIn, totalOut int64

	go CountingRelay(c2, c3, func(in, out int64) {
		totalIn += in
		totalOut += out
	})

	// c1 → c2 → c3 → c4 (counted as "in" direction)
	go func() {
		c1.Write([]byte("hello"))
		c1.Close()
	}()

	buf := make([]byte, 10)
	n, _ := c4.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q", string(buf[:n]))
	}

	c2.Close()
	c3.Close()
	c4.Close()

	// Give goroutines time to finish
	time.Sleep(50 * time.Millisecond)

	if totalIn == 0 && totalOut == 0 {
		t.Fatal("expected non-zero byte counts")
	}
}
```

Add `"time"` to imports if not present.

- [ ] **Step 2: Implement**

Add to `pkg/proto/proto.go`:

```go
// countingWriter wraps a writer and reports bytes written.
type countingWriter struct {
	dst      net.Conn
	onBytes  func(n int64)
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.dst.Write(p)
	if n > 0 {
		w.onBytes(int64(n))
	}
	return n, err
}

// CountingRelay copies data bidirectionally, calling onBytes(in, out) with cumulative counts.
func CountingRelay(c1, c2 net.Conn, onBytes func(in, out int64)) error {
	errc := make(chan error, 2)

	// c2 → c1 = "in" (from target to client)
	go func() {
		cw := &countingWriter{dst: c1, onBytes: func(n int64) { onBytes(n, 0) }}
		_, err := io.Copy(cw, c2)
		errc <- err
	}()

	// c1 → c2 = "out" (from client to target)
	go func() {
		cw := &countingWriter{dst: c2, onBytes: func(n int64) { onBytes(0, n) }}
		_, err := io.Copy(cw, c1)
		errc <- err
	}()

	err := <-errc
	c1.Close()
	c2.Close()
	<-errc
	return err
}
```

- [ ] **Step 3: Run tests**

```bash
cd pkg && go test ./proto/ -v
```

- [ ] **Step 4: Commit**

```bash
git add pkg/proto/
git commit -m "feat: add CountingRelay for traffic measurement"
```

---

### Task 4: Stats Tracker

**Files:**
- Create: `server/internal/stats/tracker.go`
- Create: `server/internal/stats/tracker_test.go`

- [ ] **Step 1: Write tests**

Create `server/internal/stats/tracker_test.go`:

```go
package stats

import (
	"testing"
	"time"
)

func TestAddRemoveConn(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice")
	conns := tr.Active()
	if len(conns) != 1 {
		t.Fatalf("expected 1, got %d", len(conns))
	}
	if conns[0].DeviceName != "MacBook" || conns[0].UserName != "Alice" {
		t.Fatalf("unexpected: %+v", conns[0])
	}

	info := tr.Remove(id)
	if info == nil {
		t.Fatal("expected conn info")
	}
	if len(tr.Active()) != 0 {
		t.Fatal("expected 0 active")
	}
}

func TestUpdateBytes(t *testing.T) {
	tr := New()
	id := tr.Add(1, "MacBook", "Alice")
	tr.AddBytes(id, 100, 200)
	tr.AddBytes(id, 50, 30)

	conns := tr.Active()
	if conns[0].BytesIn != 150 || conns[0].BytesOut != 230 {
		t.Fatalf("bytes: in=%d out=%d", conns[0].BytesIn, conns[0].BytesOut)
	}

	info := tr.Remove(id)
	if info.BytesIn != 150 || info.BytesOut != 230 {
		t.Fatalf("removed info: in=%d out=%d", info.BytesIn, info.BytesOut)
	}
}

func TestActiveCount(t *testing.T) {
	tr := New()
	tr.Add(1, "A", "U")
	tr.Add(2, "B", "U")
	if tr.ActiveCount() != 2 {
		t.Fatalf("expected 2, got %d", tr.ActiveCount())
	}
}

func TestStartedAt(t *testing.T) {
	tr := New()
	before := time.Now()
	tr.Add(1, "A", "U")
	conns := tr.Active()
	if conns[0].StartedAt.Before(before) {
		t.Fatal("started_at should be >= before")
	}
}
```

- [ ] **Step 2: Implement**

Create `server/internal/stats/tracker.go`:

```go
package stats

import (
	"sync"
	"sync/atomic"
	"time"
)

type ConnInfo struct {
	DeviceID   int       `json:"device_id"`
	DeviceName string    `json:"device_name"`
	UserName   string    `json:"user_name"`
	StartedAt  time.Time `json:"started_at"`
	BytesIn    int64     `json:"bytes_in"`
	BytesOut   int64     `json:"bytes_out"`
}

type Tracker struct {
	mu    sync.RWMutex
	conns map[int64]*ConnInfo
	nextID int64
}

func New() *Tracker {
	return &Tracker{conns: make(map[int64]*ConnInfo)}
}

func (t *Tracker) Add(deviceID int, deviceName, userName string) int64 {
	id := atomic.AddInt64(&t.nextID, 1)
	t.mu.Lock()
	t.conns[id] = &ConnInfo{
		DeviceID:   deviceID,
		DeviceName: deviceName,
		UserName:   userName,
		StartedAt:  time.Now(),
	}
	t.mu.Unlock()
	return id
}

func (t *Tracker) AddBytes(id, bytesIn, bytesOut int64) {
	t.mu.RLock()
	c, ok := t.conns[id]
	t.mu.RUnlock()
	if ok {
		atomic.AddInt64(&c.BytesIn, bytesIn)
		atomic.AddInt64(&c.BytesOut, bytesOut)
	}
}

func (t *Tracker) Remove(id int64) *ConnInfo {
	t.mu.Lock()
	c, ok := t.conns[id]
	if ok {
		delete(t.conns, id)
	}
	t.mu.Unlock()
	if !ok {
		return nil
	}
	// Return a copy with final values
	return &ConnInfo{
		DeviceID:   c.DeviceID,
		DeviceName: c.DeviceName,
		UserName:   c.UserName,
		StartedAt:  c.StartedAt,
		BytesIn:    atomic.LoadInt64(&c.BytesIn),
		BytesOut:   atomic.LoadInt64(&c.BytesOut),
	}
}

func (t *Tracker) Active() []ConnInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]ConnInfo, 0, len(t.conns))
	for _, c := range t.conns {
		result = append(result, ConnInfo{
			DeviceID:   c.DeviceID,
			DeviceName: c.DeviceName,
			UserName:   c.UserName,
			StartedAt:  c.StartedAt,
			BytesIn:    atomic.LoadInt64(&c.BytesIn),
			BytesOut:   atomic.LoadInt64(&c.BytesOut),
		})
	}
	return result
}

func (t *Tracker) ActiveCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.conns)
}
```

- [ ] **Step 3: Run tests**

```bash
cd server && go test ./internal/stats/ -v
```

- [ ] **Step 4: Commit**

```bash
git add server/internal/stats/
git commit -m "feat: add in-memory connection tracker for traffic stats"
```

---

### Task 5: Admin API Handlers

**Files:**
- Create: `server/internal/admin/admin.go`
- Create: `server/internal/admin/admin_test.go`

- [ ] **Step 1: Write tests**

Create `server/internal/admin/admin_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

func setup(t *testing.T) *Handler {
	t.Helper()
	d, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	tr := stats.New()
	return NewHandler(d, tr, "admin", "secret")
}

func authed(req *http.Request) *http.Request {
	req.SetBasicAuth("admin", "secret")
	return req
}

func TestHealthNoAuth(t *testing.T) {
	h := setup(t)
	req := httptest.NewRequest("GET", "/admin/api/stats/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestCreateAndListUsers(t *testing.T) {
	h := setup(t)

	// Create
	req := authed(httptest.NewRequest("POST", "/admin/api/users", strings.NewReader(`{"name":"Alice"}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	// List
	req = authed(httptest.NewRequest("GET", "/admin/api/users", nil))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var users []db.User
	json.NewDecoder(w.Body).Decode(&users)
	if len(users) != 1 || users[0].Name != "Alice" {
		t.Fatalf("unexpected: %+v", users)
	}
}

func TestCreateDeviceReturnsKey(t *testing.T) {
	h := setup(t)

	// Create user
	req := authed(httptest.NewRequest("POST", "/admin/api/users", strings.NewReader(`{"name":"Alice"}`)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var user db.User
	json.NewDecoder(w.Body).Decode(&user)

	// Create device
	req = authed(httptest.NewRequest("POST", "/admin/api/users/1/devices", strings.NewReader(`{"name":"MacBook"}`)))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create device: %d %s", w.Code, w.Body.String())
	}
	var dev db.Device
	json.NewDecoder(w.Body).Decode(&dev)
	if len(dev.Key) != 64 {
		t.Fatalf("expected 64-char key, got %d", len(dev.Key))
	}
}

func TestToggleDevice(t *testing.T) {
	h := setup(t)
	authedPost := func(url, body string) *httptest.ResponseRecorder {
		req := authed(httptest.NewRequest("POST", url, strings.NewReader(body)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	authedPatch := func(url, body string) *httptest.ResponseRecorder {
		req := authed(httptest.NewRequest("PATCH", url, strings.NewReader(body)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	authedPost("/admin/api/users", `{"name":"Alice"}`)
	authedPost("/admin/api/users/1/devices", `{"name":"Mac"}`)

	w := authedPatch("/admin/api/devices/1", `{"active":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle: %d", w.Code)
	}
}

func TestOverview(t *testing.T) {
	h := setup(t)
	req := authed(httptest.NewRequest("GET", "/admin/api/stats/overview", nil))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overview: %d", w.Code)
	}
	var ov db.Overview
	json.NewDecoder(w.Body).Decode(&ov)
}
```

- [ ] **Step 2: Implement**

Create `server/internal/admin/admin.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

type Handler struct {
	db       *db.DB
	tracker  *stats.Tracker
	user     string
	password string
	mux      *http.ServeMux
}

func NewHandler(d *db.DB, tr *stats.Tracker, user, password string) *Handler {
	h := &Handler{db: d, tracker: tr, user: user, password: password}
	mux := http.NewServeMux()

	// Users
	mux.HandleFunc("GET /admin/api/users", h.auth(h.listUsers))
	mux.HandleFunc("POST /admin/api/users", h.auth(h.createUser))
	mux.HandleFunc("DELETE /admin/api/users/{id}", h.auth(h.deleteUser))

	// Devices
	mux.HandleFunc("GET /admin/api/users/{id}/devices", h.auth(h.listDevices))
	mux.HandleFunc("POST /admin/api/users/{id}/devices", h.auth(h.createDevice))
	mux.HandleFunc("PATCH /admin/api/devices/{id}", h.auth(h.toggleDevice))
	mux.HandleFunc("DELETE /admin/api/devices/{id}", h.auth(h.deleteDevice))

	// Stats
	mux.HandleFunc("GET /admin/api/stats/overview", h.auth(h.overview))
	mux.HandleFunc("GET /admin/api/stats/active", h.auth(h.activeConns))
	mux.HandleFunc("GET /admin/api/stats/traffic", h.auth(h.traffic))
	mux.HandleFunc("GET /admin/api/stats/traffic/{deviceId}/daily", h.auth(h.trafficDaily))

	h.mux = mux
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != h.user || pass != h.password {
			w.Header().Set("WWW-Authenticate", `Basic realm="admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func pathID(r *http.Request, name string) int {
	id, _ := strconv.Atoi(r.PathValue(name))
	return id
}

// --- Users ---

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.db.ListUsers()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if users == nil {
		users = []db.User{}
	}
	writeJSON(w, users)
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	user, err := h.db.CreateUser(req.Name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, user)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteUser(pathID(r, "id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

// --- Devices ---

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	devs, err := h.db.ListDevices(pathID(r, "id"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if devs == nil {
		devs = []db.Device{}
	}
	writeJSON(w, devs)
}

func (h *Handler) createDevice(w http.ResponseWriter, r *http.Request) {
	var req struct{ Name string `json:"name"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	dev, err := h.db.CreateDevice(pathID(r, "id"), req.Name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, dev)
}

func (h *Handler) toggleDevice(w http.ResponseWriter, r *http.Request) {
	var req struct{ Active bool `json:"active"` }
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err := h.db.SetDeviceActive(pathID(r, "id"), req.Active); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(200)
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	if err := h.db.DeleteDevice(pathID(r, "id")); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}

// --- Stats ---

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	ov, err := h.db.GetOverview()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ov.ActiveConnections = h.tracker.ActiveCount()
	writeJSON(w, ov)
}

func (h *Handler) activeConns(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.tracker.Active())
}

func (h *Handler) traffic(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = "day"
	}
	stats, err := h.db.GetTraffic(period)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if stats == nil {
		stats = []db.TrafficStat{}
	}
	writeJSON(w, stats)
}

func (h *Handler) trafficDaily(w http.ResponseWriter, r *http.Request) {
	deviceID := pathID(r, "deviceId")
	data, err := h.db.GetTrafficByDay(deviceID, 7)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if data == nil {
		data = []map[string]interface{}{}
	}
	writeJSON(w, data)
}
```

- [ ] **Step 3: Run tests**

```bash
cd server && go test ./internal/admin/ -v
```

- [ ] **Step 4: Commit**

```bash
git add server/internal/admin/
git commit -m "feat: add admin REST API with Basic Auth"
```

---

### Task 6: Protocol Multiplexer

**Files:**
- Create: `server/internal/mux/mux.go`
- Create: `server/internal/mux/mux_test.go`

- [ ] **Step 1: Write tests**

Create `server/internal/mux/mux_test.go`:

```go
package mux

import (
	"bufio"
	"net"
	"net/http"
	"testing"
)

func TestPeekFirstByte_Protocol(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x01, 0x02, 0x03})
	}()

	br := bufio.NewReader(c2)
	b, err := br.Peek(1)
	if err != nil {
		t.Fatal(err)
	}
	if b[0] != 0x01 {
		t.Fatalf("expected 0x01, got 0x%02x", b[0])
	}
}

func TestPeekFirstByte_HTTP(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte("GET / HTTP/1.1\r\n\r\n"))
	}()

	br := bufio.NewReader(c2)
	b, err := br.Peek(1)
	if err != nil {
		t.Fatal(err)
	}
	if IsProxyProtocol(b[0]) {
		t.Fatal("HTTP should not be detected as proxy protocol")
	}
}

func TestIsProxyProtocol(t *testing.T) {
	if !IsProxyProtocol(0x01) {
		t.Fatal("0x01 should be proxy protocol")
	}
	if IsProxyProtocol('G') {
		t.Fatal("'G' should not be proxy protocol")
	}
	if IsProxyProtocol('P') {
		t.Fatal("'P' should not be proxy protocol")
	}
}

func TestPeekConn(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte("hello world"))
	}()

	pc := NewPeekConn(c2)
	first, err := pc.PeekByte()
	if err != nil {
		t.Fatal(err)
	}
	if first != 'h' {
		t.Fatalf("expected 'h', got %c", first)
	}

	// Read should return full data including peeked byte
	buf := make([]byte, 11)
	n, _ := pc.Read(buf)
	if string(buf[:n]) != "hello world" {
		t.Fatalf("got %q", string(buf[:n]))
	}
}

func TestListenerMux(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var gotProxy, gotHTTP bool
	m := NewListenerMux(ln,
		func(conn net.Conn) { gotProxy = true; conn.Close() },
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gotHTTP = true; w.WriteHeader(200) }),
	)
	go m.Serve()
	defer m.Close()

	// Send proxy protocol byte
	c, _ := net.Dial("tcp", ln.Addr().String())
	c.Write([]byte{0x01})
	c.Close()

	// Send HTTP
	resp, err := http.Get("http://" + ln.Addr().String() + "/test")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Wait a bit
	import_time_sleep(50)

	if !gotHTTP {
		t.Fatal("HTTP handler not called")
	}
}
```

NOTE: Replace `import_time_sleep(50)` with `time.Sleep(50 * time.Millisecond)` and add `"time"` import.

- [ ] **Step 2: Implement**

Create `server/internal/mux/mux.go`:

```go
package mux

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

const protoVersion = 0x01

// IsProxyProtocol returns true if the byte is our custom protocol version.
func IsProxyProtocol(b byte) bool {
	return b == protoVersion
}

// PeekConn wraps a net.Conn and allows peeking at the first byte.
type PeekConn struct {
	net.Conn
	reader *bufio.Reader
}

func NewPeekConn(c net.Conn) *PeekConn {
	return &PeekConn{Conn: c, reader: bufio.NewReader(c)}
}

func (pc *PeekConn) PeekByte() (byte, error) {
	b, err := pc.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (pc *PeekConn) Read(p []byte) (int, error) {
	return pc.reader.Read(p)
}

// ListenerMux accepts connections, peeks at the first byte, and routes:
// - 0x01 → proxyHandler(conn)
// - anything else → httpHandler via HTTP server
type ListenerMux struct {
	ln           net.Listener
	proxyHandler func(net.Conn)
	httpHandler  http.Handler
	httpConns    chan net.Conn
}

func NewListenerMux(ln net.Listener, proxyHandler func(net.Conn), httpHandler http.Handler) *ListenerMux {
	return &ListenerMux{
		ln:           ln,
		proxyHandler: proxyHandler,
		httpHandler:  httpHandler,
		httpConns:    make(chan net.Conn, 64),
	}
}

func (m *ListenerMux) Serve() error {
	// Start HTTP server on muxed connections
	httpLn := &chanListener{ch: m.httpConns, addr: m.ln.Addr()}
	go http.Serve(httpLn, m.httpHandler)

	for {
		conn, err := m.ln.Accept()
		if err != nil {
			close(m.httpConns)
			return err
		}
		go m.route(conn)
	}
}

func (m *ListenerMux) Close() error {
	return m.ln.Close()
}

func (m *ListenerMux) route(conn net.Conn) {
	pc := NewPeekConn(conn)
	b, err := pc.PeekByte()
	if err != nil {
		conn.Close()
		return
	}

	if IsProxyProtocol(b) {
		m.proxyHandler(pc)
	} else {
		m.httpConns <- pc
	}
}

// chanListener implements net.Listener using a channel of connections.
type chanListener struct {
	ch   chan net.Conn
	addr net.Addr
}

func (l *chanListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, io.EOF
	}
	return conn, nil
}

func (l *chanListener) Close() error   { return nil }
func (l *chanListener) Addr() net.Addr { return l.addr }
```

- [ ] **Step 3: Run tests**

```bash
cd server && go test ./internal/mux/ -v
```

- [ ] **Step 4: Commit**

```bash
git add server/internal/mux/
git commit -m "feat: add protocol multiplexer (proxy vs HTTP on same port)"
```

---

### Task 7: Rewrite Server Main

**Files:**
- Modify: `server/cmd/main.go`

- [ ] **Step 1: Rewrite main.go**

Replace `server/cmd/main.go` with:

```go
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"time"

	"proxyness/pkg/auth"
	"proxyness/pkg/proto"
	"proxyness/server/internal/admin"
	"proxyness/server/internal/db"
	"proxyness/server/internal/mux"
	"proxyness/server/internal/stats"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	dbPath := flag.String("db", "data.db", "SQLite database path")
	adminUser := flag.String("admin-user", "", "admin username (or ADMIN_USER env)")
	adminPass := flag.String("admin-password", "", "admin password (or ADMIN_PASSWORD env)")
	certFile := flag.String("cert", "cert.pem", "TLS certificate file")
	keyFile := flag.String("keyfile", "key.pem", "TLS private key file")
	flag.Parse()

	if *adminUser == "" {
		*adminUser = os.Getenv("ADMIN_USER")
	}
	if *adminPass == "" {
		*adminPass = os.Getenv("ADMIN_PASSWORD")
	}
	if *adminUser == "" || *adminPass == "" {
		log.Fatal("admin-user and admin-password are required (flags or ADMIN_USER/ADMIN_PASSWORD env)")
	}

	// Database
	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// Stats tracker
	tracker := stats.New()

	// TLS
	if err := ensureCert(*certFile, *keyFile); err != nil {
		log.Fatalf("cert: %v", err)
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", *addr, tlsCfg)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("server listening on %s", *addr)

	// Admin HTTP handler
	adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass)

	// Protocol multiplexer
	m := mux.NewListenerMux(ln,
		func(conn net.Conn) { handleProxy(conn, database, tracker) },
		adminHandler,
	)
	m.Serve()
}

func handleProxy(conn net.Conn, database *db.DB, tracker *stats.Tracker) {
	defer conn.Close()

	// Phase 1: Auth with multi-key
	keys, err := database.GetActiveKeys()
	if err != nil || len(keys) == 0 {
		log.Printf("no active keys: %v", err)
		conn.Close()
		return
	}

	msg := make([]byte, auth.AuthMsgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return
	}
	matchedKey, err := auth.ValidateAuthMessageMulti(keys, msg)
	if err != nil {
		proto.WriteResult(conn, false)
		log.Printf("auth failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	proto.WriteResult(conn, true)

	// Look up device info
	device, err := database.GetDeviceByKey(matchedKey)
	if err != nil {
		log.Printf("device lookup: %v", err)
		return
	}

	// Phase 2: Connect
	destAddr, port, err := proto.ReadConnect(conn)
	if err != nil {
		log.Printf("connect read: %v", err)
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", destAddr, port), 10*time.Second)
	if err != nil {
		proto.WriteResult(conn, false)
		return
	}
	defer target.Close()
	proto.WriteResult(conn, true)

	// Phase 3: Relay with traffic counting
	connID := tracker.Add(device.ID, device.Name, device.UserName)

	proto.CountingRelay(conn, target, func(in, out int64) {
		tracker.AddBytes(connID, in, out)
	})

	// Flush stats to DB
	info := tracker.Remove(connID)
	if info != nil {
		hour := time.Now().Truncate(time.Hour)
		database.RecordTraffic(device.ID, hour, info.BytesIn, info.BytesOut, 1)
	}
}

func ensureCert(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); err == nil {
		return nil
	}
	log.Println("generating self-signed TLS certificate...")
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Proxyness"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return err
	}
	certOut, _ := os.Create(certFile)
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()
	keyOut, _ := os.Create(keyFile)
	privKeyBytes, _ := x509.MarshalECPrivateKey(privKey)
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes})
	keyOut.Close()
	log.Printf("wrote %s and %s", certFile, keyFile)
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd server && go build ./cmd/
```

- [ ] **Step 3: Commit**

```bash
git add server/cmd/main.go
git commit -m "feat: rewrite server with multi-user auth, stats, protocol mux"
```

---

## Phase 2: Admin UI

### Task 8: Admin UI Scaffolding

**Files:**
- Create: `server/admin-ui/` (full Vite + React + shadcn/ui project)

- [ ] **Step 1: Create Vite project**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy/server
npm create vite@latest admin-ui -- --template react-ts
cd admin-ui
npm install
```

- [ ] **Step 2: Install shadcn/ui + dependencies**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy/server/admin-ui
npx shadcn@latest init -d
npx shadcn@latest add button card dialog input table badge switch separator label
npm install react-router-dom recharts
```

- [ ] **Step 3: Create API client**

Create `server/admin-ui/src/lib/api.ts`:

```ts
const BASE = "/admin/api";

async function request(path: string, options?: RequestInit) {
  const res = await fetch(BASE + path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...options?.headers,
    },
  });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

export interface User {
  id: number;
  name: string;
  created_at: string;
  device_count: number;
}

export interface Device {
  id: number;
  user_id: number;
  name: string;
  key: string;
  active: boolean;
  created_at: string;
}

export interface ActiveConn {
  device_id: number;
  device_name: string;
  user_name: string;
  started_at: string;
  bytes_in: number;
  bytes_out: number;
}

export interface TrafficStat {
  device_id: number;
  device_name: string;
  user_name: string;
  bytes_in: number;
  bytes_out: number;
  connections: number;
}

export interface Overview {
  total_bytes_in: number;
  total_bytes_out: number;
  active_connections: number;
  total_devices: number;
}

export interface DailyTraffic {
  day: string;
  bytes_in: number;
  bytes_out: number;
  connections: number;
}

export const api = {
  // Users
  listUsers: (): Promise<User[]> => request("/users"),
  createUser: (name: string): Promise<User> =>
    request("/users", { method: "POST", body: JSON.stringify({ name }) }),
  deleteUser: (id: number) =>
    request(`/users/${id}`, { method: "DELETE" }),

  // Devices
  listDevices: (userId: number): Promise<Device[]> =>
    request(`/users/${userId}/devices`),
  createDevice: (userId: number, name: string): Promise<Device> =>
    request(`/users/${userId}/devices`, { method: "POST", body: JSON.stringify({ name }) }),
  toggleDevice: (id: number, active: boolean) =>
    request(`/devices/${id}`, { method: "PATCH", body: JSON.stringify({ active }) }),
  deleteDevice: (id: number) =>
    request(`/devices/${id}`, { method: "DELETE" }),

  // Stats
  overview: (): Promise<Overview> => request("/stats/overview"),
  activeConns: (): Promise<ActiveConn[]> => request("/stats/active"),
  traffic: (period: string): Promise<TrafficStat[]> =>
    request(`/stats/traffic?period=${period}`),
  trafficDaily: (deviceId: number): Promise<DailyTraffic[]> =>
    request(`/stats/traffic/${deviceId}/daily`),
};
```

- [ ] **Step 4: Configure Vite proxy for dev**

Update `server/admin-ui/vite.config.ts`:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  base: "/admin/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      "/admin/api": {
        target: "https://localhost:443",
        secure: false,
      },
    },
  },
});
```

- [ ] **Step 5: Commit scaffolding**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy
git add server/admin-ui/package.json server/admin-ui/tsconfig*.json server/admin-ui/vite.config.ts server/admin-ui/src/ server/admin-ui/index.html server/admin-ui/components.json server/admin-ui/tailwind.config.* server/admin-ui/postcss.config.* server/admin-ui/eslint.config.*
git commit -m "feat: scaffold admin UI with React, shadcn/ui, Vite"
```

Do NOT commit `node_modules/`.

---

### Task 9: Admin UI Pages

**Files:**
- Create: `server/admin-ui/src/App.tsx`
- Create: `server/admin-ui/src/pages/Dashboard.tsx`
- Create: `server/admin-ui/src/pages/Users.tsx`
- Create: `server/admin-ui/src/pages/UserDetail.tsx`
- Create: `server/admin-ui/src/lib/format.ts`

- [ ] **Step 1: Create format utility**

Create `server/admin-ui/src/lib/format.ts`:

```ts
export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
}

export function formatDuration(start: string): string {
  const seconds = Math.floor((Date.now() - new Date(start).getTime()) / 1000);
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return [h, m, s].map((v) => String(v).padStart(2, "0")).join(":");
}
```

- [ ] **Step 2: Create App with routing**

Replace `server/admin-ui/src/App.tsx`:

```tsx
import { BrowserRouter, Routes, Route, Link, useLocation } from "react-router-dom";
import { Dashboard } from "./pages/Dashboard";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";

function Nav() {
  const loc = useLocation();
  const link = (to: string, label: string) => (
    <Link
      to={to}
      className={`px-3 py-2 rounded-md text-sm font-medium ${
        loc.pathname === to ? "bg-secondary text-secondary-foreground" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {label}
    </Link>
  );
  return (
    <nav className="border-b px-6 py-3 flex items-center gap-4">
      <span className="font-bold text-lg mr-4">Proxyness</span>
      {link("/admin", "Dashboard")}
      {link("/admin/users", "Users")}
    </nav>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <div className="min-h-screen bg-background text-foreground">
        <Nav />
        <main className="p-6 max-w-5xl mx-auto">
          <Routes>
            <Route path="/admin" element={<Dashboard />} />
            <Route path="/admin/users" element={<Users />} />
            <Route path="/admin/users/:id" element={<UserDetail />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
```

- [ ] **Step 3: Create Dashboard page**

Create `server/admin-ui/src/pages/Dashboard.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, Overview, ActiveConn } from "@/lib/api";
import { formatBytes, formatDuration } from "@/lib/format";

export function Dashboard() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [active, setActive] = useState<ActiveConn[]>([]);

  useEffect(() => {
    const load = () => {
      api.overview().then(setOverview);
      api.activeConns().then(setActive);
    };
    load();
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, []);

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Dashboard</h1>

      <div className="grid grid-cols-3 gap-4">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm text-muted-foreground">Active Connections</CardTitle></CardHeader>
          <CardContent><p className="text-3xl font-bold">{overview?.active_connections ?? 0}</p></CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm text-muted-foreground">Traffic Today</CardTitle></CardHeader>
          <CardContent><p className="text-3xl font-bold">{formatBytes((overview?.total_bytes_in ?? 0) + (overview?.total_bytes_out ?? 0))}</p></CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm text-muted-foreground">Total Devices</CardTitle></CardHeader>
          <CardContent><p className="text-3xl font-bold">{overview?.total_devices ?? 0}</p></CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader><CardTitle>Active Connections</CardTitle></CardHeader>
        <CardContent>
          {active.length === 0 ? (
            <p className="text-muted-foreground">No active connections</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Device</TableHead>
                  <TableHead>User</TableHead>
                  <TableHead>Duration</TableHead>
                  <TableHead>In</TableHead>
                  <TableHead>Out</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {active.map((c, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-medium">{c.device_name}</TableCell>
                    <TableCell>{c.user_name}</TableCell>
                    <TableCell>{formatDuration(c.started_at)}</TableCell>
                    <TableCell>{formatBytes(c.bytes_in)}</TableCell>
                    <TableCell>{formatBytes(c.bytes_out)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
```

- [ ] **Step 4: Create Users page**

Create `server/admin-ui/src/pages/Users.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api, User } from "@/lib/api";

export function Users() {
  const [users, setUsers] = useState<User[]>([]);
  const [name, setName] = useState("");
  const [open, setOpen] = useState(false);

  const load = () => api.listUsers().then(setUsers);
  useEffect(() => { load(); }, []);

  const handleCreate = async () => {
    if (!name.trim()) return;
    await api.createUser(name.trim());
    setName("");
    setOpen(false);
    load();
  };

  const handleDelete = async (id: number) => {
    if (!confirm("Delete user and all their devices?")) return;
    await api.deleteUser(id);
    load();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Users</h1>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger asChild>
            <Button>Add User</Button>
          </DialogTrigger>
          <DialogContent>
            <DialogHeader><DialogTitle>New User</DialogTitle></DialogHeader>
            <div className="space-y-4">
              <div><Label>Name</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name" /></div>
              <Button onClick={handleCreate} className="w-full">Create</Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Devices</TableHead>
                <TableHead>Created</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => (
                <TableRow key={u.id}>
                  <TableCell><Link to={`/admin/users/${u.id}`} className="font-medium text-blue-500 hover:underline">{u.name}</Link></TableCell>
                  <TableCell>{u.device_count}</TableCell>
                  <TableCell>{new Date(u.created_at).toLocaleDateString()}</TableCell>
                  <TableCell><Button variant="destructive" size="sm" onClick={() => handleDelete(u.id)}>Delete</Button></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
```

- [ ] **Step 5: Create UserDetail page with devices + chart**

Create `server/admin-ui/src/pages/UserDetail.tsx`:

```tsx
import { useEffect, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { BarChart, Bar, XAxis, YAxis, Tooltip, ResponsiveContainer } from "recharts";
import { api, Device, DailyTraffic } from "@/lib/api";
import { formatBytes } from "@/lib/format";

export function UserDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const userId = Number(id);
  const [devices, setDevices] = useState<Device[]>([]);
  const [name, setName] = useState("");
  const [open, setOpen] = useState(false);
  const [createdKey, setCreatedKey] = useState("");
  const [chartData, setChartData] = useState<Record<number, DailyTraffic[]>>({});

  const load = () => {
    api.listDevices(userId).then((devs) => {
      setDevices(devs);
      devs.forEach((d) =>
        api.trafficDaily(d.id).then((data) =>
          setChartData((prev) => ({ ...prev, [d.id]: data }))
        )
      );
    });
  };
  useEffect(() => { load(); }, [userId]);

  const handleCreate = async () => {
    if (!name.trim()) return;
    const dev = await api.createDevice(userId, name.trim());
    setCreatedKey(dev.key);
    setName("");
    load();
  };

  const handleToggle = async (devId: number, active: boolean) => {
    await api.toggleDevice(devId, active);
    load();
  };

  const handleDeleteDevice = async (devId: number) => {
    if (!confirm("Delete device?")) return;
    await api.deleteDevice(devId);
    load();
  };

  const handleDeleteUser = async () => {
    if (!confirm("Delete user and ALL devices?")) return;
    await api.deleteUser(userId);
    nav("/admin/users");
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Devices</h1>
        <div className="flex gap-2">
          <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) setCreatedKey(""); }}>
            <DialogTrigger asChild><Button>Add Device</Button></DialogTrigger>
            <DialogContent>
              <DialogHeader><DialogTitle>{createdKey ? "Device Created" : "New Device"}</DialogTitle></DialogHeader>
              {createdKey ? (
                <div className="space-y-4">
                  <p className="text-sm text-muted-foreground">Copy this key — it won't be shown again:</p>
                  <code className="block p-3 bg-muted rounded text-xs break-all select-all">{createdKey}</code>
                  <Button onClick={() => { navigator.clipboard.writeText(createdKey); }} className="w-full">Copy Key</Button>
                </div>
              ) : (
                <div className="space-y-4">
                  <div><Label>Device Name</Label><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="MacBook, iPhone..." /></div>
                  <Button onClick={handleCreate} className="w-full">Create</Button>
                </div>
              )}
            </DialogContent>
          </Dialog>
          <Button variant="destructive" onClick={handleDeleteUser}>Delete User</Button>
        </div>
      </div>

      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Active</TableHead>
                <TableHead></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {devices.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="font-medium">{d.name}</TableCell>
                  <TableCell><Badge variant={d.active ? "default" : "secondary"}>{d.active ? "Active" : "Inactive"}</Badge></TableCell>
                  <TableCell>{new Date(d.created_at).toLocaleDateString()}</TableCell>
                  <TableCell><Switch checked={d.active} onCheckedChange={(v) => handleToggle(d.id, v)} /></TableCell>
                  <TableCell><Button variant="destructive" size="sm" onClick={() => handleDeleteDevice(d.id)}>Delete</Button></TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {devices.map((d) => {
        const data = chartData[d.id];
        if (!data || data.length === 0) return null;
        return (
          <Card key={d.id}>
            <CardHeader><CardTitle className="text-sm">{d.name} — Traffic (7 days)</CardTitle></CardHeader>
            <CardContent>
              <ResponsiveContainer width="100%" height={200}>
                <BarChart data={data}>
                  <XAxis dataKey="day" tick={{ fontSize: 12 }} />
                  <YAxis tick={{ fontSize: 12 }} tickFormatter={(v) => formatBytes(v)} />
                  <Tooltip formatter={(v: number) => formatBytes(v)} />
                  <Bar dataKey="bytes_in" name="In" fill="#3b82f6" />
                  <Bar dataKey="bytes_out" name="Out" fill="#10b981" />
                </BarChart>
              </ResponsiveContainer>
            </CardContent>
          </Card>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 6: Update main.tsx entry point**

Replace `server/admin-ui/src/main.tsx`:

```tsx
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./index.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
```

- [ ] **Step 7: Verify build**

```bash
cd server/admin-ui && npm run build
```

Expected: builds to `dist/` without errors.

- [ ] **Step 8: Commit**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy
git add server/admin-ui/src/
git commit -m "feat: add admin UI pages (dashboard, users, devices with charts)"
```

---

## Phase 3: Integration

### Task 10: Embed UI + Dockerfile

**Files:**
- Create: `server/internal/admin/static.go`
- Modify: `server/internal/admin/admin.go`
- Modify: `Dockerfile`
- Modify: `.github/workflows/deploy.yml`
- Modify: `.gitignore`

- [ ] **Step 1: Create embed file**

Create `server/internal/admin/static.go`:

```go
package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:static
var staticFiles embed.FS

// SPAHandler serves the embedded SPA files, falling back to index.html for client-side routing.
func SPAHandler() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		if path == "" || path == "/" {
			path = "/index.html"
		}

		// Try to serve the file
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			// SPA fallback: serve index.html for unknown routes
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()
		r.URL.Path = path
		fileServer.ServeHTTP(w, r)
	})
}
```

NOTE: The `static/` directory will contain the built admin-ui files. It will be populated by the build process:
```bash
cd server/admin-ui && npm run build
cp -r dist/* ../internal/admin/static/
```

- [ ] **Step 2: Add SPA route to admin handler**

Add to `NewHandler` in `server/internal/admin/admin.go`, after the API routes:

```go
	// SPA static files
	mux.Handle("/admin/", SPAHandler())
```

- [ ] **Step 3: Update Dockerfile**

Replace `Dockerfile`:

```dockerfile
# Stage 1: Build admin UI
FROM node:22-alpine AS ui-builder
WORKDIR /ui
COPY server/admin-ui/package*.json ./
RUN npm ci
COPY server/admin-ui/ ./
RUN npm run build

# Stage 2: Build Go server
FROM golang:1.24-alpine AS builder
WORKDIR /build
COPY pkg/ pkg/
COPY server/ server/

# Copy built UI into embed location
RUN mkdir -p server/internal/admin/static
COPY --from=ui-builder /ui/dist/ server/internal/admin/static/

# Use replace directive instead of workspace
RUN cd server && go mod edit -replace proxyness/pkg=../pkg
WORKDIR /build/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /server ./cmd

# Stage 3: Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /usr/local/bin/server
EXPOSE 443
ENTRYPOINT ["server"]
```

- [ ] **Step 4: Update GitHub Actions**

Update `.github/workflows/deploy.yml` deploy step — replace the `docker run` command to use env vars instead of PROXY_KEY:

```yaml
      - name: Deploy to VPS
        uses: appleboy/ssh-action@v1
        with:
          host: ${{ secrets.VPS_HOST }}
          username: ${{ secrets.VPS_USER }}
          password: ${{ secrets.VPS_PASSWORD }}
          script: |
            if ! command -v docker &> /dev/null; then
              curl -fsSL https://get.docker.com | sh
            fi

            echo "${{ secrets.GHCR_TOKEN }}" | docker login ghcr.io -u ${{ secrets.GHCR_USER }} --password-stdin

            docker pull ghcr.io/${{ github.repository }}:latest

            docker stop proxyness 2>/dev/null || true
            docker rm proxyness 2>/dev/null || true

            docker run -d \
              --name proxyness \
              --restart unless-stopped \
              -p 443:443 \
              -v proxyness-data:/data \
              -e ADMIN_USER="${{ secrets.ADMIN_USER }}" \
              -e ADMIN_PASSWORD="${{ secrets.ADMIN_PASSWORD }}" \
              ghcr.io/${{ github.repository }}:latest \
              -addr ":443" \
              -db /data/data.db \
              -cert /data/cert.pem \
              -keyfile /data/key.pem
```

- [ ] **Step 5: Update .gitignore**

Add to `.gitignore`:

```
server/admin-ui/node_modules/
server/admin-ui/dist/
server/internal/admin/static/
```

- [ ] **Step 6: Create placeholder for static directory**

```bash
mkdir -p server/internal/admin/static
echo "placeholder" > server/internal/admin/static/.gitkeep
```

This ensures the embed directory exists for local Go builds. The real files come from the UI build.

- [ ] **Step 7: Build and verify**

```bash
cd server/admin-ui && npm run build
cp -r dist/* ../internal/admin/static/
cd ../.. && cd server && go build ./cmd/
```

- [ ] **Step 8: Commit**

```bash
git add server/internal/admin/static.go server/internal/admin/admin.go Dockerfile .github/workflows/deploy.yml .gitignore server/internal/admin/static/.gitkeep
git commit -m "feat: embed admin UI in Go binary, update Dockerfile and CI"
```

---

## New GitHub Secrets Required

After merging, add these secrets (replace PROXY_KEY with):

| Secret | Value |
|---|---|
| `ADMIN_USER` | `admin` |
| `ADMIN_PASSWORD` | a strong password |

`PROXY_KEY` is no longer needed — keys are in the database.
