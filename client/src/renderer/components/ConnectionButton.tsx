interface Props {
  connected: boolean;
  loading: boolean;
  reconnecting?: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
}

export function ConnectionButton({
  connected,
  loading,
  reconnecting,
  onConnect,
  onDisconnect,
}: Props) {
  const bg = reconnecting ? "#ff9800" : connected ? "#f44336" : "#4caf50";
  const label = reconnecting
    ? "Reconnecting..."
    : loading
      ? "..."
      : connected
        ? "Disconnect"
        : "Connect";

  return (
    <button
      onClick={connected ? onDisconnect : onConnect}
      disabled={loading || reconnecting}
      style={{
        width: "100%",
        padding: "12px 0",
        background: bg,
        color: "#fff",
        border: "none",
        borderRadius: 8,
        fontSize: 16,
        fontWeight: 600,
        cursor: loading || reconnecting ? "wait" : "pointer",
        opacity: loading || reconnecting ? 0.7 : 1,
      }}
    >
      {label}
    </button>
  );
}
