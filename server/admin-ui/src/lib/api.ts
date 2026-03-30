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
  history: Array<{ t: number; down: number; up: number }>;
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
};
