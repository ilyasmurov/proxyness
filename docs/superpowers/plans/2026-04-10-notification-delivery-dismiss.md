# Notification Delivery Tracking & Dismiss — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Track which devices received each notification, let users dismiss banners, auto-expire old notifications, and deduplicate by type.

**Architecture:** Config service gets two new tables (`device_seen`, `notification_deliveries`) and an `expires_at` column on `notifications`. The `handleClientConfig` endpoint filters by first-seen time, expiry, and deduplicates per type. Admin UI shows delivery stats grouped by user. Client NotificationBanner gets a dismiss button with a single `dismissed_before` timestamp in localStorage.

**Tech Stack:** Go (config service), SQLite, React/TypeScript (admin UI + client)

---

### Task 1: Config Service DB — New Tables & Migration

**Files:**
- Modify: `config/internal/db/db.go`

- [ ] **Step 1: Add `expires_at` to Notification struct**

In `config/internal/db/db.go`, add `ExpiresAt` field to the `Notification` struct (after `CreatedAt`):

```go
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
```

- [ ] **Step 2: Add new tables and migration to `migrate()`**

Append to the existing `d.Exec(...)` block in `migrate()`, after the `service_config` INSERT:

```sql
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
```

Add migration for `expires_at` column (after the existing `beta_only` migration):

```go
d.Exec(`ALTER TABLE notifications ADD COLUMN expires_at TEXT`)
```

- [ ] **Step 3: Add `Delivery` struct and new DB methods**

Add struct after `Notification`:

```go
type Delivery struct {
	DeviceKey   string `json:"device_key"`
	DeliveredAt string `json:"delivered_at"`
}
```

Add these methods to `db.go`:

```go
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
```

- [ ] **Step 4: Update `CreateNotification` to accept `expiresAt`**

Change signature and implementation:

```go
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
```

- [ ] **Step 5: Update `ListNotifications` to scan `expires_at`**

Update the query and scan in `ListNotifications`:

```go
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
```

- [ ] **Step 6: Add `FilteredNotifications` method**

This replaces `ActiveNotifications` for the client-config endpoint. Applies all filters + dedup:

```go
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
```

- [ ] **Step 7: Verify build**

Run: `cd config && go build ./...`
Expected: compiles without errors.

- [ ] **Step 8: Commit**

```bash
git add config/internal/db/db.go
git commit -m "feat(config): add device_seen, deliveries tables, expires_at, filtered queries [skip-deploy]"
```

---

### Task 2: Config Service API — handleClientConfig + Deliveries Endpoint

**Files:**
- Modify: `config/internal/api/api.go`

- [ ] **Step 1: Update `handleClientConfig` with new flow**

Replace the current `handleClientConfig` method:

```go
func (s *Server) handleClientConfig(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if !s.validateKey(key) {
		http.Error(w, "invalid key", http.StatusForbidden)
		return
	}

	// Record device seen, get first_seen_at
	firstSeen, err := s.db.RecordDeviceSeen(key)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	cfg, err := s.db.GetServiceConfig()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	allNotifs, err := s.db.FilteredNotifications(firstSeen)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Filter beta_only: only send to beta clients
	clientVersion := r.URL.Query().Get("v")
	isBeta := strings.Contains(clientVersion, "beta")
	notifs := make([]db.Notification, 0, len(allNotifs))
	for _, n := range allNotifs {
		if n.BetaOnly && !isBeta {
			continue
		}
		notifs = append(notifs, n)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClientConfigResponse{
		ConfigURL:     cfg["config_url"],
		ProxyServer:   cfg["proxy_server"],
		RelayURL:      cfg["relay_url"],
		Notifications: notifs,
	})

	// Async: record deliveries
	if len(notifs) > 0 {
		ids := make([]string, len(notifs))
		for i, n := range notifs {
			ids[i] = n.ID
		}
		go s.db.RecordDeliveries(ids, key)
	}
}
```

- [ ] **Step 2: Update `handleCreateNotification` to accept `expires_at`**

```go
func (s *Server) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type      string          `json:"type"`
		Title     string          `json:"title"`
		Message   string          `json:"message"`
		Action    json.RawMessage `json:"action"`
		BetaOnly  bool            `json:"beta_only"`
		ExpiresAt string          `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Default expiry: 7 days from now
	if req.ExpiresAt == "" {
		req.ExpiresAt = time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	}
	n, err := s.db.CreateNotification(req.Type, req.Title, req.Message, req.Action, req.BetaOnly, req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(n)
}
```

- [ ] **Step 3: Add `handleGetDeliveries` endpoint**

```go
func (s *Server) handleGetDeliveries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deliveries, err := s.db.GetDeliveries(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if deliveries == nil {
		deliveries = []db.Delivery{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(deliveries)
}
```

- [ ] **Step 4: Add delivery count to `handleListNotifications`**

Enrich each notification with its delivery count for the admin list view:

```go
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	notifs, err := s.db.ListNotifications()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if notifs == nil {
		notifs = []db.Notification{}
	}

	type NotifWithCount struct {
		db.Notification
		DeliveryCount int `json:"delivery_count"`
	}
	out := make([]NotifWithCount, len(notifs))
	for i, n := range notifs {
		out[i] = NotifWithCount{Notification: n, DeliveryCount: s.db.DeliveryCount(n.ID)}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
```

- [ ] **Step 5: Register the deliveries route in `Handler()`**

Add after the existing DELETE route:

```go
mux.HandleFunc("GET /api/admin/notifications/{id}/deliveries", s.requireAdmin(s.handleGetDeliveries))
```

- [ ] **Step 6: Add `time` import**

Add `"time"` to the import block at the top of `api.go` (alongside existing imports).

- [ ] **Step 7: Verify build**

Run: `cd config && go build ./...`
Expected: compiles without errors.

- [ ] **Step 8: Commit**

```bash
git add config/internal/api/api.go
git commit -m "feat(config): delivery tracking in handleClientConfig, deliveries endpoint, expires_at default [skip-deploy]"
```

---

### Task 3: Proxy Server — Add Deliveries Reverse Proxy Route

**Files:**
- Modify: `server/internal/admin/admin.go`

- [ ] **Step 1: Verify existing route coverage**

The existing pattern `mux.Handle("/api/admin/notifications/", h.authHandler(configProxy))` at line 71 uses a trailing-slash catch-all. In Go 1.22+ `net/http`, the pattern `/api/admin/notifications/` matches any path under it, including `/api/admin/notifications/{id}/deliveries`. So the route is already covered — **no code change needed**.

Verify by checking the proxy server builds:

Run: `cd server && go build ./...`
Expected: compiles without errors.

- [ ] **Step 2: Commit (skip if no changes)**

No changes needed — the existing catch-all pattern already forwards `/api/admin/notifications/{id}/deliveries` to the config service.

---

### Task 4: Admin UI — Expires Field + Delivery Stats

**Files:**
- Modify: `server/admin-ui/src/lib/api.ts`
- Modify: `server/admin-ui/src/pages/Notifications.tsx`

- [ ] **Step 1: Update types and add API methods in `api.ts`**

Update `Notification` interface and add new types/methods:

```typescript
export interface Notification {
  id: string;
  type: string;
  title: string;
  message?: string;
  action?: any;
  active: boolean;
  beta_only: boolean;
  created_at: string;
  expires_at?: string;
  delivery_count?: number;
}

export interface NotificationDelivery {
  device_key: string;
  delivered_at: string;
}
```

Add to the `api` object:

```typescript
  getDeliveries: (id: string): Promise<NotificationDelivery[]> =>
    configRequest(`/notifications/${id}/deliveries`),
```

Update `createNotification` to include `expires_at`:

```typescript
  createNotification: (data: { type: string; title: string; message?: string; action?: any; beta_only?: boolean; expires_at?: string }): Promise<Notification> =>
    configRequest("/notifications", { method: "POST", body: JSON.stringify(data) }),
```

- [ ] **Step 2: Add expires field and delivery UI to `Notifications.tsx`**

Add state for the expires selector and delivery expansion at the top of the component (after `newActionUrl`):

```typescript
  const [newExpires, setNewExpires] = useState("7");
  const [expandedDelivery, setExpandedDelivery] = useState<string | null>(null);
  const [deliveries, setDeliveries] = useState<Record<string, import("@/lib/api").NotificationDelivery[]>>({});
  const [users, setUsers] = useState<import("@/lib/api").User[]>([]);
  const [devicesByUser, setDevicesByUser] = useState<Record<number, import("@/lib/api").Device[]>>({});
```

Load users and devices in `useEffect` (after existing `loadNotifs()` and `loadServices()`):

```typescript
    api.listUsers().then(async (users) => {
      setUsers(users);
      const byUser: Record<number, import("@/lib/api").Device[]> = {};
      await Promise.all(users.map(async (u) => {
        byUser[u.id] = await api.listDevices(u.id);
      }));
      setDevicesByUser(byUser);
    });
```

Update `handleCreate` — compute `expires_at` from `newExpires` and pass it:

```typescript
  const handleCreate = async () => {
    if (!newTitle.trim()) return;
    try {
      const action = newActionType !== "none" && newActionLabel.trim()
        ? { label: newActionLabel.trim(), type: newActionType, ...(newActionType === "open_url" && newActionUrl.trim() ? { url: newActionUrl.trim() } : {}) }
        : undefined;
      const expiresAt = newExpires === "never" ? undefined : new Date(Date.now() + parseInt(newExpires) * 86400000).toISOString();
      await api.createNotification({ type: newType, title: newTitle.trim(), message: newMessage.trim() || undefined, action, beta_only: newBetaOnly, expires_at: expiresAt });
      setNewTitle("");
      setNewMessage("");
      setNewBetaOnly(false);
      setNewActionType("none");
      setNewActionLabel("");
      setNewActionUrl("");
      setNewExpires("7");
      loadNotifs();
    } catch (e: any) {
      setError(e.message);
    }
  };
```

Add expires dropdown in the create form — after the beta_only checkbox and before the Create button:

```tsx
              <div className="flex items-center gap-3">
                <label className="flex items-center gap-2 text-sm text-muted-foreground">
                  <input
                    type="checkbox"
                    checked={newBetaOnly}
                    onChange={(e) => setNewBetaOnly(e.target.checked)}
                    className="rounded"
                  />
                  Beta only
                </label>
                <div className="flex items-center gap-2 ml-auto">
                  <span className="text-xs text-muted-foreground">Expires in</span>
                  <select
                    value={newExpires}
                    onChange={(e) => setNewExpires(e.target.value)}
                    className="bg-background border border-border rounded-md px-2 py-1 text-sm"
                  >
                    <option value="1">1 day</option>
                    <option value="3">3 days</option>
                    <option value="7">7 days</option>
                    <option value="30">30 days</option>
                    <option value="never">Never</option>
                  </select>
                </div>
              </div>
```

This replaces the existing `<label className="flex items-center gap-2 ...">` block for beta_only.

Add toggle handler for delivery expansion:

```typescript
  const toggleDelivery = async (id: string) => {
    if (expandedDelivery === id) {
      setExpandedDelivery(null);
      return;
    }
    setExpandedDelivery(id);
    if (!deliveries[id]) {
      const data = await api.getDeliveries(id);
      setDeliveries((prev) => ({ ...prev, [id]: data }));
    }
  };
```

In the notification list item (inside the `<div className="flex items-center gap-3 min-w-0">` area), add a delivery count badge after the existing badges:

```tsx
                        <button
                          onClick={() => toggleDelivery(n.id)}
                          className="text-xs text-emerald-400 bg-emerald-500/20 border border-emerald-500/30 rounded-full px-2 py-0.5 hover:bg-emerald-500/30 transition-colors"
                        >
                          Delivered: {n.delivery_count ?? 0}
                        </button>
```

After each notification row's closing `</div>`, add the expandable delivery section:

```tsx
                    {expandedDelivery === n.id && (
                      <div className="border border-border rounded-md px-3 py-2 text-xs space-y-2">
                        {!deliveries[n.id] ? (
                          <span className="text-muted-foreground">Loading...</span>
                        ) : deliveries[n.id].length === 0 ? (
                          <span className="text-muted-foreground">No deliveries yet</span>
                        ) : (
                          (() => {
                            const keyToDevice: Record<string, { name: string; userId: number }> = {};
                            for (const [uid, devs] of Object.entries(devicesByUser)) {
                              for (const d of devs) {
                                keyToDevice[d.key] = { name: d.name, userId: Number(uid) };
                              }
                            }
                            const groups: Record<string, { userName: string; devices: { name: string; deliveredAt: string }[] }> = {};
                            for (const dl of deliveries[n.id]) {
                              const dev = keyToDevice[dl.device_key];
                              const userId = dev?.userId ?? 0;
                              const userName = users.find((u) => u.id === userId)?.name ?? "Unknown";
                              if (!groups[userName]) groups[userName] = { userName, devices: [] };
                              groups[userName].devices.push({ name: dev?.name ?? dl.device_key.slice(0, 8), deliveredAt: dl.delivered_at });
                            }
                            return Object.values(groups).map((g) => (
                              <div key={g.userName}>
                                <div className="font-medium text-muted-foreground">{g.userName}</div>
                                {g.devices.map((d, i) => (
                                  <div key={i} className="ml-4 flex justify-between">
                                    <span>{d.name}</span>
                                    <span className="text-muted-foreground">{new Date(d.deliveredAt).toLocaleString()}</span>
                                  </div>
                                ))}
                              </div>
                            ));
                          })()
                        )}
                      </div>
                    )}
```

- [ ] **Step 3: Verify build**

Run: `cd server/admin-ui && npm run build`
Expected: builds without errors.

- [ ] **Step 4: Commit**

```bash
git add server/admin-ui/src/lib/api.ts server/admin-ui/src/pages/Notifications.tsx
git commit -m "feat(admin): expires_at in create form, delivery stats per notification [skip-deploy]"
```

---

### Task 5: Client — Dismiss Button on NotificationBanner

**Files:**
- Modify: `client/src/renderer/components/NotificationBanner.tsx`

- [ ] **Step 1: Add dismiss state and localStorage logic**

Add state and helper at the top of `NotificationBanner()`, after the existing state declarations:

```typescript
  const [dismissedBefore, setDismissedBefore] = useState<string>(
    () => localStorage.getItem("notification-dismissed-before") || ""
  );
```

- [ ] **Step 2: Add dismiss handler**

After the existing `useEffect`, add:

```typescript
  const handleDismiss = () => {
    const now = new Date().toISOString();
    localStorage.setItem("notification-dismissed-before", now);
    setDismissedBefore(now);
  };
```

- [ ] **Step 3: Filter notifications by dismissed_before**

Replace the existing sort+select block (lines 65-67):

```typescript
  const sorted = [...notifications]
    .filter((n) => !dismissedBefore || n.created_at > dismissedBefore)
    .sort((a, b) => (TYPE_PRIORITY[a.type] ?? 9) - (TYPE_PRIORITY[b.type] ?? 9));
  const notif = sorted[0];
```

- [ ] **Step 4: Add X button to the banner**

Replace the return block for the normal notification banner (the last `return` with `colors`):

```tsx
  return (
    <div style={{ padding: "10px 12px", marginBottom: 16, background: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 8, fontSize: 13 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontWeight: 600, marginBottom: notif.message ? 4 : 0 }}>{notif.title}</div>
          {notif.message && <div style={{ color: "#94a3b8", fontSize: 12 }}>{notif.message}</div>}
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 8, marginLeft: 12, flexShrink: 0 }}>
          {notif.action && (
            <button onClick={handleAction} style={{ padding: "4px 12px", background: "#3b82f6", color: "#fff", border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer", whiteSpace: "nowrap" }}>
              {notif.action.label}
            </button>
          )}
          <button onClick={handleDismiss} style={{ background: "none", border: "none", color: "#64748b", cursor: "pointer", fontSize: 16, padding: "0 2px", lineHeight: 1 }}>
            ×
          </button>
        </div>
      </div>
    </div>
  );
```

- [ ] **Step 5: Verify dev build**

Run: `cd client && npx tsc --noEmit`
Expected: no type errors.

- [ ] **Step 6: Commit**

```bash
git add client/src/renderer/components/NotificationBanner.tsx
git commit -m "feat(client): dismiss button on notification banner [skip-deploy]"
```

---

### Task 6: Build Config Service Docker Image & Deploy

**Files:**
- No code changes — build and deploy.

- [ ] **Step 1: Build and push config service image**

The config service has its own Dockerfile. Build and push:

```bash
cd config && docker build -t ghcr.io/ilyasmurov/smurov-proxy-config:latest . && docker push ghcr.io/ilyasmurov/smurov-proxy-config:latest
```

- [ ] **Step 2: Restart config container on VPS**

```bash
ssh root@95.181.162.242 "docker pull ghcr.io/ilyasmurov/smurov-proxy-config:latest && docker-compose up -d config"
```

- [ ] **Step 3: Verify migration ran**

Test that new tables exist by calling the admin endpoint:

```bash
curl -sk -u admin:PASSWORD 'https://proxy.smurov.com/api/admin/notifications' | python3 -m json.tool
```

Expected: notifications list now includes `expires_at` and `delivery_count` fields.

- [ ] **Step 4: Create a test notification with expires_at**

```bash
curl -sk -u admin:PASSWORD -X POST 'https://proxy.smurov.com/api/admin/notifications' \
  -H 'Content-Type: application/json' \
  -d '{"type":"info","title":"Test delivery tracking","message":"Testing"}'
```

Verify `expires_at` is set to ~7 days from now in the response.
