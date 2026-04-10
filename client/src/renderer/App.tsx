import { useState, useEffect, useRef, useCallback, ClipboardEvent } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { useStats } from "./hooks/useStats";
import { StatusBar } from "./components/StatusBar";
import { ModeSelector, ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";
import { SpeedGraph } from "./components/SpeedGraph";
import { BrowserExtension } from "./components/BrowserExtension";
import { NotificationBanner } from "./components/NotificationBanner";

const SERVER = "95.181.162.242:443";
const STORAGE_KEY = "smurov-proxy-key";

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [showSetup, setShowSetup] = useState(!key);
  const [version, setVersion] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const [activeTab, setActiveTab] = useState<"main" | "extension">("main");
  const [trafficMode, setTrafficMode] = useState<"all" | "selected">("all");
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
  // reconnectingRef mirrors `reconnecting` state but is updated synchronously.
  // The state-only guard in startReconnect was racy: when the polling effect
  // and a transport-error effect both fired in the same React tick, both saw
  // reconnecting=false (state hadn't flushed yet), both passed the guard, and
  // both spawned independent retry loops — each calling /tun/start, each
  // triggering "engine already active, restarting" on the daemon side.
  // Storming the engine restart path corrupts NAT state and burns CPU.
  const reconnectingRef = useRef(false);
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wasConnected = useRef(false);

  useEffect(() => {
    (window as any).appInfo?.getVersion().then((v: string) => setVersion(v));
    // Send stored key to main process so config poller can start
    const storedKey = localStorage.getItem(STORAGE_KEY);
    if (storedKey) (window as any).updater?.storeKey(storedKey);
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
    // Synchronous guard via ref (see comment on reconnectingRef above).
    // The previous `if (reconnecting || ...)` check on React state was
    // racy across same-tick callers.
    if (reconnectingRef.current || !key) return;
    reconnectingRef.current = true;
    setReconnecting(true);
    setTunError(null);
    let attempt = 0;

    const finish = (err: string | null) => {
      reconnectingRef.current = false;
      setReconnecting(false);
      if (err !== undefined) setTunError(err);
    };

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
        finish(null);
      } catch {
        if (attempt >= maxRetries) {
          finish("Server unavailable");
        } else {
          reconnectRef.current = setTimeout(tryReconnect, retryDelay);
        }
      }
    };

    tryReconnect();
  }, [key, proxyMode, connect]);

  // Cleanup reconnect timer on unmount
  useEffect(() => {
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
    };
  }, []);

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

  const handleModeChange = async (m: ProxyMode) => {
    if (m === proxyMode) return;
    const wasConnected = isConnected;
    if (wasConnected) {
      if (proxyMode === "tun") await tunDisconnect();
      else await disconnect();
    }
    setProxyMode(m);
    localStorage.setItem("smurov-proxy-mode", m);
    if (wasConnected && key) {
      if (m === "tun") await tunConnect(SERVER, key);
      else await connect(SERVER, key);
    }
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

  // Desktop notifications on connection state transitions.
  // prevNotifState tracks the last state we notified on so we only fire
  // on real edges (connected → disconnected, etc.), not on every render.
  // Skip the very first run so we don't spam a "Disconnected" toast at
  // app launch before auto-connect has had a chance to run.
  const prevNotifState = useRef<"connected" | "disconnected" | "reconnecting" | null>(null);
  useEffect(() => {
    const current: "connected" | "disconnected" | "reconnecting" =
      isConnected ? "connected" : daemonReconnecting || reconnecting ? "reconnecting" : "disconnected";
    const prev = prevNotifState.current;
    prevNotifState.current = current;
    if (prev === null) return;
    if (prev === current) return;
    const notify = (window as any).appInfo?.showNotification;
    if (!notify) return;
    if (current === "connected") notify("SmurovProxy", "Connected");
    else if (current === "reconnecting") notify("SmurovProxy", "Reconnecting...");
    else if (current === "disconnected") notify("SmurovProxy", "Disconnected");
  }, [isConnected, daemonReconnecting, reconnecting]);

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
    // Tell main process the key so it can poll config
    (window as any).updater?.storeKey(trimmed);
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

  const titleBtn = (children: React.ReactNode, onClick: () => void) => (
    <button
      onClick={onClick}
      style={{
        width: 28, height: 28, borderRadius: 6,
        background: "transparent", border: "none",
        color: "#555", fontSize: 15, cursor: "pointer",
        display: "flex", alignItems: "center", justifyContent: "center",
        transition: "background 0.15s, color 0.15s",
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = "#1e2a3a"; e.currentTarget.style.color = "#eee"; }}
      onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "#555"; }}
    >
      {children}
    </button>
  );

  return (
    <div style={{ maxWidth: 760, margin: "0 auto", padding: "36px 16px 16px", height: "100vh", display: "flex", flexDirection: "column" }}>
      {/* Custom title bar */}
      <div
        style={{
          position: "fixed", top: 0, left: 0, right: 0, height: 36,
          display: "flex", alignItems: "center", justifyContent: "space-between",
          padding: "0 12px", // @ts-ignore electron drag region
          WebkitAppRegion: "drag", zIndex: 100,
          background: "transparent",
        }}
      >
        <div style={{ fontSize: 12, color: "#555", fontWeight: 600 }}>
          SmurovProxy {version && <span style={{ fontWeight: 400 }}>v{version}</span>}
          {version && version.includes("beta") && (
            <span style={{
              fontSize: 9, fontWeight: 700, color: "#f59e0b",
              background: "rgba(245,158,11,0.15)", borderRadius: 4,
              padding: "1px 5px", marginLeft: 6, letterSpacing: 0.5,
            }}>BETA</span>
          )}
        </div>
        {/* @ts-ignore */}
        <div style={{ display: "flex", gap: 2, WebkitAppRegion: "no-drag" }}>
          <div ref={settingsRef} style={{ position: "relative" }}>
            {titleBtn(<>&#9881;</>, () => setShowSettings(!showSettings))}
            {showSettings && (
              <div style={{
                position: "absolute", top: 32, right: 0, minWidth: 160,
                background: "#1a1f2e", border: "1px solid #1e2533", borderRadius: 8,
                padding: 4, zIndex: 200, boxShadow: "0 8px 24px rgba(0,0,0,0.5)",
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
                      display: "block", width: "100%", padding: "7px 12px",
                      background: "transparent", border: "none", borderRadius: 6,
                      color: "#bbb", fontSize: 13, cursor: "pointer", textAlign: "left",
                      transition: "background 0.1s, color 0.1s",
                    }}
                    onMouseEnter={(e) => { e.currentTarget.style.background = "#252d3d"; e.currentTarget.style.color = "#eee"; }}
                    onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "#bbb"; }}
                  >
                    {item.label}
                  </button>
                ))}
              </div>
            )}
          </div>
          {titleBtn(<span style={{ fontSize: 18, marginTop: 4 }}>&minus;</span>, () => (window as any).appInfo?.minimizeWindow())}
          {titleBtn(<span style={{ fontSize: 13 }}>&#10005;</span>, () => (window as any).appInfo?.closeWindow())}
        </div>
      </div>

      <div style={{ marginBottom: 20, display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12 }}>
        <h1 style={{ fontSize: 20, fontWeight: 700, margin: 0 }}>SmurovProxy</h1>
        {!showSetup && (
          <ModeSelector mode={proxyMode} onChange={handleModeChange} />
        )}
      </div>
      {!showSetup && <NotificationBanner />}
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
          <div style={{ display: "flex", alignItems: "stretch", gap: 8, marginBottom: 12, borderBottom: "1px solid #1e2533" }}>
            {(["main", "extension"] as const).map((tab) => {
              const isActive = activeTab === tab;
              return (
                <div
                  key={tab}
                  onClick={() => setActiveTab(tab)}
                  style={{
                    display: "flex",
                    alignItems: "center",
                    gap: 40,
                    padding: 16,
                    borderBottom: isActive ? "2px solid #3b82f6" : "2px solid transparent",
                    color: isActive ? "#fff" : "#666",
                    fontSize: 16,
                    cursor: "pointer",
                    fontWeight: isActive ? 600 : 400,
                    marginBottom: -1,
                  }}
                >
                  <span>{tab === "main" ? "Main" : "Extension"}</span>
                  {tab === "main" && proxyMode === "tun" && (
                    <div
                      onClick={(e) => { if (isActive) e.stopPropagation(); }}
                      style={{
                        display: "inline-flex",
                        padding: 2,
                        background: "#0f1420",
                        border: "none",
                        borderRadius: 8,
                        opacity: isActive ? 1 : 0.5,
                        filter: isActive ? "none" : "grayscale(1)",
                        pointerEvents: isActive ? "auto" : "none",
                      }}
                    >
                      {(["all", "selected"] as const).map((k) => {
                        const active = trafficMode === k;
                        return (
                          <button
                            key={k}
                            disabled={!isActive}
                            onClick={() => setTrafficMode(k)}
                            style={{
                              padding: "5px 12px",
                              background: active ? "#1a3a5c" : "transparent",
                              border: `1px solid ${active ? "#3b82f6" : "transparent"}`,
                              borderRadius: 6,
                              color: active ? "#fff" : "#888",
                              fontSize: 12,
                              fontWeight: active ? 600 : 400,
                              cursor: isActive ? "pointer" : "default",
                            }}
                          >
                            {k === "all" ? "All traffic" : "Selected"}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
          <div style={{ flex: 1, overflowY: "auto", minHeight: 0 }}>
            {activeTab === "main" && (
              <div style={{ marginTop: 16 }}>
                <AppRules visible={proxyMode === "tun"} mode={trafficMode} onModeChange={setTrafficMode} hideModeSwitch />
              </div>
            )}
            {activeTab === "extension" && <BrowserExtension />}
          </div>
        </>
      )}

    </div>
  );
}
