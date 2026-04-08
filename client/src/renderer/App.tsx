import { useState, useEffect, useRef, useCallback, ClipboardEvent } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { useStats } from "./hooks/useStats";
import { StatusBar } from "./components/StatusBar";
import { ModeSelector, ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";
import { SpeedGraph } from "./components/SpeedGraph";
import { BrowserExtension } from "./components/BrowserExtension";

const SERVER = "95.181.162.242:443";
const STORAGE_KEY = "smurov-proxy-key";

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [showSetup, setShowSetup] = useState(!key);
  const [version, setVersion] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const [activeTab, setActiveTab] = useState<"main" | "extension">("main");
  const settingsRef = useRef<HTMLDivElement>(null);
  const { status: socksStatus, error: socksError, loading: socksLoading, connect, disconnect } = useDaemon();
  const [proxyMode, setProxyMode] = useState<ProxyMode>(
    () => (localStorage.getItem("smurov-proxy-mode") as ProxyMode) || "tun"
  );

  // Transport state
  const [transportMode, setTransportMode] = useState<string>("auto");
  const [activeTransport, setActiveTransport] = useState<string>("");

  // TUN state
  const [tunStatus, setTunStatus] = useState<"inactive" | "active" | "reconnecting">("inactive");
  const [tunUptime, setTunUptime] = useState(0);
  const [tunLoading, setTunLoading] = useState(false);
  const [tunError, setTunError] = useState<string | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wasConnected = useRef(false);

  useEffect(() => {
    (window as any).appInfo?.getVersion().then((v: string) => setVersion(v));
  }, []);

  // Close settings dropdown on outside click
  useEffect(() => {
    if (!showSettings) return;
    const handler = (e: MouseEvent) => {
      if (settingsRef.current && !settingsRef.current.contains(e.target as Node)) {
        setShowSettings(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [showSettings]);


  const maxRetries = 5;
  const retryDelay = 5000;

  const startReconnect = useCallback(() => {
    if (reconnecting || !key) return;
    setReconnecting(true);
    setTunError(null);
    let attempt = 0;

    const tryReconnect = async () => {
      attempt++;
      console.log(`[reconnect] attempt ${attempt}/${maxRetries}`);
      try {
        if (proxyMode === "tun") {
          // connect() returns false on non-ok (e.g. 409 from lockDevice race
          // against a stale server-side lock on cold start). Treat that as
          // a retryable failure instead of silently leaving SOCKS5 down.
          const ok = await connect(SERVER, key);
          if (!ok) throw new Error("socks5 connect failed");
          const result = await (window as any).tunProxy?.start(SERVER, key);
          if (result && !result.ok) throw new Error(result.error);
          setTunStatus("active");
          wasConnected.current = true;
        } else {
          const ok = await connect(SERVER, key);
          if (!ok) throw new Error("connect failed");
        }
        setReconnecting(false);
        setTunError(null);
      } catch {
        if (attempt >= maxRetries) {
          setReconnecting(false);
          setTunError("Server unavailable");
        } else {
          reconnectRef.current = setTimeout(tryReconnect, retryDelay);
        }
      }
    };

    tryReconnect();
  }, [reconnecting, key, proxyMode, connect]);

  // Cleanup reconnect timer on unmount
  useEffect(() => {
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
    };
  }, []);

  // Auto-connect on mount when a previously-paired key is present.
  //
  // Intentionally reuses startReconnect() rather than tunConnect(): its
  // retry-with-backoff loop (5 attempts, 5s apart) covers the transient
  // failure modes on cold start — lockDevice races against a freshly-killed
  // previous session, network stack not fully up yet, daemon's UDP transport
  // mid-handshake. A single-shot tunConnect would just give up and leave the
  // user with "Server unavailable" on a first boot blip.
  //
  // Fires exactly once per React mount via autoConnectFired ref. The !key /
  // showSetup guards skip the new-user flow (no key → setup screen handles
  // the first connect via connectWithKey) and the stale-key-cleared flow
  // (machineID rejection clears the key and flips showSetup back on).
  const autoConnectFired = useRef(false);
  useEffect(() => {
    if (autoConnectFired.current) return;
    if (!key || showSetup) return;
    autoConnectFired.current = true;
    startReconnect();
  }, [key, showSetup, startReconnect]);

  // Poll TUN status when in TUN mode
  useEffect(() => {
    if (proxyMode !== "tun") return;
    const poll = async () => {
      try {
        const s = await (window as any).tunProxy?.getStatus();
        if (s) {
          // Map daemon's tun status enum into our local one (which now
          // includes the third "reconnecting" value).
          let next: "inactive" | "active" | "reconnecting" = "inactive";
          if (s.status === "active") next = "active";
          else if (s.status === "reconnecting") next = "reconnecting";
          setTunStatus(next);
          setTunUptime(s.uptime || 0);
          const active = next === "active";
          if (s.error) setTunError(s.error);
          // Only fire client-side startReconnect on a HARD disconnect, not
          // while the daemon is still trying to reconnect on its own.
          if (wasConnected.current && !active && next !== "reconnecting" && s.error) {
            if (s.error.includes("bound to a different machine")) {
              localStorage.removeItem(STORAGE_KEY);
              setKey("");
              setShowSetup(true);
            } else {
              startReconnect();
            }
          }
          wasConnected.current = active;
        }
        const tr = await (window as any).transport?.getMode();
        if (tr) {
          setTransportMode(tr.mode || "auto");
          setActiveTransport(tr.active || "");
        }
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => clearInterval(interval);
  }, [proxyMode, startReconnect]);

  // Detect SOCKS5 server loss and auto-reconnect
  useEffect(() => {
    if (proxyMode !== "socks5") return;
    if (socksError && !reconnecting && key) {
      if (socksError.includes("bound to a different machine")) {
        localStorage.removeItem(STORAGE_KEY);
        setKey("");
        setShowSetup(true);
      } else {
        startReconnect();
      }
    }
  }, [socksError, proxyMode, reconnecting, key, startReconnect]);

  // Daemon-reported reconnecting state — distinct from the client-side
  // `reconnecting` flag (which drives startReconnect()'s loop). Both
  // mean the user should see "Reconnecting…" in the UI; OR them.
  const daemonReconnecting =
    socksStatus.status === "reconnecting" || tunStatus === "reconnecting";

  // Effective state based on mode
  const isConnected = proxyMode === "tun"
    ? tunStatus === "active"
    : socksStatus.status === "connected";
  const isLoading = reconnecting || (proxyMode === "tun" ? tunLoading : socksLoading);
  const currentError = reconnecting ? null : (proxyMode === "tun" ? tunError : socksError);
  const uptime = proxyMode === "tun" ? tunUptime : socksStatus.uptime;
  const stats = useStats(isConnected);

  const handleModeChange = (m: ProxyMode) => {
    setProxyMode(m);
    localStorage.setItem("smurov-proxy-mode", m);
  };

  const tunConnect = useCallback(async (server: string, k: string) => {
    setTunLoading(true);
    setTunError(null);
    try {
      // Start SOCKS5 tunnel + enable system proxy with PAC. This has to
      // run BEFORE the TUN engine: browsers rely on PAC/SOCKS5 for the
      // per-site proxy rules, and if this step fails silently the whole
      // "proxy one site in Chrome" flow is dead while the UI still shows
      // green because TUN (per-app routing) reports healthy.
      //
      // One retry with 800ms backoff covers transient lockDevice /
      // handshake races — e.g. the previous daemon session hasn't fully
      // released the device lock on the server yet when /connect reaches
      // it a beat too soon.
      let socksOk = await connect(server, k);
      if (!socksOk) {
        console.warn("[tunConnect] first /connect failed, retrying in 800ms");
        await new Promise((r) => setTimeout(r, 800));
        socksOk = await connect(server, k);
      }
      if (socksOk) {
        (window as any).sysproxy?.setPacSites({ proxy_all: true });
        (window as any).sysproxy?.enable();
      } else {
        // Don't bail — TUN still works for per-app proxying (Telegram,
        // Cursor, etc.). Just surface the failure so the user knows
        // browsers won't get per-site proxying until they reconnect.
        console.warn("[tunConnect] SOCKS5 tunnel failed twice, continuing with TUN only");
        setTunError("Browser proxy is off (SOCKS5 failed to start). Apps still go through TUN. Reconnect to retry.");
      }

      // Start TUN for apps
      const result = await (window as any).tunProxy?.start(server, k);
      if (result && !result.ok) {
        setTunError(result.error || "Failed to connect");
      } else {
        setTunStatus("active");
      }
    } catch {
      setTunError("Failed to connect");
    } finally {
      setTunLoading(false);
    }
  }, [connect]);

  const stopReconnect = useCallback(() => {
    if (reconnectRef.current) {
      clearTimeout(reconnectRef.current);
      reconnectRef.current = null;
    }
    setReconnecting(false);
    wasConnected.current = false;
  }, []);

  const tunDisconnect = useCallback(async () => {
    stopReconnect();
    setTunLoading(true);
    try {
      await (window as any).tunProxy?.stop();
      (window as any).sysproxy?.disable();
      disconnect();
      setTunStatus("inactive");
      setTunError(null);
    } catch {} finally {
      setTunLoading(false);
    }
  }, [disconnect, stopReconnect]);

  // Handle transport mode change from the StatusBar badge dropdown.
  // Persist the mode on the daemon, then force a reconnect so the running
  // transport is replaced by one of the new kind.
  const handleTransportChange = useCallback(
    async (mode: string) => {
      try {
        await (window as any).transport?.setMode(mode);
        setTransportMode(mode);
      } catch {
        return;
      }
      if (!key) return;
      if (proxyMode === "tun") {
        await tunDisconnect();
        await new Promise((r) => setTimeout(r, 300));
        await tunConnect(SERVER, key);
      } else {
        await disconnect();
        await new Promise((r) => setTimeout(r, 300));
        await connect(SERVER, key);
      }
    },
    [key, proxyMode, connect, disconnect, tunConnect, tunDisconnect],
  );

  // Update tray icon based on connection status
  useEffect(() => {
    (window as any).appInfo?.setTrayStatus(isConnected);
  }, [isConnected]);

  // On system wake, the daemon's UDP session is silently dead (server forgot
  // us during sleep). Tear down the old state and reconnect instead of waiting
  // for the keepalive timeout. Uses a ref for the "was connected" check so the
  // resume handler always sees the latest value without re-subscribing on
  // every status change.
  const wasConnectedOnWakeRef = useRef(false);
  useEffect(() => {
    wasConnectedOnWakeRef.current = isConnected;
  }, [isConnected]);
  useEffect(() => {
    const app = (window as any).appInfo;
    if (!app?.onSystemResumed) return;
    const cleanup = app.onSystemResumed(async () => {
      if (!wasConnectedOnWakeRef.current || !key) return;
      console.log("[wake] system resumed, forcing reconnect");
      if (proxyMode === "tun") {
        await tunDisconnect();
      } else {
        await disconnect();
      }
      // Let the network stack settle before trying to reach the server.
      await new Promise((r) => setTimeout(r, 500));
      startReconnect();
    });
    return cleanup;
  }, [key, proxyMode, tunDisconnect, disconnect, startReconnect]);

  // Handle tray connect/disconnect clicks
  useEffect(() => {
    const app = (window as any).appInfo;
    if (!app) return;
    app.onTrayConnect(() => {
      if (!isConnected && key) {
        if (proxyMode === "tun") {
          tunConnect(SERVER, key);
        } else {
          connect(SERVER, key);
        }
      }
    });
    app.onTrayDisconnect(() => {
      if (isConnected) {
        if (proxyMode === "tun") {
          tunDisconnect();
        } else {
          disconnect();
        }
      }
    });
  }, [key, isConnected, proxyMode, connect, disconnect, tunConnect, tunDisconnect]);


  const connectWithKey = (k: string) => {
    const trimmed = k.trim();
    if (!trimmed) return;
    localStorage.setItem(STORAGE_KEY, trimmed);
    setKey(trimmed);
    setShowSetup(false);
    if (proxyMode === "tun") {
      tunConnect(SERVER, trimmed);
    } else {
      connect(SERVER, trimmed);
    }
  };

  const handlePaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const pasted = e.clipboardData.getData("text");
    if (pasted.trim()) {
      connectWithKey(pasted);
    }
  };

  const handleReset = () => {
    if (proxyMode === "tun") {
      tunDisconnect();
    } else {
      disconnect();
    }
    localStorage.removeItem(STORAGE_KEY);
    setKey("");
    setShowSetup(true);
  };

  return (
    <div style={{ maxWidth: 760, margin: "0 auto", paddingTop: 36 }}>
      {/* Custom title bar */}
      <div
        style={{
          position: "fixed", top: 0, left: 0, right: 0, height: 36,
          display: "flex", alignItems: "center", justifyContent: "space-between",
          padding: "0 12px", // @ts-ignore electron drag region
          WebkitAppRegion: "drag", zIndex: 100,
          background: "#0b0f1a",
        }}
      >
        <div style={{ fontSize: 12, color: "#555", fontWeight: 600 }}>
          SmurovProxy {version && <span style={{ fontWeight: 400 }}>v{version}</span>}
        </div>
        {/* @ts-ignore */}
        <div style={{ display: "flex", gap: 4, WebkitAppRegion: "no-drag" }}>
          <div ref={settingsRef} style={{ position: "relative" }}>
            <button
              onClick={() => setShowSettings(!showSettings)}
              style={{
                width: 28, height: 28, borderRadius: 6,
                background: showSettings ? "#1e2a3a" : "transparent", border: "none",
                color: showSettings ? "#fff" : "#666", fontSize: 15, cursor: "pointer",
                display: "flex", alignItems: "center", justifyContent: "center",
              }}
              onMouseEnter={(e) => { e.currentTarget.style.background = "#1e2a3a"; e.currentTarget.style.color = "#fff"; }}
              onMouseLeave={(e) => { if (!showSettings) { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "#666"; } }}
            >
              ⚙
            </button>
            {showSettings && (
              <div style={{
                position: "absolute", top: 32, right: 0, minWidth: 160,
                background: "#1a1f2e", border: "1px solid #333", borderRadius: 8,
                padding: 4, zIndex: 200, boxShadow: "0 4px 12px rgba(0,0,0,0.4)",
              }}>
                {[
                  { label: "Check for Updates", onClick: () => {
                    setShowSettings(false);
                    (window as any).appInfo?.openUpdate();
                  }},
                  ...(!showSetup ? [{ label: "Change Key", onClick: () => { setShowSettings(false); handleReset(); } }] : []),
                  { label: "Logs", onClick: () => { setShowSettings(false); (window as any).appInfo?.openLogs(); } },
                ].map((item) => (
                  <button
                    key={item.label}
                    onClick={item.onClick}
                    style={{
                      display: "block", width: "100%", padding: "6px 10px",
                      background: "transparent", border: "none", borderRadius: 4,
                      color: "#ccc", fontSize: 13, cursor: "pointer", textAlign: "left",
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.background = "#2a3040"; }}
                    onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; }}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            )}
          </div>
          <button
            onClick={() => (window as any).appInfo?.closeWindow()}
            style={{
              width: 28, height: 28, borderRadius: 6,
              background: "transparent", border: "none",
              color: "#666", fontSize: 16, cursor: "pointer",
              display: "flex", alignItems: "center", justifyContent: "center",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.background = "#1e2a3a"; e.currentTarget.style.color = "#fff"; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "#666"; }}
          >
            ✕
          </button>
        </div>
      </div>

      <div style={{ marginBottom: 20 }}>
        <h1 style={{ fontSize: 20, fontWeight: 700 }}>SmurovProxy</h1>
      </div>
      {!showSetup && (
        <StatusBar
          connected={isConnected}
          loading={isLoading}
          reconnecting={reconnecting || daemonReconnecting}
          server={SERVER.replace(":443", "")}
          uptime={uptime}
          transport={activeTransport}
          transportMode={transportMode}
          error={currentError}
          onConnect={() => {
            if (proxyMode === "tun") {
              tunConnect(SERVER, key);
            } else {
              connect(SERVER, key);
            }
          }}
          onDisconnect={() => {
            if (proxyMode === "tun") {
              tunDisconnect();
            } else {
              disconnect();
            }
          }}
          onTransportChange={handleTransportChange}
        />
      )}
      {isConnected && (
        <SpeedGraph
          download={stats.download}
          upload={stats.upload}
          history={stats.history}
        />
      )}

      {showSetup ? (
        <div>
          <label style={{ display: "block", marginBottom: 16 }}>
            <span style={{ fontSize: 13, color: "#aaa" }}>Access Key</span>
            <input
              style={{
                width: "100%",
                padding: "10px 12px",
                background: "#16213e",
                border: "1px solid #333",
                borderRadius: 6,
                color: "#eee",
                fontSize: 14,
                marginTop: 4,
              }}
              type="password"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="Paste your access key"
              onPaste={handlePaste}
              onKeyDown={(e) => e.key === "Enter" && connectWithKey(key)}
            />
          </label>
          {isLoading && (
            <div style={{ color: "#aaa", fontSize: 13, marginTop: 8 }}>
              Connecting...
            </div>
          )}
        </div>
      ) : (
        <>
          <div style={{ display: "flex", gap: 4, marginBottom: 12, borderBottom: "1px solid #1e2533" }}>
            {(["main", "extension"] as const).map((tab) => (
              <button
                key={tab}
                onClick={() => setActiveTab(tab)}
                style={{
                  padding: "6px 14px",
                  background: "transparent",
                  border: "none",
                  borderBottom: activeTab === tab ? "2px solid #3b82f6" : "2px solid transparent",
                  color: activeTab === tab ? "#fff" : "#666",
                  fontSize: 13,
                  cursor: "pointer",
                  fontWeight: activeTab === tab ? 600 : 400,
                  marginBottom: -1,
                }}
              >
                {tab === "main" ? "Main" : "Extension"}
              </button>
            ))}
          </div>
          {activeTab === "main" && (
            <>
              <ModeSelector mode={proxyMode} onChange={handleModeChange} disabled={isConnected} />
              <AppRules visible={proxyMode === "tun"} />
            </>
          )}
          {activeTab === "extension" && <BrowserExtension />}
        </>
      )}

    </div>
  );
}
