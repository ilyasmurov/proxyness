import { useState, useEffect, useRef, useCallback, ClipboardEvent } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { useStats } from "./hooks/useStats";
import { StatusBar } from "./components/StatusBar";
import { ConnectionButton } from "./components/ConnectionButton";
import { ModeSelector, ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";
import { SpeedGraph } from "./components/SpeedGraph";

const SERVER = "82.97.246.65:443";
const STORAGE_KEY = "smurov-proxy-key";

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [showSetup, setShowSetup] = useState(!key);
  const [version, setVersion] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const settingsRef = useRef<HTMLDivElement>(null);
  const { status: socksStatus, error: socksError, loading: socksLoading, connect, disconnect } = useDaemon();
  const [proxyMode, setProxyMode] = useState<ProxyMode>(
    () => (localStorage.getItem("smurov-proxy-mode") as ProxyMode) || "tun"
  );

  // TUN state
  const [tunStatus, setTunStatus] = useState<"inactive" | "active">("inactive");
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
          await connect(SERVER, key);
          const result = await (window as any).tunProxy?.start(SERVER, key);
          if (result && !result.ok) throw new Error(result.error);
          setTunStatus("active");
          wasConnected.current = true;
        } else {
          await connect(SERVER, key);
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

  // Poll TUN status when in TUN mode
  useEffect(() => {
    if (proxyMode !== "tun") return;
    const poll = async () => {
      try {
        const s = await (window as any).tunProxy?.getStatus();
        if (s) {
          const active = s.status === "active";
          setTunStatus(active ? "active" : "inactive");
          setTunUptime(s.uptime || 0);
          if (s.error) setTunError(s.error);
          if (wasConnected.current && !active && s.error) {
            startReconnect();
          }
          wasConnected.current = active;
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
      startReconnect();
    }
  }, [socksError, proxyMode, reconnecting, key, startReconnect]);

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
      // Start SOCKS5 tunnel + enable system proxy with PAC
      await connect(server, k);
      (window as any).sysproxy?.setPacSites({ proxy_all: true, sites: [] });
      (window as any).sysproxy?.enable();

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
  }, [disconnect]);

  // Update tray icon based on connection status
  useEffect(() => {
    (window as any).appInfo?.setTrayStatus(isConnected);
  }, [isConnected]);

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
    <div style={{ maxWidth: 380, margin: "0 auto", paddingTop: 36 }}>
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

      <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 20 }}>
        <h1 style={{ fontSize: 20, fontWeight: 700 }}>SmurovProxy</h1>
      </div>
      <StatusBar status={isConnected ? "connected" : "disconnected"} uptime={uptime} error={currentError} />
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
          <ModeSelector mode={proxyMode} onChange={handleModeChange} disabled={isConnected} />
          <ConnectionButton
            connected={isConnected}
            loading={isLoading}
            reconnecting={reconnecting}
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
          />
          <AppRules visible={proxyMode === "tun"} />
        </>
      )}

    </div>
  );
}
