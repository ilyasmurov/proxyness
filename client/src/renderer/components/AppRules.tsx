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
}

const KNOWN_APPS: KnownApp[] = [
  { id: "telegram", name: "Telegram", color: "#27A7E7", letter: "T", keywords: ["telegram"] },
  { id: "discord", name: "Discord", color: "#5865F2", letter: "D", keywords: ["discord"] },
  { id: "claude", name: "Claude Code", color: "#D97757", letter: "C", keywords: ["claude"] },
  { id: "cursor", name: "Cursor", color: "#00D1FF", letter: "Cu", keywords: ["cursor"] },
  { id: "slack", name: "Slack", color: "#E01E5A", letter: "S", keywords: ["slack"] },
];

interface BrowserSite {
  domain: string;
  label: string;
  builtin?: boolean;
}

const DEFAULT_SITES: BrowserSite[] = [
  { domain: "*", label: "All sites", builtin: true },
  { domain: "youtube.com", label: "YouTube", builtin: true },
  { domain: "instagram.com", label: "Instagram", builtin: true },
  { domain: "twitter.com", label: "Twitter / X", builtin: true },
];

const STORAGE_KEY_SITES = "smurov-proxy-sites";
const STORAGE_KEY_ENABLED_SITES = "smurov-proxy-enabled-sites";

function loadSites(): BrowserSite[] {
  const custom = localStorage.getItem(STORAGE_KEY_SITES);
  if (custom) {
    try {
      return [...DEFAULT_SITES, ...JSON.parse(custom)];
    } catch {}
  }
  return [...DEFAULT_SITES];
}

function saveCustomSites(sites: BrowserSite[]) {
  const custom = sites.filter((s) => !s.builtin);
  localStorage.setItem(STORAGE_KEY_SITES, JSON.stringify(custom));
}

function loadEnabledSites(): Set<string> {
  const saved = localStorage.getItem(STORAGE_KEY_ENABLED_SITES);
  if (saved) {
    try { return new Set(JSON.parse(saved)); } catch {}
  }
  return new Set(["*"]); // default: all sites
}

function saveEnabledSites(enabled: Set<string>) {
  localStorage.setItem(STORAGE_KEY_ENABLED_SITES, JSON.stringify([...enabled]));
}

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

  // Browser sites
  const [sites, setSites] = useState<BrowserSite[]>(loadSites);
  const [enabledSites, setEnabledSites] = useState<Set<string>>(loadEnabledSites);
  const [browsersOn, setBrowsersOn] = useState(() => localStorage.getItem("smurov-proxy-browsers-on") !== "false");
  const [showSites, setShowSites] = useState(false);
  const [newSite, setNewSite] = useState("");

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
        if (paths.length > 0) {
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
            for (const sp of savedPaths) {
              if (app.keywords.some((kw) => sp.includes(kw))) {
                enabledIds.add(app.id);
                break;
              }
            }
          }
          setEnabled(enabledIds);
        }
      }
    });
  }, [visible]);

  const applyPac = useCallback((on: boolean, eSites: Set<string>) => {
    if (!on) {
      window.sysproxy?.disable();
      return;
    }
    const proxyAll = eSites.has("*");
    const siteDomains = proxyAll ? [] : [...eSites];
    window.sysproxy?.setPacSites({ proxy_all: proxyAll, sites: siteDomains });
    window.sysproxy?.enable();
  }, []);

  const applyRules = useCallback((m: Mode, enabledIds: Set<string>, resolvedApps: ResolvedApp[], bOn: boolean, eSites: Set<string>) => {
    if (m === "all") {
      window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
      window.sysproxy?.setPacSites({ proxy_all: true, sites: [] });
      window.sysproxy?.enable();
    } else {
      const paths: string[] = [];
      for (const r of resolvedApps) {
        if (enabledIds.has(r.app.id)) {
          paths.push(...r.paths);
        }
      }
      window.tunProxy?.setRules({ mode: "proxy_only", apps: paths });
      applyPac(bOn, eSites);
    }
  }, [applyPac]);

  const handleModeChange = (m: Mode) => {
    setMode(m);
    applyRules(m, enabled, resolved, browsersOn, enabledSites);
  };

  const toggleApp = (appId: string) => {
    setEnabled((prev) => {
      const next = new Set(prev);
      if (next.has(appId)) next.delete(appId);
      else next.add(appId);
      applyRules(mode, next, resolved, browsersOn, enabledSites);
      return next;
    });
  };

  const toggleBrowsers = () => {
    const next = !browsersOn;
    setBrowsersOn(next);
    localStorage.setItem("smurov-proxy-browsers-on", next ? "true" : "false");
    applyPac(next, enabledSites);
  };

  const toggleSite = (domain: string) => {
    setEnabledSites((prev) => {
      const next = new Set(prev);
      if (domain === "*") {
        // "All sites" toggle: if turning on, enable only "*"; if turning off, clear
        if (next.has("*")) {
          next.delete("*");
        } else {
          next.clear();
          next.add("*");
        }
      } else {
        // Specific site: disable "all sites" if it was on
        next.delete("*");
        if (next.has(domain)) next.delete(domain);
        else next.add(domain);
      }
      saveEnabledSites(next);
      applyPac(browsersOn, next);
      return next;
    });
  };

  const addSite = () => {
    let domain = newSite.trim().toLowerCase();
    if (!domain) return;
    // Strip protocol and path
    domain = domain.replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/^www\./, "");
    if (!domain || sites.some((s) => s.domain === domain)) {
      setNewSite("");
      return;
    }
    const site: BrowserSite = { domain, label: domain };
    const next = [...sites, site];
    setSites(next);
    saveCustomSites(next);
    setEnabledSites((prev) => {
      const ns = new Set(prev);
      ns.delete("*");
      ns.add(domain);
      saveEnabledSites(ns);
      applyPac(browsersOn, ns);
      return ns;
    });
    setNewSite("");
  };

  const removeSite = (domain: string) => {
    const next = sites.filter((s) => s.domain !== domain);
    setSites(next);
    saveCustomSites(next);
    setEnabledSites((prev) => {
      const ns = new Set(prev);
      ns.delete(domain);
      saveEnabledSites(ns);
      applyPac(browsersOn, ns);
      return ns;
    });
  };

  if (!visible) return null;

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
          {/* Browsers toggle with expandable sites */}
          <div>
            <div
              style={{
                display: "flex", alignItems: "center", gap: 10,
                padding: "6px 8px", borderRadius: 6, cursor: "pointer",
                background: browsersOn ? "rgba(59,130,246,0.08)" : "transparent",
              }}
            >
              <div
                onClick={() => setShowSites(!showSites)}
                style={{
                  width: 28, height: 28, borderRadius: 6,
                  background: browsersOn ? "#4285F4" : "#333",
                  display: "flex", alignItems: "center", justifyContent: "center",
                  fontSize: 12, fontWeight: 700, color: browsersOn ? "#fff" : "#666",
                  flexShrink: 0,
                }}
              >
                B
              </div>
              <div onClick={() => setShowSites(!showSites)} style={{ flex: 1, fontSize: 13, color: browsersOn ? "#eee" : "#666", cursor: "pointer" }}>
                Browsers
                <span style={{ fontSize: 10, color: "#555", marginLeft: 6 }}>
                  {enabledSites.has("*") ? "all sites" : `${enabledSites.size} site${enabledSites.size !== 1 ? "s" : ""}`}
                </span>
              </div>
              <span
                onClick={() => setShowSites(!showSites)}
                style={{
                  fontSize: 10, color: "#555", cursor: "pointer",
                  transition: "transform 0.2s",
                  transform: showSites ? "rotate(90deg)" : "rotate(0deg)",
                  display: "inline-block", userSelect: "none",
                }}
              >
                ▶
              </span>
              <div
                onClick={toggleBrowsers}
                style={{
                  width: 36, height: 20, borderRadius: 10,
                  background: browsersOn ? "#3b82f6" : "#333",
                  position: "relative", transition: "background 0.2s", cursor: "pointer",
                }}
              >
                <div style={{
                  width: 16, height: 16, borderRadius: 8, background: "#fff",
                  position: "absolute", top: 2, left: browsersOn ? 18 : 2,
                  transition: "left 0.2s",
                }} />
              </div>
            </div>

            {showSites && (
              <div style={{ marginLeft: 38, marginTop: 4, display: "flex", flexDirection: "column", gap: 2 }}>
                {sites.map((site) => {
                  const isOn = enabledSites.has(site.domain);
                  return (
                    <div key={site.domain} style={{ display: "flex", alignItems: "center", gap: 8, padding: "3px 0" }}>
                      <div
                        onClick={() => toggleSite(site.domain)}
                        style={{
                          width: 16, height: 16, borderRadius: 4, cursor: "pointer",
                          background: isOn ? "#3b82f6" : "transparent",
                          border: `1.5px solid ${isOn ? "#3b82f6" : "#555"}`,
                          display: "flex", alignItems: "center", justifyContent: "center",
                          fontSize: 10, color: "#fff", flexShrink: 0,
                        }}
                      >
                        {isOn && "✓"}
                      </div>
                      <span style={{ flex: 1, fontSize: 12, color: isOn ? "#ccc" : "#666" }}>{site.label}</span>
                      {!site.builtin && (
                        <button
                          onClick={() => removeSite(site.domain)}
                          style={{
                            background: "transparent", border: "none", color: "#555",
                            fontSize: 14, cursor: "pointer", padding: "0 4px", lineHeight: 1,
                          }}
                        >
                          ×
                        </button>
                      )}
                    </div>
                  );
                })}
                <div style={{ display: "flex", gap: 4, marginTop: 4 }}>
                  <input
                    value={newSite}
                    onChange={(e) => setNewSite(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && addSite()}
                    placeholder="example.com"
                    style={{
                      flex: 1, padding: "4px 8px", fontSize: 12,
                      background: "#0d1117", border: "1px solid #333", borderRadius: 4,
                      color: "#ccc", outline: "none",
                    }}
                  />
                  <button
                    onClick={addSite}
                    style={{
                      padding: "4px 10px", fontSize: 11,
                      background: "#1a3a5c", border: "1px solid #3b82f6", borderRadius: 4,
                      color: "#fff", cursor: "pointer",
                    }}
                  >
                    Add
                  </button>
                </div>
              </div>
            )}
          </div>

          {/* App toggles */}
          {resolved.map(({ app }) => (
            <AppToggle key={app.id} app={app} isOn={enabled.has(app.id)} onToggle={toggleApp} />
          ))}
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
