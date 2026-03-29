interface Props {
  connected: boolean;
  loading: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
}

export function ConnectionButton({
  connected,
  loading,
  onConnect,
  onDisconnect,
}: Props) {
  const bg = connected ? "#f44336" : "#4caf50";
  const label = loading
    ? "..."
    : connected
      ? "Disconnect"
      : "Connect";

  return (
    <button
      onClick={connected ? onDisconnect : onConnect}
      disabled={loading}
      style={{
        width: "100%",
        padding: "12px 0",
        background: bg,
        color: "#fff",
        border: "none",
        borderRadius: 8,
        fontSize: 16,
        fontWeight: 600,
        cursor: loading ? "wait" : "pointer",
        opacity: loading ? 0.7 : 1,
      }}
    >
      {label}
    </button>
  );
}
