export type ProxyMode = "tun" | "socks5";

interface Props {
  mode: ProxyMode;
  onChange: (mode: ProxyMode) => void;
  disabled?: boolean;
}

export function ModeSelector({ mode, onChange, disabled }: Props) {
  const options: { value: ProxyMode; label: string }[] = [
    { value: "tun", label: "Full (TUN)" },
    { value: "socks5", label: "Browser only" },
  ];
  return (
    <div
      style={{
        display: "inline-flex",
        padding: 2,
        background: "#0f1420",
        border: "none",
        borderRadius: 8,
        opacity: disabled ? 0.5 : 1,
      }}
    >
      {options.map((opt) => {
        const active = mode === opt.value;
        return (
          <button
            key={opt.value}
            onClick={() => !disabled && onChange(opt.value)}
            disabled={disabled}
            style={{
              padding: "5px 12px",
              background: active ? "#1a3a5c" : "transparent",
              border: `1px solid ${active ? "#3b82f6" : "transparent"}`,
              borderRadius: 6,
              color: active ? "#fff" : "#888",
              fontSize: 12,
              fontWeight: active ? 600 : 400,
              cursor: disabled ? "default" : "pointer",
              transition: "background 0.15s, color 0.15s",
            }}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}
