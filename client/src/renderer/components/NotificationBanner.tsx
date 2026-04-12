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
  created_at?: string;
  action?: { label: string; type: string; url?: string; server?: string };
}

type DownloadState = "idle" | "downloading" | "ready";

const TYPE_PRIORITY: Record<string, number> = { migration: 0, update: 1, maintenance: 2, info: 3 };
const TYPE_COLORS: Record<string, { bg: string; border: string; accent: string; icon: string }> = {
  migration: { bg: "oklch(0.15 0.025 25)", border: "oklch(0.62 0.19 25 / 0.15)", accent: "oklch(0.62 0.19 25)", icon: "alert" },
  update: { bg: "oklch(0.15 0.02 235)", border: "oklch(0.68 0.12 235 / 0.15)", accent: "oklch(0.68 0.12 235)", icon: "download" },
  maintenance: { bg: "oklch(0.15 0.025 75)", border: "oklch(0.78 0.155 75 / 0.15)", accent: "oklch(0.78 0.155 75)", icon: "clock" },
  info: { bg: "oklch(0.155 0.016 250)", border: "oklch(0.24 0.013 250)", accent: "oklch(0.60 0.012 250)", icon: "info" },
};

const ICONS: Record<string, string> = {
  download: "M12 5v14M5 12l7 7 7-7",
  alert: "M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0zM12 9v4M12 17h.01",
  clock: "M12 2a10 10 0 100 20 10 10 0 000-20zM12 6v6l4 2",
  info: "M12 2a10 10 0 100 20 10 10 0 000-20zM12 16v-4M12 8h.01",
  check: "M20 6L9 17l-5-5",
};

function NotifIcon({ type, color }: { type: string; color: string }) {
  const d = ICONS[type] || ICONS.info;
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke={color} strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
      <path d={d} />
    </svg>
  );
}

export function NotificationBanner() {
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [dlState, setDlState] = useState<DownloadState>("idle");
  const [progress, setProgress] = useState(0);
  const [dismissedBefore, setDismissedBefore] = useState<string>(
    () => localStorage.getItem("notification-dismissed-before") || ""
  );
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

  const handleDismiss = () => {
    const now = new Date().toISOString();
    localStorage.setItem("notification-dismissed-before", now);
    setDismissedBefore(now);
  };

  // Pick highest priority notification, filtering out dismissed
  const sorted = [...notifications]
    .filter((n) => !dismissedBefore || !n.created_at || n.created_at > dismissedBefore)
    .sort((a, b) => (TYPE_PRIORITY[a.type] ?? 9) - (TYPE_PRIORITY[b.type] ?? 9));
  const notif = sorted[0];

  if (!notif && dlState === "idle") return null;

  if (dlState === "downloading") {
    const tc = TYPE_COLORS.update;
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 12px", borderRadius: 6, background: tc.bg, border: `1px solid ${tc.border}` }}>
        <NotifIcon type="download" color={tc.accent} />
        <div style={{ flex: 1, minWidth: 0 }}>
          <div style={{ fontFamily: "'Figtree', system-ui, sans-serif", fontSize: 12, fontWeight: 600, color: "oklch(0.93 0.006 250)" }}>
            Downloading update... {progress >= 0 ? `${progress}%` : `${-progress} MB`}
          </div>
          <div style={{ height: 3, background: `${tc.accent}26`, borderRadius: 2, marginTop: 6 }}>
            <div style={{ height: 3, width: `${Math.max(progress, 0)}%`, background: tc.accent, borderRadius: 2, transition: "width 0.3s" }} />
          </div>
        </div>
      </div>
    );
  }

  if (dlState === "ready") {
    const tc = TYPE_COLORS.update;
    return (
      <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "8px 12px", borderRadius: 6, background: "oklch(0.14 0.02 150)", border: "1px solid oklch(0.72 0.15 150 / 0.15)" }}>
        <NotifIcon type="check" color="oklch(0.72 0.15 150)" />
        <div style={{ flex: 1, fontFamily: "'Figtree', system-ui, sans-serif", fontSize: 12, fontWeight: 600, color: "oklch(0.93 0.006 250)" }}>
          Update ready to install
        </div>
        <button
          onClick={() => window.updater?.installUpdate()}
          style={{
            padding: "4px 12px", borderRadius: 4, cursor: "pointer",
            fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif", fontSize: 11, fontWeight: 600, letterSpacing: 0.3,
            background: "oklch(0.72 0.15 150 / 0.15)", border: "1px solid oklch(0.72 0.15 150 / 0.25)", color: "oklch(0.72 0.15 150)",
          }}
        >
          Restart & Update
        </button>
      </div>
    );
  }

  if (!notif) return null;

  const tc = TYPE_COLORS[notif.type] || TYPE_COLORS.info;

  const handleAction = () => {
    if (!notif.action) return;
    switch (notif.action.type) {
      case "update":
        setDlState("downloading");
        window.updater?.downloadUpdate();
        break;
      case "open_url":
        if (notif.action.url) {
          window.open(notif.action.url, "_blank");
        }
        break;
      case "reconnect":
        break;
    }
  };

  return (
    <div style={{
      display: "flex", alignItems: "center", gap: 10,
      padding: "8px 12px", borderRadius: 6,
      background: tc.bg, border: `1px solid ${tc.border}`,
      animation: "pn-blur-fade 0.35s cubic-bezier(0.25,1,0.5,1) both",
    }}>
      <div style={{ animation: "pn-blur-dot 0.3s cubic-bezier(0.25,1,0.5,1) 0.1s both" }}>
        <NotifIcon type={tc.icon} color={tc.accent} />
      </div>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontFamily: "'Figtree', system-ui, sans-serif", fontSize: 12, fontWeight: 600, color: "oklch(0.93 0.006 250)", animation: "pn-blur-heavy 0.4s cubic-bezier(0.25,1,0.5,1) 0.12s both" }}>
          {notif.title}
        </div>
        {notif.message && (
          <div style={{ fontFamily: "'Figtree', system-ui, sans-serif", fontSize: 11, color: "oklch(0.40 0.01 250)", marginTop: 1, animation: "pn-blur-light 0.35s cubic-bezier(0.25,1,0.5,1) 0.2s both" }}>
            {notif.message}
          </div>
        )}
      </div>
      {notif.action && (
        <button
          onClick={handleAction}
          style={{
            padding: "4px 12px", borderRadius: 4, cursor: "pointer", flexShrink: 0,
            fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif", fontSize: 11, fontWeight: 600, letterSpacing: 0.3,
            background: `${tc.accent}26`, border: `1px solid ${tc.accent}40`, color: tc.accent,
            transition: "all 0.1s", whiteSpace: "nowrap" as const,
            animation: "pn-blur-badge 0.3s cubic-bezier(0.25,1,0.5,1) 0.25s both",
          }}
        >
          {notif.action.label}
        </button>
      )}
      <button
        onClick={handleDismiss}
        style={{
          background: "none", border: "none", color: "oklch(0.40 0.01 250)",
          cursor: "pointer", fontSize: 14, padding: "0 2px", lineHeight: 1, flexShrink: 0,
          animation: "pn-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) 0.3s both",
          transition: "color 0.1s",
        }}
        onMouseEnter={(e) => { e.currentTarget.style.color = "oklch(0.93 0.006 250)"; }}
        onMouseLeave={(e) => { e.currentTarget.style.color = "oklch(0.40 0.01 250)"; }}
      >
        ×
      </button>
    </div>
  );
}
