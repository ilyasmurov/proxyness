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

type SplitMode = "all" | "only";

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [mode, setMode] = useState<SplitMode>("all");
  const [selectedApps, setSelectedApps] = useState<string[]>([]);
  const [installedApps, setInstalledApps] = useState<{ name: string; path: string }[]>([]);
  const [search, setSearch] = useState("");

  useEffect(() => {
    if (!visible) return;
    window.tunProxy?.getRules().then((rules) => {
      const apps = rules.apps || [];
      setMode(rules.mode === "proxy_only" ? "only" : "all");
      setSelectedApps(apps);
    });
    window.tunProxy?.getInstalledApps().then((apps) => {
      setInstalledApps(apps);
    });
  }, [visible]);

  const save = (m: SplitMode, apps: string[]) => {
    window.tunProxy?.setRules({
      mode: m === "only" ? "proxy_only" : "proxy_all_except",
      apps: m === "all" ? [] : apps,
    });
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

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>Traffic</div>

      <div style={{ display: "flex", gap: 6, marginBottom: 12 }}>
        {([["all", "All traffic"], ["only", "Selected apps"]] as const).map(([key, label]) => (
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
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search apps..."
            style={{
              width: "100%", padding: "6px 8px", marginBottom: 8,
              background: "#16213e", border: "1px solid #333",
              borderRadius: 6, color: "#eee", fontSize: 12,
            }}
          />
          {installedApps.length > 0 ? (
            <div style={{ maxHeight: 180, overflowY: "auto" }}>
              {installedApps
                .filter((app) => app.name.toLowerCase().includes(search.toLowerCase()))
                .map((app) => {
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
