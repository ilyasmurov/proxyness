import { useState, useEffect, useRef } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { StatusBar } from "./components/StatusBar";
import { Settings } from "./components/Settings";
import { ConnectionButton } from "./components/ConnectionButton";
import { UpdateBanner } from "./components/UpdateBanner";

const STORAGE_KEY = "smurov-proxy-settings";

function loadSettings() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return JSON.parse(raw);
  } catch {}
  return { server: "", key: "" };
}

function saveSettings(server: string, key: string) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify({ server, key }));
}

export function App() {
  const saved = loadSettings();
  const [server, setServer] = useState(saved.server);
  const [key, setKey] = useState(saved.key);
  const [showSettings, setShowSettings] = useState(!saved.server || !saved.key);
  const { status, error, loading, connect, disconnect } = useDaemon();
  const autoConnected = useRef(false);

  const isConnected = status.status === "connected";

  // Auto-connect on launch if settings are saved
  useEffect(() => {
    if (!autoConnected.current && server && key && !isConnected && !loading) {
      autoConnected.current = true;
      connect(server, key);
    }
  }, [server, key, isConnected, loading, connect]);

  const handleConnect = () => {
    saveSettings(server, key);
    setShowSettings(false);
    connect(server, key);
  };

  const handleDisconnect = () => {
    disconnect();
  };

  const handleReset = () => {
    disconnect();
    localStorage.removeItem(STORAGE_KEY);
    setServer("");
    setKey("");
    setShowSettings(true);
    autoConnected.current = false;
  };

  return (
    <div style={{ maxWidth: 360, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginBottom: 20, fontWeight: 700 }}>
        SmurovProxy
      </h1>
      <UpdateBanner />
      <StatusBar status={status.status} uptime={status.uptime} error={error} />

      {showSettings || !server || !key ? (
        <>
          <Settings
            server={server}
            secretKey={key}
            onServerChange={setServer}
            onKeyChange={setKey}
            disabled={false}
          />
          <ConnectionButton
            connected={false}
            loading={loading}
            onConnect={handleConnect}
            onDisconnect={handleDisconnect}
          />
        </>
      ) : (
        <>
          <div style={{ marginBottom: 16, padding: 12, background: "#16213e", borderRadius: 8 }}>
            <div style={{ fontSize: 13, color: "#aaa", marginBottom: 4 }}>Server</div>
            <div style={{ fontSize: 14 }}>{server}</div>
          </div>
          <ConnectionButton
            connected={isConnected}
            loading={loading}
            onConnect={() => connect(server, key)}
            onDisconnect={handleDisconnect}
          />
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
            Change settings
          </button>
        </>
      )}
    </div>
  );
}
