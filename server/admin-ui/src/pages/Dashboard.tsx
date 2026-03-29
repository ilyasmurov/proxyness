import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Overview, ActiveConn } from "@/lib/api";
import { formatBytes, formatDuration } from "@/lib/format";

export function Dashboard() {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [active, setActive] = useState<ActiveConn[]>([]);

  useEffect(() => {
    const load = () => {
      api.overview().then(setOverview).catch(() => {});
      api.activeConns().then(setActive).catch(() => {});
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
            <p className="text-muted-foreground text-sm">No active connections</p>
          ) : (
            <Table>
              <TableHeader><TableRow>
                <TableHead>Device</TableHead><TableHead>User</TableHead>
                <TableHead>Duration</TableHead><TableHead>In</TableHead><TableHead>Out</TableHead>
              </TableRow></TableHeader>
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
