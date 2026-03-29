import { useState } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { StatusBar } from "./components/StatusBar";
import { Settings } from "./components/Settings";
import { ConnectionButton } from "./components/ConnectionButton";

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
  const { status, error, loading, connect, disconnect } = useDaemon();

  const isConnected = status.status === "connected";

  const handleConnect = () => {
    saveSettings(server, key);
    connect(server, key);
  };

  return (
    <div style={{ maxWidth: 360, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginBottom: 20, fontWeight: 700 }}>
        SmurovProxy
      </h1>
      <StatusBar status={status.status} uptime={status.uptime} error={error} />
      <Settings
        server={server}
        secretKey={key}
        onServerChange={setServer}
        onKeyChange={setKey}
        disabled={isConnected}
      />
      <ConnectionButton
        connected={isConnected}
        loading={loading}
        onConnect={handleConnect}
        onDisconnect={disconnect}
      />
    </div>
  );
}
