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
};
