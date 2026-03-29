import { useState, useEffect } from "react";

declare global {
  interface Window {
    tunProxy?: {
      start: (server: string, key: string) => void;
      stop: () => void;
      getStatus: () => Promise<{ status: string }>;
      getRules: () => Promise<{ mode: string; apps: string[] }>;
      setRules: (rules: { mode: string; apps: string[] }) => void;
    };
  }
}

type RuleMode = "proxy_all_except" | "proxy_only";

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [mode, setMode] = useState<RuleMode>("proxy_all_except");
  const [apps, setApps] = useState<string[]>([]);
  const [newApp, setNewApp] = useState("");

  useEffect(() => {
    if (!visible) return;
    window.tunProxy?.getRules().then((rules) => {
      setMode((rules.mode as RuleMode) || "proxy_all_except");
      setApps(rules.apps || []);
    });
  }, [visible]);

  const save = (m: RuleMode, a: string[]) => {
    window.tunProxy?.setRules({ mode: m, apps: a });
  };

  const handleModeChange = (m: RuleMode) => {
    setMode(m);
    save(m, apps);
  };

  const addApp = () => {
    const trimmed = newApp.trim();
    if (!trimmed || apps.includes(trimmed)) return;
    const updated = [...apps, trimmed];
    setApps(updated);
    setNewApp("");
    save(mode, updated);
  };

  const removeApp = (app: string) => {
    const updated = apps.filter((a) => a !== app);
    setApps(updated);
    save(mode, updated);
  };

  if (!visible) return null;

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>Split Tunneling</div>

      <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
        <button
          onClick={() => handleModeChange("proxy_all_except")}
          style={{
            flex: 1, padding: "6px 0", fontSize: 11,
            background: mode === "proxy_all_except" ? "#1a3a5c" : "transparent",
            border: `1px solid ${mode === "proxy_all_except" ? "#3b82f6" : "#333"}`,
            borderRadius: 6, color: mode === "proxy_all_except" ? "#fff" : "#888",
            cursor: "pointer",
          }}
        >
          Proxy all except...
        </button>
        <button
          onClick={() => handleModeChange("proxy_only")}
          style={{
            flex: 1, padding: "6px 0", fontSize: 11,
            background: mode === "proxy_only" ? "#1a3a5c" : "transparent",
            border: `1px solid ${mode === "proxy_only" ? "#3b82f6" : "#333"}`,
            borderRadius: 6, color: mode === "proxy_only" ? "#fff" : "#888",
            cursor: "pointer",
          }}
        >
          Proxy only...
        </button>
      </div>

      {apps.map((app) => (
        <div key={app} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "4px 0", fontSize: 12, color: "#ccc" }}>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 260 }}>{app.split("/").pop()}</span>
          <button
            onClick={() => removeApp(app)}
            style={{ background: "transparent", border: "none", color: "#666", cursor: "pointer", fontSize: 14 }}
          >
            x
          </button>
        </div>
      ))}

      <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
        <input
          value={newApp}
          onChange={(e) => setNewApp(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && addApp()}
          placeholder="App path..."
          style={{
            flex: 1, padding: "6px 8px", background: "#16213e",
            border: "1px solid #333", borderRadius: 6, color: "#eee", fontSize: 12,
          }}
        />
        <button
          onClick={addApp}
          style={{
            padding: "6px 12px", background: "#3b82f6", color: "#fff",
            border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer",
          }}
        >
          Add
        </button>
      </div>
    </div>
  );
}
