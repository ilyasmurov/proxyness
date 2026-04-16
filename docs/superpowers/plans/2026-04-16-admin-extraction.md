# Admin Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the admin panel from the proxy server into a standalone React SPA served by nginx, with the proxy server providing REST API + SSE for live stats.

**Architecture:** Proxy server keeps all admin REST endpoints + adds CORS + SSE. Embedded React SPA and landing template are removed. New `admin/` directory: React 19 + Vite + Tailwind (migrated from `server/admin-ui/`), built into nginx container, deployed on Aeza at `admin.proxyness.smurov.com`. Host-level nginx on Aeza does SNI-based TCP routing so both proxy (proxyness.smurov.com) and admin (admin.proxyness.smurov.com) share port 443.

**Tech Stack:** Go 1.26 (proxy server changes), React 19, Vite 8, Tailwind CSS 4, TypeScript 5.9, nginx (Alpine), certbot (Let's Encrypt), nginx stream module (SNI routing).

**Prereqs:**
- Postgres migration complete (server-2.0.0 deployed)
- SSH access to Aeza (95.181.162.242)
- DNS control for `admin.proxyness.smurov.com`

---

## Task 1: Add CORS and SSE to proxy server

**Files:**
- Modify: `server/internal/admin/admin.go`

- [ ] **Step 1: Add CORS in ServeHTTP**

Replace the `ServeHTTP` method (line 92-94) with CORS-aware version:

```go
// ServeHTTP implements http.Handler with CORS for the admin dashboard.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "https://admin.proxyness.smurov.com" || origin == "http://localhost:5173" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Max-Age", "3600")
	}
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}
```

`http://localhost:5173` is for local Vite dev server. Harmless in production (no one can spoof Origin from a browser).

- [ ] **Step 2: Add SSE endpoint registration**

In `NewHandler`, after the `GET /admin/api/logs` line (line 51), add:

```go
	mux.HandleFunc("GET /admin/api/stats/stream", h.auth(h.statsStream))
```

- [ ] **Step 3: Add SSE handler method**

Add the following method after `statsRate` (after line 344):

```go
func (h *Handler) statsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ctx := r.Context()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			active := h.tracker.Active()
			if active == nil {
				active = []stats.ConnInfo{}
			}
			activeJSON, _ := json.Marshal(active)
			fmt.Fprintf(w, "event: active\ndata: %s\n\n", activeJSON)

			rates := h.tracker.Rates()
			if rates == nil {
				rates = []stats.DeviceRate{}
			}
			ratesJSON, _ := json.Marshal(rates)
			fmt.Fprintf(w, "event: rate\ndata: %s\n\n", ratesJSON)

			flusher.Flush()
		}
	}
}
```

- [ ] **Step 4: Add missing imports**

Add `"fmt"` and `"time"` to the import block in admin.go (they're needed by `statsStream`).

- [ ] **Step 5: Build**

```bash
cd server && go build ./...
```

Expected: compiles clean.

- [ ] **Step 6: Commit**

```bash
git add server/internal/admin/admin.go
git commit -m "feat(admin): add CORS for external admin dashboard and SSE stats stream"
```

## Task 2: Remove embedded admin UI from proxy server

**Files:**
- Delete: `server/internal/admin/static.go`
- Delete: `server/internal/admin/landing.go`
- Modify: `server/internal/admin/admin.go` (remove SPA + landing routes)
- Modify: `Dockerfile` (remove node build stage)

- [ ] **Step 1: Delete static.go and landing.go**

```bash
rm server/internal/admin/static.go server/internal/admin/landing.go
```

- [ ] **Step 2: Remove the static embed directory**

```bash
rm -rf server/internal/admin/static/
```

- [ ] **Step 3: Remove SPA route from admin.go, keep landing**

In `NewHandler`, delete only the SPA line:

```go
	// SPA static files (auth required)
	mux.Handle("/admin/", h.authHandler(SPAHandler()))
```

**Keep** the landing reverse proxy — `GET /{$}` → landing container. The proxy server still serves the landing page at `proxyness.smurov.com/`. This route has no dependency on static.go or landing.go.

- [ ] **Step 4: Remove `downloadsDir` parameter and `/download/` route**

The downloads directory was only used by the landing page. Remove from `NewHandler`:

1. Delete the `downloadsDir` field from the `Handler` struct.
2. Change `NewHandler` signature — remove `downloadsDir string` parameter:
```go
func NewHandler(d *db.DB, tr *stats.Tracker, user, password, configAddr string) *Handler {
	h := &Handler{db: d, tracker: tr, user: user, password: password}
```
3. Delete the `/download/` route:
```go
	// Download files
	mux.Handle("/download/", http.StripPrefix("/download/", http.FileServer(http.Dir(downloadsDir))))
```

- [ ] **Step 5: Update main.go to match new NewHandler signature**

In `server/cmd/main.go`, change the `admin.NewHandler` call (line 82) from:
```go
adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass, "/data/downloads", *configAddr)
```
to:
```go
adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass, *configAddr)
```

- [ ] **Step 6: Clean up unused imports in admin.go**

Remove any imports that become unused after deleting landing/static references. The `html/template` and `embed` imports are no longer needed. Keep: `database/sql`, `encoding/json`, `fmt`, `net/http`, `net/http/httputil`, `net/url`, `strconv`, `strings`, `time`, and the internal packages.

- [ ] **Step 7: Simplify Dockerfile — remove node build stage**

Replace the entire Dockerfile with:

```dockerfile
# Build Go server
FROM golang:1.26-alpine AS builder
WORKDIR /build
COPY pkg/ pkg/
COPY server/ server/

# Use replace directive instead of workspace
RUN cd server && go mod edit -replace proxyness/pkg=../pkg && go mod tidy
WORKDIR /build/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /server ./cmd

# Runtime
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /server /usr/local/bin/server
COPY changelog.json /changelog.json
EXPOSE 443/tcp 443/udp
ENTRYPOINT ["server"]
```

No more `node:22-alpine` stage, no more copying UI dist into admin/static/.

- [ ] **Step 8: Build**

```bash
cd server && go build ./...
```

Expected: compiles clean. The `static/` directory no longer exists and `static.go` (with `//go:embed`) is deleted, so no embed errors.

- [ ] **Step 9: Commit**

```bash
git add -A server/internal/admin/ server/cmd/main.go Dockerfile
git commit -m "refactor(server): remove embedded admin UI and landing page"
```

## Task 3: Create admin React app

Copy the existing `server/admin-ui/` project to `admin/`, adapt it for standalone deployment with external API.

**Files:**
- Create: `admin/` (entire directory, copied from `server/admin-ui/` then modified)
- Create: `admin/src/lib/auth.ts` (credential management)
- Create: `admin/src/hooks/useStatsStream.ts` (SSE hook)
- Modify: `admin/src/lib/api.ts` (external API URL + auth headers)
- Modify: `admin/src/App.tsx` (routes without `/admin` prefix, auth gate)
- Modify: `admin/src/pages/Dashboard.tsx` (SSE instead of polling)
- Modify: `admin/vite.config.ts` (base `/`, dev proxy)

- [ ] **Step 1: Copy admin-ui to admin/**

```bash
cp -r server/admin-ui admin
```

- [ ] **Step 2: Update package.json**

In `admin/package.json`, change the `"name"` field to `"proxyness-admin"`.

- [ ] **Step 3: Rewrite vite.config.ts**

Replace `admin/vite.config.ts` with:

```typescript
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/",
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/admin/api": {
        target: "https://proxyness.smurov.com",
        changeOrigin: true,
        secure: false,
      },
      "/api/admin": {
        target: "https://proxyness.smurov.com",
        changeOrigin: true,
        secure: false,
      },
    },
  },
});
```

Key changes: `base: "/"` (was `/admin/`), proxy targets the real server for dev.

- [ ] **Step 4: Create auth module**

Create `admin/src/lib/auth.ts`:

```typescript
const STORAGE_KEY = "proxyness-admin-auth";

export function getCredentials(): string | null {
  return sessionStorage.getItem(STORAGE_KEY);
}

export function setCredentials(user: string, pass: string): void {
  sessionStorage.setItem(STORAGE_KEY, btoa(`${user}:${pass}`));
}

export function clearCredentials(): void {
  sessionStorage.removeItem(STORAGE_KEY);
}

export function authHeaders(): Record<string, string> {
  const creds = getCredentials();
  if (!creds) return {};
  return { Authorization: `Basic ${creds}` };
}
```

Credentials stored in `sessionStorage` — cleared when the tab closes.

- [ ] **Step 5: Rewrite api.ts for external API**

Replace `admin/src/lib/api.ts` with:

```typescript
import { authHeaders, clearCredentials } from "./auth";

const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";
const BASE = `${API_URL}/admin/api`;
const CONFIG_BASE = `${API_URL}/api/admin`;

async function request(path: string, options?: RequestInit) {
  const res = await fetch(BASE + path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...options?.headers,
    },
  });
  if (res.status === 401) {
    clearCredentials();
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

async function configRequest(path: string, options?: RequestInit) {
  const res = await fetch(CONFIG_BASE + path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...authHeaders(),
      ...options?.headers,
    },
  });
  if (res.status === 401) {
    clearCredentials();
    window.location.reload();
    throw new Error("Unauthorized");
  }
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

// ---- Types ----

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

export interface DeviceRate {
  device_id: number;
  device_name: string;
  user_name: string;
  version: string;
  download: number;
  upload: number;
  total_bytes: number;
  connections: number;
  tls_conns: number;
  raw_conns: number;
  history: Array<{ t: number; down: number; up: number }>;
}

export interface ChangelogEntry {
  id: string;
  title: string;
  description: string;
  type: "feature" | "fix" | "improvement";
  createdAt: string;
}

export interface ChangelogResponse {
  entries: ChangelogEntry[];
  total: number;
  page: number;
  pages: number;
}

export interface LogEntry {
  id: number;
  level: string;
  message: string;
  created_at: string;
}

export interface LogsResponse {
  entries: LogEntry[];
  total: number;
}

export interface SiteWithStats {
  id: number;
  slug: string;
  label: string;
  primary_domain: string;
  approved: boolean;
  created_by_user_id: number | null;
  created_by_user_name: string;
  users_count: number;
  domains_count: number;
  created_at: string;
}

export interface SiteDomainRow {
  domain: string;
  is_primary: boolean;
}

export interface SiteUserRow {
  id: number;
  name: string;
  enabled: boolean;
  updated_at: number;
}

export interface SiteDetail {
  id: number;
  slug: string;
  label: string;
  primary_domain: string;
  approved: boolean;
  created_by_user_id: number | null;
  created_by_user_name: string;
  created_at: string;
  domains: SiteDomainRow[];
  users: SiteUserRow[];
}

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

export interface ServiceConfigMap {
  [key: string]: string;
}

// ---- API methods ----

export const api = {
  listUsers: (): Promise<User[]> => request("/users"),
  createUser: (name: string): Promise<User> =>
    request("/users", { method: "POST", body: JSON.stringify({ name }) }),
  deleteUser: (id: number) =>
    request(`/users/${id}`, { method: "DELETE" }),
  listDevices: (userId: number): Promise<Device[]> =>
    request(`/users/${userId}/devices`),
  createDevice: (userId: number, name: string): Promise<Device> =>
    request(`/users/${userId}/devices`, { method: "POST", body: JSON.stringify({ name }) }),
  toggleDevice: (id: number, active: boolean) =>
    request(`/devices/${id}`, { method: "PATCH", body: JSON.stringify({ active }) }),
  deleteDevice: (id: number) =>
    request(`/devices/${id}`, { method: "DELETE" }),
  overview: (): Promise<Overview> => request("/stats/overview"),
  activeConns: (): Promise<ActiveConn[]> => request("/stats/active"),
  traffic: (period: string): Promise<TrafficStat[]> =>
    request(`/stats/traffic?period=${period}`),
  trafficDaily: (deviceId: number): Promise<DailyTraffic[]> =>
    request(`/stats/traffic/${deviceId}/daily`),
  rate: (): Promise<DeviceRate[]> => request("/stats/rate"),
  changelog: (page = 1, perPage = 10): Promise<ChangelogResponse> =>
    request(`/changelog?page=${page}&per_page=${perPage}`),
  logs: (limit = 200, offset = 0, level = ""): Promise<LogsResponse> =>
    request(`/logs?limit=${limit}&offset=${offset}${level ? `&level=${level}` : ""}`),
  listSites: (): Promise<SiteWithStats[]> => request("/sites"),
  getSite: (id: number): Promise<SiteDetail> => request(`/sites/${id}`),
  deleteSite: (id: number) => request(`/sites/${id}`, { method: "DELETE" }),
  deleteSiteDomain: (id: number, domain: string) =>
    request(`/sites/${id}/domains/${encodeURIComponent(domain)}`, { method: "DELETE" }),
  listNotifications: (): Promise<Notification[]> => configRequest("/notifications"),
  createNotification: (data: { type: string; title: string; message?: string; action?: any; beta_only?: boolean; expires_at?: string }): Promise<Notification> =>
    configRequest("/notifications", { method: "POST", body: JSON.stringify(data) }),
  deleteNotification: (id: string) =>
    configRequest(`/notifications/${id}`, { method: "DELETE" }),
  updateNotification: (id: string, data: { active?: boolean; title?: string; message?: string }) =>
    configRequest(`/notifications/${id}`, { method: "PATCH", body: JSON.stringify(data) }),
  getDeliveries: (id: string): Promise<NotificationDelivery[]> =>
    configRequest(`/notifications/${id}/deliveries`),
  getServices: (): Promise<ServiceConfigMap> => configRequest("/services"),
  setServices: (data: ServiceConfigMap) =>
    configRequest("/services", { method: "PUT", body: JSON.stringify(data) }),
};
```

Key changes vs original: `API_URL` from env var, `authHeaders()` injected into every request, 401 → clear credentials and reload.

- [ ] **Step 6: Create SSE hook**

Create `admin/src/hooks/useStatsStream.ts`:

```typescript
import { useEffect, useRef, useState } from "react";
import type { ActiveConn, DeviceRate } from "@/lib/api";
import { authHeaders } from "@/lib/auth";

const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";

export function useStatsStream() {
  const [active, setActive] = useState<ActiveConn[]>([]);
  const [rates, setRates] = useState<DeviceRate[]>([]);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;

    async function connect() {
      try {
        const res = await fetch(`${API_URL}/admin/api/stats/stream`, {
          headers: authHeaders(),
          signal: controller.signal,
        });
        if (!res.ok || !res.body) return;

        const reader = res.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });

          const parts = buf.split("\n\n");
          buf = parts.pop()!;

          for (const part of parts) {
            let event = "";
            let data = "";
            for (const line of part.split("\n")) {
              if (line.startsWith("event: ")) event = line.slice(7);
              else if (line.startsWith("data: ")) data = line.slice(6);
            }
            if (!data) continue;
            try {
              const parsed = JSON.parse(data);
              if (event === "active") setActive(parsed);
              else if (event === "rate") setRates(parsed);
            } catch {}
          }
        }
      } catch (e: any) {
        if (e.name === "AbortError") return;
        // Reconnect after 3s on error
        await new Promise((r) => setTimeout(r, 3000));
        if (!controller.signal.aborted) connect();
      }
    }

    connect();
    return () => controller.abort();
  }, []);

  return { active, rates };
}
```

Uses `fetch` + `ReadableStream` because native `EventSource` doesn't support custom `Authorization` headers.

- [ ] **Step 7: Rewrite App.tsx — remove `/admin` prefix, add auth gate**

Replace `admin/src/App.tsx` with:

```tsx
import { useState } from "react";
import { BrowserRouter, Routes, Route, Link, useLocation } from "react-router-dom";
import { Dashboard } from "./pages/Dashboard";
import { Users } from "./pages/Users";
import { UserDetail } from "./pages/UserDetail";
import { Sites } from "./pages/Sites";
import { SiteDetail } from "./pages/SiteDetail";
import { Releases } from "./pages/Releases";
import { Changelog } from "./pages/Changelog";
import { Logs } from "./pages/Logs";
import { Notifications } from "./pages/Notifications";
import { getCredentials, setCredentials } from "./lib/auth";

function Login({ onLogin }: { onLogin: () => void }) {
  const [user, setUser] = useState("");
  const [pass, setPass] = useState("");
  const [error, setError] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setCredentials(user, pass);
    // Verify credentials with a lightweight request
    const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";
    try {
      const res = await fetch(`${API_URL}/admin/api/stats/overview`, {
        headers: { Authorization: `Basic ${btoa(`${user}:${pass}`)}` },
      });
      if (res.ok) {
        onLogin();
      } else {
        setError(true);
      }
    } catch {
      setError(true);
    }
  };

  return (
    <div className="min-h-screen bg-background text-foreground flex items-center justify-center">
      <form onSubmit={submit} className="w-80 space-y-4">
        <h1 className="text-2xl font-bold text-center">Proxyness Admin</h1>
        <input
          type="text"
          placeholder="Username"
          value={user}
          onChange={(e) => setUser(e.target.value)}
          className="w-full px-3 py-2 border rounded-md bg-background"
          autoFocus
        />
        <input
          type="password"
          placeholder="Password"
          value={pass}
          onChange={(e) => setPass(e.target.value)}
          className="w-full px-3 py-2 border rounded-md bg-background"
        />
        {error && <p className="text-red-500 text-sm">Invalid credentials</p>}
        <button type="submit" className="w-full px-3 py-2 bg-primary text-primary-foreground rounded-md">
          Sign in
        </button>
      </form>
    </div>
  );
}

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
      {link("/", "Dashboard")}
      {link("/users", "Users")}
      {link("/sites", "Sites")}
      {link("/notifications", "Notifications")}
      {link("/releases", "Releases")}
      {link("/changelog", "Changelog")}
      {link("/logs", "Logs")}
    </nav>
  );
}

export default function App() {
  const [authed, setAuthed] = useState(!!getCredentials());

  if (!authed) return <Login onLogin={() => setAuthed(true)} />;

  return (
    <BrowserRouter>
      <div className="min-h-screen bg-background text-foreground">
        <Nav />
        <main className="p-6 max-w-5xl mx-auto">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/users" element={<Users />} />
            <Route path="/users/:id" element={<UserDetail />} />
            <Route path="/sites" element={<Sites />} />
            <Route path="/sites/:id" element={<SiteDetail />} />
            <Route path="/notifications" element={<Notifications />} />
            <Route path="/releases" element={<Releases />} />
            <Route path="/changelog" element={<Changelog />} />
            <Route path="/logs" element={<Logs />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  );
}
```

Key changes: auth gate with login form, routes without `/admin` prefix, `getCredentials()` check on mount.

- [ ] **Step 8: Update Dashboard.tsx to use SSE**

Replace `admin/src/pages/Dashboard.tsx` with:

```tsx
import { useEffect, useState } from "react";
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, Tooltip } from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { Overview, DeviceRate } from "@/lib/api";
import { useStatsStream } from "@/hooks/useStatsStream";
import { formatBytes, formatSpeed } from "../lib/format";

export function Dashboard() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const { rates } = useStatsStream();

  useEffect(() => {
    const load = () => {
      api.overview().then(setOverview).catch(() => {});
    };
    load();
    const interval = setInterval(load, 10000);
    return () => clearInterval(interval);
  }, []);

  // Active connections count from SSE rates (more accurate than overview polling)
  const activeCount = rates.reduce((sum, r) => sum + r.connections, 0);

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Dashboard</h1>
      <div className="grid grid-cols-3 gap-4">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm text-muted-foreground">Active Connections</CardTitle></CardHeader>
          <CardContent><p className="text-3xl font-bold">{activeCount}</p></CardContent>
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
        <CardHeader><CardTitle>Active Devices</CardTitle></CardHeader>
        <CardContent>
          {rates.length === 0 ? (
            <p style={{ color: "#888" }}>No active connections</p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
              {rates
                .sort((a, b) => a.device_id - b.device_id)
                .map((device) => (
                  <div
                    key={device.device_id}
                    style={{
                      border: "1px solid #e5e7eb",
                      borderRadius: 8,
                      padding: 16,
                    }}
                  >
                    <div style={{ display: "flex", justifyContent: "space-between", marginBottom: 8 }}>
                      <div>
                        <strong>{device.device_name}</strong>
                        <span style={{ color: "#888", marginLeft: 8, fontSize: 13 }}>
                          {device.user_name}
                        </span>
                        {device.version && (
                          <span style={{ color: "#888", marginLeft: 8, fontSize: 12 }}>
                            v{device.version}
                          </span>
                        )}
                      </div>
                      <span style={{ color: "#888", fontSize: 13 }}>
                        {formatBytes(device.total_bytes)} total · {device.connections} conn
                        {device.raw_conns > 0 && (
                          <span style={{ marginLeft: 8, color: "#f59e0b", fontWeight: 600 }}>
                            {device.raw_conns} raw
                          </span>
                        )}
                        {device.tls_conns > 0 && (
                          <span style={{ marginLeft: 4, color: "#16a34a" }}>
                            {device.tls_conns} TLS
                          </span>
                        )}
                      </span>
                    </div>
                    <div style={{ display: "flex", gap: 16, marginBottom: 8, fontSize: 14 }}>
                      <span style={{ color: device.download > 0 && device.download < 500_000 ? "#ef4444" : "#16a34a" }}>↓ {formatSpeed(device.download)}</span>
                      <span style={{ color: device.upload > 0 && device.upload < 500_000 ? "#ef4444" : "#2563eb" }}>↑ {formatSpeed(device.upload)}</span>
                    </div>
                    <ResponsiveContainer width="100%" height={80}>
                      <LineChart data={device.history}>
                        <XAxis dataKey="t" hide />
                        <YAxis hide />
                        <Tooltip
                          formatter={(value, name) =>
                            [formatSpeed(Number(value)), name === "down" ? "Download" : "Upload"]
                          }
                          labelFormatter={() => ""}
                        />
                        <Line type="monotone" dataKey="down" stroke="#16a34a" strokeWidth={1.5} dot={false} isAnimationActive={false} />
                        <Line type="monotone" dataKey="up" stroke="#2563eb" strokeWidth={1.5} dot={false} isAnimationActive={false} />
                      </LineChart>
                    </ResponsiveContainer>
                  </div>
                ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
```

Key changes: `useStatsStream()` hook replaces `setInterval` polling for rates. Overview still polls every 10s (it's DB-backed, no SSE needed).

- [ ] **Step 9: Fix route paths in all pages**

In every page file that uses `<Link to="/admin/...">`, replace `/admin/` with `/`. Check:
- `admin/src/pages/Users.tsx` — links to `/admin/users/:id` → `/users/:id`
- `admin/src/pages/Sites.tsx` — links to `/admin/sites/:id` → `/sites/:id`
- Any other internal navigation links

```bash
cd admin && grep -rn '"/admin/' src/pages/ src/components/
```

Replace all matches: `/admin/` → `/`.

- [ ] **Step 10: Remove unused assets**

```bash
rm -f admin/src/assets/react.svg admin/src/assets/vite.svg
```

These are Vite starter template leftovers.

- [ ] **Step 11: Install deps and verify build**

```bash
cd admin && npm install && npm run build
```

Expected: builds to `dist/`, no errors. Check `ls dist/` — should contain `index.html`, `assets/` with JS/CSS.

- [ ] **Step 12: Commit**

```bash
git add admin/
git commit -m "feat(admin): standalone React admin app with SSE stats"
```

## Task 4: Admin Dockerfile and nginx config

**Files:**
- Create: `admin/nginx.conf`
- Create: `admin/Dockerfile`
- Create: `admin/VERSION`

- [ ] **Step 1: Create nginx.conf**

Create `admin/nginx.conf`:

```nginx
server {
    listen 80;
    root /usr/share/nginx/html;
    index index.html;

    # SPA fallback
    location / {
        try_files $uri /index.html;
    }

    # Cache static assets
    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    gzip on;
    gzip_types text/html text/css application/javascript application/json image/svg+xml;
    gzip_min_length 256;
}
```

nginx listens on port 80 internally. TLS is handled at the host level (SNI router + certbot).

- [ ] **Step 2: Create Dockerfile**

Create `admin/Dockerfile`:

```dockerfile
# Build React app
FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# Serve with nginx
FROM nginx:alpine
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
```

- [ ] **Step 3: Create VERSION file**

```bash
echo "1.0.0" > admin/VERSION
```

- [ ] **Step 4: Test Docker build locally**

```bash
cd admin && docker build -t proxyness-admin:local .
```

Expected: builds clean. Verify:

```bash
docker run --rm -p 8080:80 proxyness-admin:local &
sleep 2
curl -s http://localhost:8080/ | head -5
docker stop $(docker ps -q --filter ancestor=proxyness-admin:local)
```

Expected: HTML with `<div id="root">`.

- [ ] **Step 5: Commit**

```bash
git add admin/Dockerfile admin/nginx.conf admin/VERSION
git commit -m "chore(admin): Dockerfile (node build + nginx) and VERSION"
```

## Task 5: Create deploy-admin.yml workflow

**Files:**
- Create: `.github/workflows/deploy-admin.yml`

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/deploy-admin.yml`:

```yaml
name: Deploy Admin

on:
  push:
    tags: ["admin-*"]
  workflow_dispatch:

env:
  REGISTRY: ghcr.io
  IMAGE_NAME: ${{ github.repository }}-admin

jobs:
  build-and-push:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    steps:
      - uses: actions/checkout@v4

      - name: Log in to Container Registry
        uses: docker/login-action@v3
        with:
          registry: ${{ env.REGISTRY }}
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}

      - name: Build and push Docker image
        uses: docker/build-push-action@v6
        with:
          context: ./admin
          push: true
          build-args: |
            VITE_API_URL=https://proxyness.smurov.com
          tags: |
            ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:latest
            ${{ env.REGISTRY }}/${{ env.IMAGE_NAME }}:${{ github.ref_name }}

  deploy:
    needs: build-and-push
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Deploy to Aeza
        uses: appleboy/ssh-action@v1
        with:
          host: ${{ secrets.VPS_HOST }}
          username: ${{ secrets.VPS_USER }}
          password: ${{ secrets.VPS_PASSWORD }}
          script: |
            echo "${{ secrets.GHCR_TOKEN }}" | docker login ghcr.io -u ${{ secrets.GHCR_USER }} --password-stdin
            docker pull ghcr.io/${{ github.repository }}-admin:latest
            docker stop proxyness-admin 2>/dev/null || true
            docker rm proxyness-admin 2>/dev/null || true
            docker run -d \
              --name proxyness-admin \
              --restart unless-stopped \
              -p 8080:80 \
              ghcr.io/${{ github.repository }}-admin:latest
```

Admin runs on port 8080 internally. The host-level SNI router (Task 7) will forward `admin.proxyness.smurov.com:443` → TLS-terminating nginx → localhost:8080.

Note: `VITE_API_URL` is a build-time env var. We pass it as a Docker build-arg. In the Dockerfile, we need to pick it up. Update the Dockerfile builder stage to accept it:

- [ ] **Step 2: Update admin/Dockerfile to accept VITE_API_URL build arg**

In `admin/Dockerfile`, add the build arg before `npm run build`:

Replace the builder stage with:

```dockerfile
# Build React app
FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
ARG VITE_API_URL=https://proxyness.smurov.com
ENV VITE_API_URL=$VITE_API_URL
RUN npm run build
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/deploy-admin.yml admin/Dockerfile
git commit -m "ci(admin): deploy workflow (admin-* tags, Aeza only)"
```

## Task 6: Update deploy.yml for Aeza SNI routing

On Aeza, port 443 TCP will be handled by the host nginx SNI router. The proxy container's TCP port mapping changes from `443:443` to `4430:443`. UDP stays on `443:443/udp` (SNI is TCP-only; UDP goes directly to the container).

**Files:**
- Modify: `.github/workflows/deploy.yml`

- [ ] **Step 1: Change Aeza port mapping**

In `deploy-aeza` job, change the `docker run` command. Replace:

```yaml
              -p 443:443 \
              -p 443:443/udp \
```

with:

```yaml
              -p 4430:443 \
              -p 443:443/udp \
```

Timeweb stays unchanged (no SNI router, no admin container).

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/deploy.yml
git commit -m "chore(ci): map Aeza proxy TCP to port 4430 for SNI routing"
```

## Task 7: Set up SNI router and SSL on Aeza

All ops are done via SSH to Aeza (95.181.162.242). This task sets up:
1. nginx with stream module as SNI-based TCP router on port 443
2. Let's Encrypt cert for `admin.proxyness.smurov.com`
3. TLS-terminating nginx for admin on port 8443

**Files:** (remote host only, no repo changes)

- [ ] **Step 1: Add DNS record**

Add an A record: `admin.proxyness.smurov.com` → `95.181.162.242`

Verify propagation:
```bash
dig +short admin.proxyness.smurov.com
```
Expected: `95.181.162.242`. May take up to 5 minutes.

- [ ] **Step 2: Install nginx on Aeza host**

```bash
sshpass -e ssh root@95.181.162.242 'apt update && apt install -y nginx && nginx -v'
```

Expected: `nginx/1.x.x`. If already installed, skip.

- [ ] **Step 3: Stop default nginx (if running) to free port 80 for certbot**

```bash
sshpass -e ssh root@95.181.162.242 'systemctl stop nginx 2>/dev/null; ss -tlnp | grep -E ":80 |:443 "'
```

Expected: port 80 free (only proxy container on 443). If the landing container holds port 80: `docker stop proxyness-landing 2>/dev/null || true`.

- [ ] **Step 4: Get Let's Encrypt cert for admin domain**

```bash
sshpass -e ssh root@95.181.162.242 'apt install -y certbot && certbot certonly --standalone -d admin.proxyness.smurov.com --non-interactive --agree-tos -m is@oneclick.life'
```

Expected: cert saved to `/etc/letsencrypt/live/admin.proxyness.smurov.com/`. Verify:

```bash
sshpass -e ssh root@95.181.162.242 'ls /etc/letsencrypt/live/admin.proxyness.smurov.com/'
```

Expected: `cert.pem  chain.pem  fullchain.pem  privkey.pem  README`.

- [ ] **Step 5: Write SNI router config**

```bash
sshpass -e ssh root@95.181.162.242 'cat > /etc/nginx/nginx.conf <<'\''EOF'\''
user www-data;
worker_processes auto;
pid /run/nginx.pid;
include /etc/nginx/modules-enabled/*.conf;

events {
    worker_connections 1024;
}

# SNI-based TCP routing on port 443
stream {
    map $ssl_preread_server_name $backend {
        admin.proxyness.smurov.com  127.0.0.1:8443;
        default                     127.0.0.1:4430;
    }

    server {
        listen 443;
        listen [::]:443;
        ssl_preread on;
        proxy_pass $backend;
        proxy_connect_timeout 5s;
    }
}

# TLS-terminating reverse proxy for admin dashboard
http {
    server {
        listen 8443 ssl;
        server_name admin.proxyness.smurov.com;

        ssl_certificate /etc/letsencrypt/live/admin.proxyness.smurov.com/fullchain.pem;
        ssl_certificate_key /etc/letsencrypt/live/admin.proxyness.smurov.com/privkey.pem;

        location / {
            proxy_pass http://127.0.0.1:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
        }
    }

    # Landing page stays routed by the proxy server (GET /{$} → 172.17.0.1:80).
    # No separate nginx config needed for it.
}
EOF'
```

How it works:
- Port 443 TCP → stream block peeks SNI hostname
- `admin.proxyness.smurov.com` → 127.0.0.1:8443 (this same nginx, http block, terminates TLS with LE cert, proxies to admin container on 8080)
- Everything else (proxyness.smurov.com, raw IP) → 127.0.0.1:4430 (proxy container, handles its own TLS)
- UDP 443 bypasses nginx entirely (stream module is TCP-only), goes straight to proxy container's `-p 443:443/udp`

- [ ] **Step 6: Test nginx config and start**

```bash
sshpass -e ssh root@95.181.162.242 'nginx -t && systemctl enable --now nginx && systemctl status nginx --no-pager | head -10'
```

Expected: `syntax is ok`, `test is successful`, `active (running)`.

- [ ] **Step 7: Set up certbot auto-renewal**

Certbot standalone won't work for renewal (port 443 is now nginx). Switch to webroot or use the nginx plugin:

```bash
sshpass -e ssh root@95.181.162.242 'apt install -y python3-certbot-nginx && certbot renew --dry-run'
```

If dry-run fails with port conflict, configure a pre/post hook:

```bash
sshpass -e ssh root@95.181.162.242 'cat > /etc/letsencrypt/renewal-hooks/pre/stop-nginx.sh << "EOF"
#!/bin/sh
systemctl stop nginx
EOF
cat > /etc/letsencrypt/renewal-hooks/post/start-nginx.sh << "EOF"
#!/bin/sh
systemctl start nginx
EOF
chmod +x /etc/letsencrypt/renewal-hooks/pre/stop-nginx.sh /etc/letsencrypt/renewal-hooks/post/start-nginx.sh'
```

This briefly stops nginx for renewal (~5s every 90 days).

## Task 8: Redeploy proxy server on Aeza with new port mapping

Before this task: the proxy container on Aeza must be restarted with the new port mapping (`4430:443` instead of `443:443` for TCP). This is a manual step before the CI deploy.

**IMPORTANT:** Make sure you (the user) are on Amnezia WG before this step — the proxy will be briefly down during the container restart.

- [ ] **Step 1: Restart proxy container with new mapping**

```bash
sshpass -e ssh root@95.181.162.242 '
  docker stop -t 5 proxyness 2>/dev/null || true
  docker rm proxyness 2>/dev/null || true
  source /etc/proxyness/docker-env 2>/dev/null || true
  docker run -d \
    --name proxyness \
    --restart unless-stopped \
    --ulimit nofile=32768:32768 \
    -p 4430:443 \
    -p 443:443/udp \
    -v proxyness-data:/data \
    --env-file /etc/proxyness/db.env \
    -e ADMIN_USER="$(grep ADMIN_USER /etc/proxyness/admin.env 2>/dev/null | cut -d= -f2- || echo admin)" \
    -e ADMIN_PASSWORD="$(grep ADMIN_PASSWORD /etc/proxyness/admin.env 2>/dev/null | cut -d= -f2-)" \
    ghcr.io/ilyasmurov/proxyness:latest \
    -addr ":443" \
    -cert /data/cert.pem \
    -keyfile /data/key.pem \
    -config "http://172.17.0.1:8443"
'
```

Note: Admin credentials need to come from somewhere. Check where they're currently stored. If they're only in GitHub Secrets, save them to `/etc/proxyness/admin.env` first. If the current container is already running, extract them:

```bash
sshpass -e ssh root@95.181.162.242 'docker inspect proxyness --format "{{range .Config.Env}}{{println .}}{{end}}" | grep -E "ADMIN_USER|ADMIN_PASSWORD"'
```

- [ ] **Step 2: Verify proxy works through SNI router**

```bash
curl -sk https://proxyness.smurov.com/admin/api/stats/overview -u admin:PASSWORD
```

Expected: JSON with `total_devices`, `active_connections`, etc. The request goes: client → Aeza:443 (nginx SNI) → 127.0.0.1:4430 (proxy container).

- [ ] **Step 3: Verify the SSE endpoint**

```bash
curl -sk -N https://proxyness.smurov.com/admin/api/stats/stream -u admin:PASSWORD --max-time 5
```

Expected: SSE events like `event: active\ndata: [...]`.

## Task 9: Deploy admin container

- [ ] **Step 1: Build and run admin container manually (first time)**

```bash
sshpass -e ssh root@95.181.162.242 '
  echo "GHCR_TOKEN" | docker login ghcr.io -u ilyasmurov --password-stdin
  docker pull ghcr.io/ilyasmurov/proxyness-admin:latest 2>/dev/null || true
'
```

If the image doesn't exist yet (first deploy before CI), build locally and push:

```bash
cd admin && docker build --build-arg VITE_API_URL=https://proxyness.smurov.com -t ghcr.io/ilyasmurov/proxyness-admin:latest .
docker push ghcr.io/ilyasmurov/proxyness-admin:latest
```

Then on Aeza:

```bash
sshpass -e ssh root@95.181.162.242 '
  docker stop proxyness-admin 2>/dev/null || true
  docker rm proxyness-admin 2>/dev/null || true
  docker run -d \
    --name proxyness-admin \
    --restart unless-stopped \
    -p 8080:80 \
    ghcr.io/ilyasmurov/proxyness-admin:latest
'
```

- [ ] **Step 2: Verify admin loads**

```bash
curl -sk https://admin.proxyness.smurov.com/ | head -5
```

Expected: HTML with `<div id="root">`. The flow: browser → Aeza:443 (nginx SNI → 8443 TLS termination → 8080 admin container).

- [ ] **Step 3: Test in browser**

Open `https://admin.proxyness.smurov.com` in a browser:
1. Login form appears
2. Enter admin credentials
3. Dashboard loads with live rates via SSE
4. Navigate to Users, Sites, Notifications — all load data from proxy API

## Task 10: Bump versions, push, and tag

- [ ] **Step 1: Bump server VERSION**

```bash
echo "2.1.0" > server/VERSION
```

Minor bump: new feature (SSE + CORS), no breaking changes for clients.

- [ ] **Step 2: Final commit**

```bash
git add server/VERSION
git commit -m "chore: bump server VERSION to 2.1.0 (admin extraction)"
```

- [ ] **Step 3: Push to main**

```bash
git push
```

- [ ] **Step 4: Tag and deploy server (after confirming user is on Amnezia)**

```bash
git tag -a server-2.1.0 -m "Remove embedded admin UI, add CORS + SSE"
git push origin server-2.1.0
```

Monitor CI: Aeza deploys first (with new port mapping from updated deploy.yml), then Timeweb.

- [ ] **Step 5: Tag and deploy admin**

```bash
git tag -a admin-1.0.0 -m "Standalone admin dashboard"
git push origin admin-1.0.0
```

## Task 11: Update CLAUDE.md and cleanup

**Files:**
- Modify: `CLAUDE.md`
- Delete: `server/admin-ui/` (old embedded UI source, no longer used)

- [ ] **Step 1: Delete old admin-ui directory**

```bash
rm -rf server/admin-ui/
```

- [ ] **Step 2: Update CLAUDE.md**

Update the following sections:

**In "### Server" section:** Replace mention of "Admin UI (`server/admin-ui/`) is a React/Vite app compiled and embedded into the server binary via Go embed" with: "Admin API endpoints remain in the server binary (REST + SSE), but the UI is a separate service (see Admin Dashboard below)."

**Add new section "### Admin Dashboard (`admin/`)":**
```
Standalone React 19 + Vite + TypeScript SPA served by nginx. Deployed as its own container on Aeza at `admin.proxyness.smurov.com`. Communicates with proxy server exclusively via REST API + SSE (no direct DB access).

- `src/lib/api.ts` — fetch wrapper, injects Basic Auth, base URL from `VITE_API_URL`
- `src/hooks/useStatsStream.ts` — SSE client for live stats (fetch+ReadableStream, not EventSource, because native EventSource doesn't support custom headers)
- `src/lib/auth.ts` — credentials in sessionStorage, login form on missing credentials
- Pages: Dashboard (SSE-driven), Users, UserDetail, Sites, SiteDetail, Notifications, Releases, Changelog, Logs
```

**In "## Deployment" table:** Add row:
```
| Admin dashboard | `admin-*` | `admin-1.0.0` | `deploy-admin.yml` | `admin/VERSION` |
```

**In "## Deployment" details:** Add:
```
- **Admin dashboard**: nginx container on Aeza only. Host nginx SNI-routes `admin.proxyness.smurov.com:443` → TLS termination (Let's Encrypt) → admin container on 8080. Image: `ghcr.io/${{ github.repository }}-admin:latest`.
```

**In "### Shared infra on Aeza":** Add bullet:
```
- **Host nginx** as SNI-based TCP router on port 443. `stream` block peeks SNI hostname: `admin.proxyness.smurov.com` → 127.0.0.1:8443 (TLS terminated by nginx `http` block, Let's Encrypt cert, proxied to admin container on 8080); everything else → 127.0.0.1:4430 (proxy container, handles its own TLS). UDP 443 bypasses nginx (stream is TCP-only). Config: `/etc/nginx/nginx.conf`. Cert renewal: certbot with pre/post hooks to stop/start nginx.
```

**In Key Design Decisions:** Add bullet about SSE and CORS design.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "docs: admin extraction — update CLAUDE.md, remove old admin-ui"
```

---

## Done criteria

- `https://admin.proxyness.smurov.com` — login form → dashboard with live SSE rates → all pages functional
- `https://proxyness.smurov.com/admin/api/stats/stream` — returns SSE events with Basic Auth
- CORS headers present on admin API responses (check `Access-Control-Allow-Origin`)
- Proxy server no longer serves embedded UI (no `/admin/` HTML, only API)
- Landing page still accessible at `https://proxyness.smurov.com/` (via SNI router + landing container)
- Client (Electron) connects normally through both VPSs
- `server/admin-ui/` deleted from repo
- CLAUDE.md updated
- Deploy tags: `server-2.1.0`, `admin-1.0.0`
