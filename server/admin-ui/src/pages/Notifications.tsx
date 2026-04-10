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
  }, []);

  const handleCreate = async () => {
    if (!newTitle.trim()) return;
    try {
      await api.createNotification({ type: newType, title: newTitle.trim(), message: newMessage.trim() || undefined });
      setNewTitle("");
      setNewMessage("");
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
                    <div key={n.id} className="flex items-center justify-between border border-border rounded-md px-3 py-2">
                      <div className="flex items-center gap-3 min-w-0">
                        <Badge variant="outline" className={typeColors[n.type] || typeColors.info}>
                          {n.type}
                        </Badge>
                        <Badge variant={n.active ? "default" : "secondary"} className="text-xs">
                          {n.active ? "Active" : "Off"}
                        </Badge>
                        <div className="truncate">
                          <span className="font-medium text-sm">{n.title}</span>
                          {n.message && <span className="text-muted-foreground text-xs ml-2">{n.message}</span>}
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
