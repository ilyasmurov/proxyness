import { useEffect, useState } from "react";
import { LineChart, Line, XAxis, YAxis, ResponsiveContainer, Tooltip } from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { Overview, DeviceRate } from "@/lib/api";
import { formatBytes, formatSpeed } from "../lib/format";

export function Dashboard() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [rates, setRates] = useState<DeviceRate[]>([]);

  useEffect(() => {
    const load = () => {
      api.overview().then(setOverview).catch(() => {});
      api.rate().then(setRates).catch(() => {});
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
                      <span style={{ color: "#16a34a" }}>↓ {formatSpeed(device.download)}</span>
                      <span style={{ color: "#2563eb" }}>↑ {formatSpeed(device.upload)}</span>
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
                        <Line
                          type="monotone"
                          dataKey="down"
                          stroke="#16a34a"
                          strokeWidth={1.5}
                          dot={false}
                          isAnimationActive={false}
                        />
                        <Line
                          type="monotone"
                          dataKey="up"
                          stroke="#2563eb"
                          strokeWidth={1.5}
                          dot={false}
                          isAnimationActive={false}
                        />
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
