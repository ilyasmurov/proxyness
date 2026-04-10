# Notification Delivery Tracking & Dismiss

## Problem

1. No visibility into which devices received notifications
2. No way for users to dismiss a notification banner
3. New users see all historical notifications
4. Multiple stale update notifications stack up

## Design

### Config Service — DB Changes

**New table `device_seen`:**
```sql
CREATE TABLE IF NOT EXISTS device_seen (
    device_key  TEXT PRIMARY KEY,
    first_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
);
```

**New table `notification_deliveries`:**
```sql
CREATE TABLE IF NOT EXISTS notification_deliveries (
    notification_id TEXT NOT NULL,
    device_key      TEXT NOT NULL,
    delivered_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (notification_id, device_key)
);
```

**Alter `notifications` — add `expires_at`:**
```sql
ALTER TABLE notifications ADD COLUMN expires_at TEXT;
```
Default: 7 days from creation (set in Go code). NULL = never expires.

### Config Service — `handleClientConfig` Changes

Current flow: validate key → fetch active notifications → filter beta_only → respond.

New flow:
1. Validate key
2. `INSERT OR IGNORE INTO device_seen(device_key) VALUES(?)`
3. Query `first_seen_at` for this device
4. Fetch notifications with filter:
   - `active = 1`
   - `created_at > first_seen_at`
   - `expires_at IS NULL OR expires_at > datetime('now')`
   - Beta filter (existing)
5. Deduplicate: for types `update`, `maintenance`, `migration` — keep only the latest per type. `info` — no limit.
6. Respond with filtered notifications
7. Fire goroutine: `INSERT OR IGNORE INTO notification_deliveries` for each returned notification × device_key

### Config Service — New Admin Endpoint

`GET /api/admin/notifications/{id}/deliveries`

Returns: `[{"device_key": "abc...", "delivered_at": "2026-04-10T14:41:22Z"}, ...]`

Requires Basic Auth (same as other admin endpoints).

### Config Service — `handleCreateNotification` Changes

Accept optional `expires_at` field. If not provided, default to `time.Now().Add(7 * 24 * time.Hour)`.

### Proxy Server — Route Addition

Add reverse proxy route for the new deliveries endpoint:
```go
mux.Handle("/api/admin/notifications/{id}/deliveries", h.authHandler(configProxy))
```

Existing `/api/admin/notifications/` catch-all pattern should already match this — verify and add explicit route if needed.

### Admin UI — Notifications Page

**Create form additions:**
- "Expires" field: date picker or dropdown (1 day, 3 days, 7 days, 30 days, never). Default: 7 days.

**Notification list additions:**
- Each notification shows "Delivered: N" badge
- Click badge → expandable section showing devices grouped by user
- Frontend joins delivery data (device_key + delivered_at) with user/device list (already loaded)
- Group by user name, show device name + delivered_at timestamp

### Client — NotificationBanner

**Dismiss button:**
- X button on right side of banner
- On click: store `dismissed_before = new Date().toISOString()` in localStorage key `notification-dismissed-before`
- Filter: hide notifications where `created_at <= dismissed_before`
- New notifications (created after dismiss) appear normally

## Data Flow

```
Admin creates notification (with expires_at)
    ↓
Config DB: notifications table
    ↓
Client polls /api/client-config
    ↓
Config service:
  1. Record device_seen (first_seen_at)
  2. Filter: active + not expired + created after first_seen
  3. Deduplicate by type (latest for update/maintenance/migration)
  4. Return filtered list
  5. Async: record deliveries
    ↓
Client renders NotificationBanner
  - Filter by dismissed_before from localStorage
  - Show highest priority notification
  - X button to dismiss
    ↓
Admin views delivery stats
  - Fetches /api/admin/notifications/{id}/deliveries
  - Joins with users/devices on frontend by device_key
  - Groups by user
```
