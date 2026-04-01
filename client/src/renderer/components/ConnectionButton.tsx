import { useState, useEffect } from "react";

interface Props {
  connected: boolean;
  loading: boolean;
  reconnecting?: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
}

function useAnimatedDots(active: boolean) {
  const [dots, setDots] = useState(1);
  useEffect(() => {
    if (!active) { setDots(1); return; }
    const id = setInterval(() => setDots(d => (d % 3) + 1), 400);
    return () => clearInterval(id);
  }, [active]);
  return ".".repeat(dots);
}

export function ConnectionButton({
  connected,
  loading,
  reconnecting,
  onConnect,
  onDisconnect,
}: Props) {
  const dots = useAnimatedDots(loading || !!reconnecting);
  const bg = reconnecting ? "transparent" : connected ? "#f44336" : "#4caf50";
  const label = reconnecting
    ? `Reconnecting${dots}`
    : loading
      ? `Connecting${dots}`
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
        color: reconnecting ? "#1a6be6" : "#fff",
        border: reconnecting ? "2px solid #0d47a1" : "none",
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
