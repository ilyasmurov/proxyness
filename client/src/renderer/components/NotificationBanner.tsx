import { useState, useEffect, useRef } from "react";

declare global {
  interface Window {
    updater?: {
      downloadUpdate: () => void;
      installUpdate: () => void;
      onUpdateProgress: (cb: (percent: number) => void) => void;
      onUpdateDownloaded: (cb: () => void) => void;
      onUpdateError: (cb: () => void) => void;
      onConfigUpdated: (cb: (config: any) => void) => void;
      getConfig: () => Promise<any>;
      storeKey: (key: string) => void;
    };
  }
}

interface Notification {
  id: string;
  type: "update" | "migration" | "maintenance" | "info";
  title: string;
  message?: string;
  action?: { label: string; type: string; url?: string; server?: string };
}

type DownloadState = "idle" | "downloading" | "ready";

const TYPE_PRIORITY: Record<string, number> = { migration: 0, update: 1, maintenance: 2, info: 3 };
const TYPE_COLORS: Record<string, { bg: string; border: string }> = {
  migration: { bg: "#2d1b1b", border: "#7f1d1d" },
  update: { bg: "#1a2744", border: "#2a4a7a" },
  maintenance: { bg: "#2d2006", border: "#78520a" },
  info: { bg: "#1a2234", border: "#2a3a5a" },
};

export function NotificationBanner() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [dlState, setDlState] = useState<DownloadState>("idle");
  const [progress, setProgress] = useState(0);
  const dlStateRef = useRef(dlState);
  dlStateRef.current = dlState;

  useEffect(() => {
    if (!window.updater) return;

    // Load initial config
    window.updater.getConfig().then((cfg) => {
      if (cfg?.notifications) setNotifications(cfg.notifications);
    });

    // Listen for config updates from main process poller
    window.updater.onConfigUpdated((cfg) => {
      if (cfg?.notifications) {
        if (dlStateRef.current !== "idle") return;
        setNotifications(cfg.notifications);
      }
    });

    // Download progress handlers
    window.updater.onUpdateProgress((p) => setProgress(p < 0 ? Math.round(-p / 1024 / 1024) : p));
    window.updater.onUpdateDownloaded(() => setDlState("ready"));
    window.updater.onUpdateError(() => setDlState("idle"));
  }, []);

  // Pick highest priority notification
  const sorted = [...notifications].sort((a, b) => (TYPE_PRIORITY[a.type] ?? 9) - (TYPE_PRIORITY[b.type] ?? 9));
  const notif = sorted[0];

  if (!notif && dlState === "idle") return null;

  if (dlState === "downloading") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, background: "#1a2744", border: "1px solid #2a4a7a", borderRadius: 8, fontSize: 13 }}>
        <div style={{ marginBottom: 4 }}>Downloading... {progress >= 0 ? `${progress}%` : `${-progress} MB`}</div>
        <div style={{ height: 4, background: "#333", borderRadius: 2 }}>
          <div style={{ height: 4, width: `${Math.max(progress, 0)}%`, background: "#3b82f6", borderRadius: 2 }} />
        </div>
      </div>
    );
  }

  if (dlState === "ready") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, background: "#0f3d1a", border: "1px solid #166534", borderRadius: 8, fontSize: 13, display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>Update ready</span>
        <button onClick={() => window.updater?.installUpdate()} style={{ padding: "4px 12px", background: "#22c55e", color: "#fff", border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer" }}>
          Restart & Update
        </button>
      </div>
    );
  }

  if (!notif) return null;

  const colors = TYPE_COLORS[notif.type] || TYPE_COLORS.info;

  const handleAction = () => {
    if (!notif.action) return;
    switch (notif.action.type) {
      case "update":
        setDlState("downloading");
        window.updater?.downloadUpdate();
        break;
      case "open_url":
        if (notif.action.url) {
          // Use shell.openExternal via the main process
          window.open(notif.action.url, "_blank");
        }
        break;
      case "reconnect":
        // Future: reconnect to new server address
        break;
    }
  };

  return (
    <div style={{ padding: "10px 12px", marginBottom: 16, background: colors.bg, border: `1px solid ${colors.border}`, borderRadius: 8, fontSize: 13 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <div>
          <div style={{ fontWeight: 600, marginBottom: notif.message ? 4 : 0 }}>{notif.title}</div>
          {notif.message && <div style={{ color: "#94a3b8", fontSize: 12 }}>{notif.message}</div>}
        </div>
        {notif.action && (
          <button onClick={handleAction} style={{ padding: "4px 12px", background: "#3b82f6", color: "#fff", border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer", whiteSpace: "nowrap", marginLeft: 12 }}>
            {notif.action.label}
          </button>
        )}
      </div>
    </div>
  );
}
