import { useState, useEffect, useRef } from "react";

interface Props {
  status: string;
  uptime: number;
  error: string | null;
  transport?: string;
}

export function StatusBar({ status, uptime, error, transport }: Props) {
  const color = status === "connected" ? "#4caf50" : "#f44336";
  const label = status.charAt(0).toUpperCase() + status.slice(1);
  const [dismissed, setDismissed] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const prevError = useRef(error);

  useEffect(() => {
    if (error !== prevError.current) {
      setDismissed(false);
      prevError.current = error;
    }
    if (!error) return;
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => setDismissed(true), 15000);
    return () => { if (timerRef.current) clearTimeout(timerRef.current); };
  }, [error]);

  const showError = error && !dismissed;

  return (
    <div style={{ marginBottom: 20 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div
          style={{
            width: 12,
            height: 12,
            borderRadius: "50%",
            background: color,
          }}
        />
        <span style={{ fontSize: 18, fontWeight: 600 }}>{label}</span>
        {status === "connected" && (
          <span style={{ color: "#aaa", fontSize: 14 }}>
            {formatUptime(uptime)}
          </span>
        )}
        {status === "connected" && transport && (
          <span style={{
            fontSize: 11,
            padding: "2px 6px",
            borderRadius: 4,
            background: transport === "udp" ? "#1a3a5c" : "#2a1a3a",
            color: "#ccc",
          }}>
            {transport.toUpperCase()}
          </span>
        )}
      </div>
      {showError && (
        <div style={{ display: "flex", alignItems: "center", gap: 6, marginTop: 8 }}>
          <span style={{ color: "#f44336", fontSize: 13, flex: 1 }}>{error}</span>
          <button
            onClick={() => setDismissed(true)}
            style={{
              background: "none", border: "none", color: "#f44336",
              cursor: "pointer", fontSize: 14, padding: 0, lineHeight: 1,
            }}
          >
            ✕
          </button>
        </div>
      )}
    </div>
  );
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return [h, m, s].map((v) => String(v).padStart(2, "0")).join(":");
}
