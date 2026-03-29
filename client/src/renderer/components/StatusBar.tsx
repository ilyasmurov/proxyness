interface Props {
  status: string;
  uptime: number;
  error: string | null;
}

export function StatusBar({ status, uptime, error }: Props) {
  const color = status === "connected" ? "#4caf50" : "#f44336";
  const label = status.charAt(0).toUpperCase() + status.slice(1);

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
      </div>
      {error && (
        <div style={{ color: "#f44336", marginTop: 8, fontSize: 13 }}>
          {error}
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
