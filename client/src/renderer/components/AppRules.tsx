import { useState, useEffect } from "react";

declare global {
  interface Window {
    tunProxy?: {
      start: (server: string, key: string) => void;
      stop: () => void;
      getStatus: () => Promise<{ status: string }>;
      getRules: () => Promise<{ mode: string; apps: string[] }>;
      setRules: (rules: { mode: string; apps: string[] }) => void;
      getInstalledApps: () => Promise<{ name: string; path: string }[]>;
    };
  }
}

interface KnownApp {
  id: string;
  name: string;
  color: string;
  letter: string;
  keywords: string[];
}

const KNOWN_APPS: KnownApp[] = [
  { id: "telegram", name: "Telegram", color: "#27A7E7", letter: "T", keywords: ["telegram"] },
  { id: "discord", name: "Discord", color: "#5865F2", letter: "D", keywords: ["discord"] },
  { id: "browsers", name: "Browsers", color: "#4285F4", letter: "B", keywords: ["chrome", "firefox", "edge", "safari", "opera", "brave", "yandex", "browser"] },
  { id: "claude", name: "Claude Code", color: "#D97757", letter: "C", keywords: ["claude"] },
  { id: "cursor", name: "Cursor", color: "#00D1FF", letter: "Cu", keywords: ["cursor"] },
  { id: "slack", name: "Slack", color: "#E01E5A", letter: "S", keywords: ["slack"] },
];

interface ResolvedApp {
  app: KnownApp;
  paths: string[]; // matched installed paths
}

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [resolved, setResolved] = useState<ResolvedApp[]>([]);
  const [enabled, setEnabled] = useState<Set<string>>(new Set(KNOWN_APPS.map((a) => a.id)));

  useEffect(() => {
    if (!visible) return;

    // Load installed apps and match against known list
    window.tunProxy?.getInstalledApps().then((installed) => {
      const results: ResolvedApp[] = [];
      for (const app of KNOWN_APPS) {
        const paths: string[] = [];
        for (const inst of installed) {
          const lower = (inst.name + " " + inst.path).toLowerCase();
          if (app.keywords.some((kw) => lower.includes(kw))) {
            paths.push(inst.path.toLowerCase());
          }
        }
        if (paths.length > 0) {
          results.push({ app, paths });
        }
      }
      setResolved(results);
    });

    // Load saved rules
    window.tunProxy?.getRules().then((rules) => {
      if (rules.mode === "proxy_only" && rules.apps?.length > 0) {
        // Figure out which known apps are enabled from saved paths
        const savedPaths = new Set(rules.apps.map((a) => a.toLowerCase()));
        const enabledIds = new Set<string>();
        for (const app of KNOWN_APPS) {
          // Check if any keyword matches any saved path
          for (const sp of savedPaths) {
            if (app.keywords.some((kw) => sp.includes(kw))) {
              enabledIds.add(app.id);
              break;
            }
          }
        }
        setEnabled(enabledIds);
      }
    });
  }, [visible]);

  const toggle = (appId: string) => {
    setEnabled((prev) => {
      const next = new Set(prev);
      if (next.has(appId)) {
        next.delete(appId);
      } else {
        next.add(appId);
      }

      // Collect all paths for enabled apps
      const paths: string[] = [];
      for (const r of resolved) {
        if (next.has(r.app.id)) {
          paths.push(...r.paths);
        }
      }
      window.tunProxy?.setRules({ mode: "proxy_only", apps: paths });

      return next;
    });
  };

  if (!visible) return null;

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 10 }}>Proxy apps</div>

      {resolved.length === 0 ? (
        <div style={{ color: "#555", fontSize: 12, textAlign: "center", padding: "8px 0" }}>
          Scanning apps...
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {resolved.map(({ app }) => {
            const isOn = enabled.has(app.id);
            return (
              <div
                key={app.id}
                onClick={() => toggle(app.id)}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 10,
                  padding: "6px 8px",
                  borderRadius: 6,
                  cursor: "pointer",
                  background: isOn ? "rgba(59,130,246,0.08)" : "transparent",
                }}
              >
                <div
                  style={{
                    width: 28,
                    height: 28,
                    borderRadius: 6,
                    background: isOn ? app.color : "#333",
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "center",
                    fontSize: 12,
                    fontWeight: 700,
                    color: isOn ? "#fff" : "#666",
                    flexShrink: 0,
                  }}
                >
                  {app.letter}
                </div>
                <div style={{ flex: 1, fontSize: 13, color: isOn ? "#eee" : "#666" }}>
                  {app.name}
                </div>
                <div
                  style={{
                    width: 36,
                    height: 20,
                    borderRadius: 10,
                    background: isOn ? "#3b82f6" : "#333",
                    position: "relative",
                    transition: "background 0.2s",
                  }}
                >
                  <div
                    style={{
                      width: 16,
                      height: 16,
                      borderRadius: 8,
                      background: "#fff",
                      position: "absolute",
                      top: 2,
                      left: isOn ? 18 : 2,
                      transition: "left 0.2s",
                    }}
                  />
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
