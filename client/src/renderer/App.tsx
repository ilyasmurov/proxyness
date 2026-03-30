import { useState, useEffect, useRef, useCallback, ClipboardEvent } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { useStats } from "./hooks/useStats";
import { StatusBar } from "./components/StatusBar";
import { ConnectionButton } from "./components/ConnectionButton";
import { UpdateBanner } from "./components/UpdateBanner";
import { ModeSelector, ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";
import { SpeedGraph } from "./components/SpeedGraph";

const SERVER = "82.97.246.65:443";
const STORAGE_KEY = "smurov-proxy-key";

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [showSetup, setShowSetup] = useState(!key);
  const [version, setVersion] = useState("");
  const [showLogs, setShowLogs] = useState(false);
  const [logs, setLogs] = useState<string[]>([]);
  const logsEndRef = useRef<HTMLDivElement>(null);
  const { status: socksStatus, error: socksError, loading: socksLoading, connect, disconnect } = useDaemon();
  const autoConnected = useRef(false);
  const [proxyMode, setProxyMode] = useState<ProxyMode>(
    () => (localStorage.getItem("smurov-proxy-mode") as ProxyMode) || "tun"
  );

  // TUN state
  const [tunStatus, setTunStatus] = useState<"inactive" | "active">("inactive");
  const [tunLoading, setTunLoading] = useState(false);
  const [tunError, setTunError] = useState<string | null>(null);

  useEffect(() => {
    (window as any).appInfo?.getVersion().then((v: string) => setVersion(v));
  }, []);

  // Poll logs when panel is open
  useEffect(() => {
    if (!showLogs) return;
    const poll = async () => {
      const l = await (window as any).appInfo?.getLogs();
      if (l) setLogs(l);
    };
    poll();
    const interval = setInterval(poll, 1000);
    return () => clearInterval(interval);
  }, [showLogs]);


  // Poll TUN status when in TUN mode
  useEffect(() => {
    if (proxyMode !== "tun") return;
    const poll = async () => {
      try {
        const s = await (window as any).tunProxy?.getStatus();
        if (s) setTunStatus(s.status === "active" ? "active" : "inactive");
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => clearInterval(interval);
  }, [proxyMode]);

  // Effective state based on mode
  const isConnected = proxyMode === "tun"
    ? tunStatus === "active"
    : socksStatus.status === "connected";
  const isLoading = proxyMode === "tun" ? tunLoading : socksLoading;
  const currentError = proxyMode === "tun" ? tunError : socksError;
  const uptime = proxyMode === "tun" ? 0 : socksStatus.uptime;
  const stats = useStats(isConnected);

  const handleModeChange = (m: ProxyMode) => {
    setProxyMode(m);
    localStorage.setItem("smurov-proxy-mode", m);
  };

  const tunConnect = useCallback(async (server: string, k: string) => {
    setTunLoading(true);
    setTunError(null);
    try {
      // Start SOCKS5 tunnel for browsers
      await connect(server, k);
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

  const tunDisconnect = useCallback(async () => {
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

  // Auto-connect on launch if key is saved
  useEffect(() => {
    if (!autoConnected.current && key && !isConnected && !isLoading) {
      autoConnected.current = true;
      if (proxyMode === "tun") {
        tunConnect(SERVER, key);
      } else {
        connect(SERVER, key);
      }
    }
  }, [key, isConnected, isLoading, connect, proxyMode, tunConnect]);

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
    autoConnected.current = false;
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
        <button
          onClick={() => (window as any).appInfo?.closeWindow()}
          style={{
            // @ts-ignore electron no-drag region
            WebkitAppRegion: "no-drag",
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

      <div style={{ display: "flex", alignItems: "baseline", gap: 8, marginBottom: 20 }}>
        <h1 style={{ fontSize: 20, fontWeight: 700 }}>SmurovProxy</h1>
      </div>
      <UpdateBanner />
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
          <button
            onClick={handleReset}
            style={{
              width: "100%",
              marginTop: 12,
              padding: "8px 0",
              background: "transparent",
              border: "1px solid #333",
              borderRadius: 8,
              color: "#888",
              fontSize: 13,
              cursor: "pointer",
            }}
          >
            Change key
          </button>
        </>
      )}

      <button
        onClick={() => setShowLogs(!showLogs)}
        style={{
          width: "100%",
          marginTop: 12,
          padding: "8px 0",
          background: "transparent",
          border: "1px solid #333",
          borderRadius: 8,
          color: showLogs ? "#4fc3f7" : "#666",
          fontSize: 13,
          cursor: "pointer",
        }}
      >
        {showLogs ? "Hide Logs" : "Logs"}
      </button>

      {showLogs && (
        <div
          style={{
            marginTop: 8,
            background: "#0a0e1a",
            border: "1px solid #222",
            borderRadius: 8,
            padding: 8,
            maxHeight: 200,
            overflowY: "auto",
            fontSize: 11,
            fontFamily: "monospace",
            color: "#aaa",
            lineHeight: 1.5,
          }}
        >
          {logs.length === 0 && <div style={{ color: "#555" }}>No logs yet</div>}
          {logs.map((line, i) => (
            <div key={i} style={{ color: line.includes("[helper]") ? "#81c784" : "#90caf9" }}>
              {line}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
