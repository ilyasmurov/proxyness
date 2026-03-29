import { useEffect, useState, useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Overview, ActiveConn } from "@/lib/api";
import { formatBytes, formatDuration } from "@/lib/format";

interface DeviceGroup {
  device_name: string;
  user_name: string;
  connections: ActiveConn[];
  total_in: number;
  total_out: number;
}

export function Dashboard() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [active, setActive] = useState<ActiveConn[]>([]);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    const load = () => {
      api.overview().then(setOverview).catch(() => {});
      api.activeConns().then(setActive).catch(() => {});
    };
    load();
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, []);

  const groups = useMemo(() => {
    const map = new Map<string, DeviceGroup>();
    for (const c of active) {
      const key = `${c.device_name}::${c.user_name}`;
      let g = map.get(key);
      if (!g) {
        g = { device_name: c.device_name, user_name: c.user_name, connections: [], total_in: 0, total_out: 0 };
        map.set(key, g);
      }
      g.connections.push(c);
      g.total_in += c.bytes_in;
      g.total_out += c.bytes_out;
    }
    return Array.from(map.values());
  }, [active]);

  const toggle = (key: string) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });
  };

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
          {groups.length === 0 ? (
            <p className="text-muted-foreground text-sm">No active connections</p>
          ) : (
            <Table>
              <TableHeader><TableRow>
                <TableHead>Device</TableHead><TableHead>User</TableHead>
                <TableHead>Connections</TableHead><TableHead>In</TableHead><TableHead>Out</TableHead>
              </TableRow></TableHeader>
              <TableBody>
                {groups.map((g) => {
                  const key = `${g.device_name}::${g.user_name}`;
                  const isOpen = expanded.has(key);
                  return (
                    <>
                      <TableRow
                        key={key}
                        className="cursor-pointer hover:bg-muted/50"
                        onClick={() => toggle(key)}
                      >
                        <TableCell className="font-medium">
                          <span className="mr-2 inline-block w-4 text-muted-foreground">{isOpen ? "▾" : "▸"}</span>
                          {g.device_name}
                        </TableCell>
                        <TableCell>{g.user_name}</TableCell>
                        <TableCell>{g.connections.length}</TableCell>
                        <TableCell>{formatBytes(g.total_in)}</TableCell>
                        <TableCell>{formatBytes(g.total_out)}</TableCell>
                      </TableRow>
                      {isOpen && g.connections.map((c, i) => (
                        <TableRow key={`${key}-${i}`} className="bg-muted/20">
                          <TableCell className="pl-10 text-muted-foreground text-xs">connection #{i + 1}</TableCell>
                          <TableCell></TableCell>
                          <TableCell className="text-xs">{formatDuration(c.started_at)}</TableCell>
                          <TableCell className="text-xs">{formatBytes(c.bytes_in)}</TableCell>
                          <TableCell className="text-xs">{formatBytes(c.bytes_out)}</TableCell>
                        </TableRow>
                      ))}
                    </>
                  );
                })}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
