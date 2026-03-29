import { useState, useEffect, useRef } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { StatusBar } from "./components/StatusBar";
import { ConnectionButton } from "./components/ConnectionButton";
import { UpdateBanner } from "./components/UpdateBanner";

const SERVER = "82.97.246.65:443";
const STORAGE_KEY = "smurov-proxy-key";

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [showSetup, setShowSetup] = useState(!key);
  const { status, error, loading, connect, disconnect } = useDaemon();
  const autoConnected = useRef(false);

  const isConnected = status.status === "connected";

  // Auto-connect on launch if key is saved
  useEffect(() => {
    if (!autoConnected.current && key && !isConnected && !loading) {
      autoConnected.current = true;
      connect(SERVER, key);
    }
  }, [key, isConnected, loading, connect]);

  const handleConnect = () => {
    if (!key.trim()) return;
    localStorage.setItem(STORAGE_KEY, key.trim());
    setShowSetup(false);
    connect(SERVER, key.trim());
  };

  const handleReset = () => {
    disconnect();
    localStorage.removeItem(STORAGE_KEY);
    setKey("");
    setShowSetup(true);
    autoConnected.current = false;
  };

  return (
    <div style={{ maxWidth: 360, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginBottom: 20, fontWeight: 700 }}>
        SmurovProxy
      </h1>
      <UpdateBanner />
      <StatusBar status={status.status} uptime={status.uptime} error={error} />

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
              onKeyDown={(e) => e.key === "Enter" && handleConnect()}
            />
          </label>
          <button
            onClick={handleConnect}
            disabled={loading || !key.trim()}
            style={{
              width: "100%",
              padding: "12px 0",
              background: "#4caf50",
              color: "#fff",
              border: "none",
              borderRadius: 8,
              fontSize: 16,
              fontWeight: 600,
              cursor: loading ? "wait" : "pointer",
              opacity: loading || !key.trim() ? 0.7 : 1,
            }}
          >
            {loading ? "..." : "Connect"}
          </button>
        </div>
      ) : (
        <>
          <ConnectionButton
            connected={isConnected}
            loading={loading}
            onConnect={() => connect(SERVER, key)}
            onDisconnect={disconnect}
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
            Change key
          </button>
        </>
      )}
    </div>
  );
}
