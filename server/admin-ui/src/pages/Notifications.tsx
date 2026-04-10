import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api, type Notification, type ServiceConfigMap } from "@/lib/api";

const typeColors: Record<string, string> = {
  update: "bg-blue-500/20 text-blue-400 border-blue-500/30",
  migration: "bg-red-500/20 text-red-400 border-red-500/30",
  maintenance: "bg-yellow-500/20 text-yellow-400 border-yellow-500/30",
  info: "bg-slate-500/20 text-slate-400 border-slate-500/30",
};

type Tab = "notifications" | "services";

export function Notifications() {
  const [tab, setTab] = useState<Tab>("notifications");
  const [notifs, setNotifs] = useState<Notification[]>([]);
  const [services, setServices] = useState<ServiceConfigMap>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  // Create form
  const [newType, setNewType] = useState("info");
  const [newTitle, setNewTitle] = useState("");
  const [newMessage, setNewMessage] = useState("");
  const [newBetaOnly, setNewBetaOnly] = useState(false);
  const [newActionType, setNewActionType] = useState("none");
  const [newActionLabel, setNewActionLabel] = useState("");
  const [newActionUrl, setNewActionUrl] = useState("");
  const [newExpires, setNewExpires] = useState("7");
  const [expandedDelivery, setExpandedDelivery] = useState<string | null>(null);
  const [deliveries, setDeliveries] = useState<Record<string, import("@/lib/api").NotificationDelivery[]>>({});
  const [users, setUsers] = useState<import("@/lib/api").User[]>([]);
  const [devicesByUser, setDevicesByUser] = useState<Record<number, import("@/lib/api").Device[]>>({});

  const loadNotifs = () => {
    api.listNotifications()
      .then(setNotifs)
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
  };

  const loadServices = () => {
    api.getServices()
      .then((s) => setServices(s || {}))
      .catch((e) => setError(e.message));
  };

  useEffect(() => {
    loadNotifs();
    loadServices();
    api.listUsers().then(async (users) => {
      setUsers(users);
      const byUser: Record<number, import("@/lib/api").Device[]> = {};
      await Promise.all(users.map(async (u) => {
        byUser[u.id] = await api.listDevices(u.id);
      }));
      setDevicesByUser(byUser);
    });
  }, []);

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

  const handleToggle = async (id: string, active: boolean) => {
    await api.updateNotification(id, { active });
    loadNotifs();
  };

  const handleDelete = async (id: string) => {
    await api.deleteNotification(id);
    loadNotifs();
  };

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

  const handleSaveServices = async () => {
    try {
      await api.setServices(services);
      setError("");
    } catch (e: any) {
      setError(e.message);
    }
  };

  const tabButton = (t: Tab, label: string) => (
    <button
      onClick={() => setTab(t)}
      className={`px-4 py-2 rounded-md text-sm font-medium transition-colors ${
        tab === t ? "bg-secondary text-secondary-foreground" : "text-muted-foreground hover:text-foreground"
      }`}
    >
      {label}
    </button>
  );

  if (loading) return <p className="text-muted-foreground">Loading...</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        {tabButton("notifications", "Notifications")}
        {tabButton("services", "Services")}
      </div>

      {error && (
        <div className="text-red-400 text-sm bg-red-500/10 border border-red-500/20 rounded-md px-3 py-2">
          {error}
        </div>
      )}

      {tab === "notifications" && (
        <>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">Send Notification</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div className="flex gap-3">
                <select
                  value={newType}
                  onChange={(e) => setNewType(e.target.value)}
                  className="bg-background border border-border rounded-md px-3 py-2 text-sm"
                >
                  <option value="info">Info</option>
                  <option value="update">Update</option>
                  <option value="migration">Migration</option>
                  <option value="maintenance">Maintenance</option>
                </select>
                <Input
                  placeholder="Title"
                  value={newTitle}
                  onChange={(e) => setNewTitle(e.target.value)}
                />
              </div>
              <Input
                placeholder="Message (optional)"
                value={newMessage}
                onChange={(e) => setNewMessage(e.target.value)}
              />
              <div className="flex gap-3">
                <div className="flex-1">
                  <label className="text-xs text-muted-foreground mb-1 block">Button action</label>
                  <select
                    value={newActionType}
                    onChange={(e) => setNewActionType(e.target.value)}
                    className="bg-background border border-border rounded-md px-3 py-2 text-sm w-full"
                  >
                    <option value="none">No button</option>
                    <option value="update">Download update</option>
                    <option value="open_url">Open URL</option>
                    <option value="reconnect">Reconnect to server</option>
                  </select>
                </div>
                {newActionType !== "none" && (
                  <div className="flex-1">
                    <label className="text-xs text-muted-foreground mb-1 block">Button label</label>
                    <Input
                      placeholder="e.g. Update"
                      value={newActionLabel}
                      onChange={(e) => setNewActionLabel(e.target.value)}
                    />
                  </div>
                )}
              </div>
              {newActionType === "open_url" && (
                <Input
                  placeholder="URL (https://...)"
                  value={newActionUrl}
                  onChange={(e) => setNewActionUrl(e.target.value)}
                />
              )}
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
              <Button onClick={handleCreate} disabled={!newTitle.trim()}>
                Create
              </Button>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="text-base">All Notifications</CardTitle>
            </CardHeader>
            <CardContent>
              {notifs.length === 0 ? (
                <p className="text-muted-foreground text-sm">No notifications yet</p>
              ) : (
                <div className="space-y-2">
                  {notifs.map((n) => (
                    <div key={n.id} className="space-y-2">
                      <div className="flex items-center justify-between border border-border rounded-md px-3 py-2">
                        <div className="flex items-center gap-3 min-w-0">
                          <Badge variant="outline" className={typeColors[n.type] || typeColors.info}>
                            {n.type}
                          </Badge>
                          <Badge variant={n.active ? "default" : "secondary"} className="text-xs">
                            {n.active ? "Active" : "Off"}
                          </Badge>
                          {n.beta_only && (
                            <Badge variant="outline" className="bg-amber-500/20 text-amber-400 border-amber-500/30 text-xs">
                              Beta
                            </Badge>
                          )}
                          <button
                            onClick={() => toggleDelivery(n.id)}
                            className="text-xs text-emerald-400 bg-emerald-500/20 border border-emerald-500/30 rounded-full px-2 py-0.5 hover:bg-emerald-500/30 transition-colors"
                          >
                            Delivered: {n.delivery_count ?? 0}
                          </button>
                          <div className="truncate">
                            <span className="font-medium text-sm">{n.title}</span>
                            {n.message && <span className="text-muted-foreground text-xs ml-2">{n.message}</span>}
                            {n.action && <span className="text-blue-400 text-xs ml-2">[{n.action.type}: {n.action.label}]</span>}
                          </div>
                        </div>
                        <div className="flex gap-2 shrink-0 ml-3">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => handleToggle(n.id, !n.active)}
                          >
                            {n.active ? "Disable" : "Enable"}
                          </Button>
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => handleDelete(n.id)}
                          >
                            Delete
                          </Button>
                        </div>
                      </div>
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
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </>
      )}

      {tab === "services" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Service Config</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {Object.entries(services).map(([key, value]) => (
              <div key={key} className="flex items-center gap-3">
                <span className="text-sm font-mono text-muted-foreground w-32 shrink-0">{key}</span>
                <Input
                  value={value}
                  onChange={(e) => setServices({ ...services, [key]: e.target.value })}
                />
              </div>
            ))}
            <Button onClick={handleSaveServices}>Save</Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
