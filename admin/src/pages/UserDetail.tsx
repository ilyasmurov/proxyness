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
import { api } from "@/lib/api";
import type { Device, DailyTraffic } from "@/lib/api";
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
      devs.forEach((d) => api.trafficDaily(d.id).then((data) => setChartData((prev) => ({ ...prev, [d.id]: data }))).catch(() => {}));
    }).catch(() => {});
  };
  useEffect(() => { load(); }, [userId]);

  const handleCreate = async () => {
    if (!name.trim()) return;
    const dev = await api.createDevice(userId, name.trim());
    setCreatedKey(dev.key);
    setName(""); load();
  };

  const handleToggle = async (devId: number, active: boolean) => { await api.toggleDevice(devId, active); load(); };
  const handleDeleteDevice = async (devId: number) => { if (!confirm("Delete device?")) return; await api.deleteDevice(devId); load(); };
  const handleDeleteUser = async () => { if (!confirm("Delete user and ALL devices?")) return; await api.deleteUser(userId); nav("/users"); };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Devices</h1>
        <div className="flex gap-2">
          <Dialog open={open} onOpenChange={(v) => { setOpen(v); if (!v) setCreatedKey(""); }}>
            <DialogTrigger render={<Button />}>Add Device</DialogTrigger>
            <DialogContent>
              <DialogHeader><DialogTitle>{createdKey ? "Device Created" : "New Device"}</DialogTitle></DialogHeader>
              {createdKey ? (
                <div className="space-y-4">
                  <p className="text-sm text-muted-foreground">Device key:</p>
                  <code className="block p-3 bg-muted rounded text-xs break-all select-all">{createdKey}</code>
                  <Button onClick={() => navigator.clipboard.writeText(createdKey)} className="w-full">Copy Key</Button>
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
            <TableHeader><TableRow>
              <TableHead>Device</TableHead><TableHead>Key</TableHead><TableHead>Status</TableHead><TableHead>Created</TableHead><TableHead>Active</TableHead><TableHead></TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {devices.map((d) => (
                <TableRow key={d.id}>
                  <TableCell className="font-medium">{d.name}</TableCell>
                  <TableCell>
                    <div className="flex items-center gap-1">
                      <code className="text-xs text-muted-foreground">{d.key.slice(0, 8)}…</code>
                      <Button variant="ghost" size="sm" className="h-6 px-1.5 text-xs" onClick={() => navigator.clipboard.writeText(d.key)}>Copy</Button>
                    </div>
                  </TableCell>
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
                  <YAxis tick={{ fontSize: 12 }} tickFormatter={(v: number) => formatBytes(v)} />
                  <Tooltip formatter={(v) => formatBytes(Number(v))} />
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
