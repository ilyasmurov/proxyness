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

type SplitMode = "all" | "all_except" | "only";

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [mode, setMode] = useState<SplitMode>("all");
  const [selectedApps, setSelectedApps] = useState<string[]>([]);
  const [installedApps, setInstalledApps] = useState<{ name: string; path: string }[]>([]);

  useEffect(() => {
    if (!visible) return;
    window.tunProxy?.getRules().then((rules) => {
      const apps = rules.apps || [];
      if (rules.mode === "proxy_only") {
        setMode("only");
      } else if (apps.length > 0) {
        setMode("all_except");
      } else {
        setMode("all");
      }
      setSelectedApps(apps);
    });
    window.tunProxy?.getInstalledApps().then((apps) => {
      setInstalledApps(apps);
    });
  }, [visible]);

  const save = (m: SplitMode, apps: string[]) => {
    const daemonMode = m === "only" ? "proxy_only" : "proxy_all_except";
    const daemonApps = m === "all" ? [] : apps;
    window.tunProxy?.setRules({ mode: daemonMode, apps: daemonApps });
  };

  const handleModeChange = (m: SplitMode) => {
    setMode(m);
    save(m, selectedApps);
  };

  const toggleApp = (appPath: string) => {
    const lower = appPath.toLowerCase();
    const updated = selectedApps.includes(lower)
      ? selectedApps.filter((a) => a !== lower)
      : [...selectedApps, lower];
    setSelectedApps(updated);
    save(mode, updated);
  };

  if (!visible) return null;

  const modes: { key: SplitMode; label: string }[] = [
    { key: "all", label: "All" },
    { key: "all_except", label: "Exclude" },
    { key: "only", label: "Select" },
  ];

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>Traffic</div>

      <div style={{ display: "flex", gap: 6, marginBottom: 12 }}>
        {modes.map(({ key, label }) => (
          <button
            key={key}
            onClick={() => handleModeChange(key)}
            style={{
              flex: 1, padding: "6px 0", fontSize: 11,
              background: mode === key ? "#1a3a5c" : "transparent",
              border: `1px solid ${mode === key ? "#3b82f6" : "#333"}`,
              borderRadius: 6, color: mode === key ? "#fff" : "#888",
              cursor: "pointer",
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {mode === "all" ? (
        <div style={{ color: "#666", fontSize: 12, textAlign: "center", padding: "4px 0" }}>
          All traffic goes through proxy
        </div>
      ) : (
        <>
          <div style={{ color: "#888", fontSize: 11, marginBottom: 6 }}>
            {mode === "all_except" ? "Exclude from proxy:" : "Proxy only:"}
          </div>
          {installedApps.length > 0 ? (
            <div style={{ maxHeight: 180, overflowY: "auto" }}>
              {installedApps.map((app) => {
                const checked = selectedApps.includes(app.path.toLowerCase());
                return (
                  <label
                    key={app.path}
                    style={{
                      display: "flex", alignItems: "center", gap: 8,
                      padding: "4px 4px", fontSize: 12, color: "#ccc",
                      cursor: "pointer",
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => toggleApp(app.path)}
                      style={{ accentColor: "#3b82f6" }}
                    />
                    {app.name}
                  </label>
                );
              })}
            </div>
          ) : (
            <div style={{ color: "#555", fontSize: 12, textAlign: "center", padding: "8px 0" }}>
              No apps detected
            </div>
          )}
        </>
      )}
    </div>
  );
}
