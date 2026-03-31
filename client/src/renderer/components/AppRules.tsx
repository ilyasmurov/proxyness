import { useState, useEffect, useCallback } from "react";

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
  browser?: boolean;
}

const KNOWN_APPS: KnownApp[] = [
  // Browsers (control SOCKS5 system proxy)
  { id: "chrome", name: "Google Chrome", color: "#4285F4", letter: "G", keywords: ["chrome", "google chrome"], browser: true },
  { id: "safari", name: "Safari", color: "#006CFF", letter: "Sa", keywords: ["safari"], browser: true },
  { id: "firefox", name: "Firefox", color: "#FF7139", letter: "F", keywords: ["firefox"], browser: true },
  { id: "edge", name: "Microsoft Edge", color: "#0078D7", letter: "E", keywords: ["edge"], browser: true },
  { id: "yandex", name: "Yandex Browser", color: "#FC3F1D", letter: "Y", keywords: ["yandex"], browser: true },
  { id: "opera", name: "Opera", color: "#FF1B2D", letter: "O", keywords: ["opera"], browser: true },
  { id: "arc", name: "Arc", color: "#FCBFBD", letter: "A", keywords: ["arc"], browser: true },
  // Apps (control TUN routing)
  { id: "telegram", name: "Telegram", color: "#27A7E7", letter: "T", keywords: ["telegram"] },
  { id: "discord", name: "Discord", color: "#5865F2", letter: "D", keywords: ["discord"] },
  { id: "claude", name: "Claude Code", color: "#D97757", letter: "C", keywords: ["claude"] },
  { id: "cursor", name: "Cursor", color: "#00D1FF", letter: "Cu", keywords: ["cursor"] },
  { id: "slack", name: "Slack", color: "#E01E5A", letter: "S", keywords: ["slack"] },
];

type Mode = "all" | "selected";

interface ResolvedApp {
  app: KnownApp;
  paths: string[];
}

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [mode, setMode] = useState<Mode>("all");
  const [resolved, setResolved] = useState<ResolvedApp[]>([]);
  const [enabled, setEnabled] = useState<Set<string>>(new Set(KNOWN_APPS.map((a) => a.id)));

  useEffect(() => {
    if (!visible) return;

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
        // Always show browsers (they control system proxy, not TUN paths)
        if (paths.length > 0 || app.browser) {
          results.push({ app, paths });
        }
      }
      setResolved(results);
    });

    window.tunProxy?.getRules().then((rules) => {
      if (rules.mode === "proxy_all_except") {
        setMode("all");
      } else if (rules.mode === "proxy_only") {
        setMode("selected");
        if (rules.apps?.length > 0) {
          const savedPaths = new Set(rules.apps.map((a) => a.toLowerCase()));
          const enabledIds = new Set<string>();
          for (const app of KNOWN_APPS) {
            if (app.browser) continue; // browsers are restored from localStorage
            for (const sp of savedPaths) {
              if (app.keywords.some((kw) => sp.includes(kw))) {
                enabledIds.add(app.id);
                break;
              }
            }
          }
          // Restore browser state from localStorage
          const savedBrowsers = localStorage.getItem("smurov-proxy-browsers");
          if (savedBrowsers) {
            for (const id of JSON.parse(savedBrowsers)) {
              enabledIds.add(id);
            }
          }
          setEnabled(enabledIds);
        }
      }
    });
  }, [visible]);

  const applyRules = useCallback((m: Mode, enabledIds: Set<string>, resolvedApps: ResolvedApp[]) => {
    if (m === "all") {
      window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
      window.sysproxy?.enable();
    } else {
      // TUN rules: only non-browser apps
      const paths: string[] = [];
      for (const r of resolvedApps) {
        if (!r.app.browser && enabledIds.has(r.app.id)) {
          paths.push(...r.paths);
        }
      }
      window.tunProxy?.setRules({ mode: "proxy_only", apps: paths });

      // SOCKS5: enable if any browser is selected
      const anyBrowser = KNOWN_APPS.some((a) => a.browser && enabledIds.has(a.id));
      if (anyBrowser) {
        window.sysproxy?.enable();
      } else {
        window.sysproxy?.disable();
      }

      // Save browser state
      const browserIds = KNOWN_APPS.filter((a) => a.browser && enabledIds.has(a.id)).map((a) => a.id);
      localStorage.setItem("smurov-proxy-browsers", JSON.stringify(browserIds));
    }
  }, []);

  const handleModeChange = (m: Mode) => {
    setMode(m);
    applyRules(m, enabled, resolved);
  };

  const toggle = (appId: string) => {
    setEnabled((prev) => {
      const next = new Set(prev);
      if (next.has(appId)) next.delete(appId);
      else next.add(appId);
      applyRules(mode, next, resolved);
      return next;
    });
  };

  if (!visible) return null;

  const browsers = resolved.filter((r) => r.app.browser);
  const apps = resolved.filter((r) => !r.app.browser);

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>Traffic</div>

      <div style={{ display: "flex", gap: 6, marginBottom: 12 }}>
        {([["all", "All traffic"], ["selected", "Selected apps"]] as const).map(([key, label]) => (
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
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {browsers.length > 0 && (
            <>
              <div style={{ fontSize: 11, color: "#555", fontWeight: 600, padding: "4px 8px 0" }}>Browsers</div>
              {browsers.map(({ app }) => (
                <AppToggle key={app.id} app={app} isOn={enabled.has(app.id)} onToggle={toggle} />
              ))}
            </>
          )}
          {apps.length > 0 && (
            <>
              <div style={{ fontSize: 11, color: "#555", fontWeight: 600, padding: "8px 8px 0" }}>Apps</div>
              {apps.map(({ app }) => (
                <AppToggle key={app.id} app={app} isOn={enabled.has(app.id)} onToggle={toggle} />
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function AppToggle({ app, isOn, onToggle }: { app: KnownApp; isOn: boolean; onToggle: (id: string) => void }) {
  return (
    <div
      onClick={() => onToggle(app.id)}
      style={{
        display: "flex", alignItems: "center", gap: 10,
        padding: "6px 8px", borderRadius: 6, cursor: "pointer",
        background: isOn ? "rgba(59,130,246,0.08)" : "transparent",
      }}
    >
      <div style={{
        width: 28, height: 28, borderRadius: 6,
        background: isOn ? app.color : "#333",
        display: "flex", alignItems: "center", justifyContent: "center",
        fontSize: 12, fontWeight: 700, color: isOn ? "#fff" : "#666",
        flexShrink: 0,
      }}>
        {app.letter}
      </div>
      <div style={{ flex: 1, fontSize: 13, color: isOn ? "#eee" : "#666" }}>
        {app.name}
      </div>
      <div style={{
        width: 36, height: 20, borderRadius: 10,
        background: isOn ? "#3b82f6" : "#333",
        position: "relative", transition: "background 0.2s",
      }}>
        <div style={{
          width: 16, height: 16, borderRadius: 8, background: "#fff",
          position: "absolute", top: 2, left: isOn ? 18 : 2,
          transition: "left 0.2s",
        }} />
      </div>
    </div>
  );
}
