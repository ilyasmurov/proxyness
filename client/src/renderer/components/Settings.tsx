interface Props {
  server: string;
  secretKey: string;
  onServerChange: (v: string) => void;
  onKeyChange: (v: string) => void;
  disabled: boolean;
}

export function Settings({
  server,
  secretKey,
  onServerChange,
  onKeyChange,
  disabled,
}: Props) {
  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "10px 12px",
    background: "#16213e",
    border: "1px solid #333",
    borderRadius: 6,
    color: "#eee",
    fontSize: 14,
    marginTop: 4,
  };

  return (
    <div style={{ marginBottom: 20 }}>
      <label style={{ display: "block", marginBottom: 12 }}>
        <span style={{ fontSize: 13, color: "#aaa" }}>Server address</span>
        <input
          style={inputStyle}
          value={server}
          onChange={(e) => onServerChange(e.target.value)}
          placeholder="ip:port"
          disabled={disabled}
        />
      </label>
      <label style={{ display: "block" }}>
        <span style={{ fontSize: 13, color: "#aaa" }}>Secret key</span>
        <input
          style={inputStyle}
          type="password"
          value={secretKey}
          onChange={(e) => onKeyChange(e.target.value)}
          placeholder="hex key"
          disabled={disabled}
        />
      </label>
    </div>
  );
}
