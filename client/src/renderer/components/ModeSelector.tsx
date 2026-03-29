import { useState } from "react";

export type ProxyMode = "tun" | "socks5";

interface Props {
  mode: ProxyMode;
  onChange: (mode: ProxyMode) => void;
  disabled?: boolean;
}

export function ModeSelector({ mode, onChange, disabled }: Props) {
  return (
    <div style={{ display: "flex", gap: 8, marginBottom: 16 }}>
      <button
        onClick={() => onChange("tun")}
        disabled={disabled}
        style={{
          flex: 1,
          padding: "8px 0",
          background: mode === "tun" ? "#1a3a5c" : "transparent",
          border: `1px solid ${mode === "tun" ? "#3b82f6" : "#333"}`,
          borderRadius: 8,
          color: mode === "tun" ? "#fff" : "#888",
          fontSize: 13,
          cursor: disabled ? "default" : "pointer",
          opacity: disabled ? 0.5 : 1,
        }}
      >
        Full (TUN)
      </button>
      <button
        onClick={() => onChange("socks5")}
        disabled={disabled}
        style={{
          flex: 1,
          padding: "8px 0",
          background: mode === "socks5" ? "#1a3a5c" : "transparent",
          border: `1px solid ${mode === "socks5" ? "#3b82f6" : "#333"}`,
          borderRadius: 8,
          color: mode === "socks5" ? "#fff" : "#888",
          fontSize: 13,
          cursor: disabled ? "default" : "pointer",
          opacity: disabled ? 0.5 : 1,
        }}
      >
        Browser only (SOCKS5)
      </button>
    </div>
  );
}
