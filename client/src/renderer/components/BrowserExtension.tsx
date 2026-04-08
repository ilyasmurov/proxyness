import { useEffect, useState } from "react";

export function BrowserExtension() {
  const [token, setToken] = useState<string>("");
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    (window as any).appInfo?.getDaemonToken?.().then((t: string) => setToken(t));
  }, []);

  const copy = async () => {
    if (!token) return;
    await navigator.clipboard.writeText(token);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div style={{ padding: 24, maxWidth: 600 }}>
      <h2 style={{ marginTop: 0 }}>Browser Extension</h2>
      <p style={{ color: "#888", marginBottom: 16 }}>
        Установи расширение в Chrome / Edge / Brave (см. <code>extension/README.md</code>),
        затем открой расширение и вставь токен ниже:
      </p>
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <input
          readOnly
          value={token || "(daemon not running)"}
          style={{
            flex: 1,
            fontFamily: "monospace",
            fontSize: 12,
            padding: "8px 12px",
            background: "#1a1f2e",
            border: "1px solid #2a3042",
            color: "#aab3c5",
            borderRadius: 4,
          }}
          onClick={(e) => (e.target as HTMLInputElement).select()}
        />
        <button
          onClick={copy}
          disabled={!token}
          style={{
            padding: "8px 16px",
            background: copied ? "#22c55e" : "#3b82f6",
            color: "#fff",
            border: "none",
            borderRadius: 4,
            cursor: token ? "pointer" : "not-allowed",
          }}
        >
          {copied ? "Copied!" : "Copy"}
        </button>
      </div>
      <p style={{ color: "#666", fontSize: 12, marginTop: 16 }}>
        Токен генерируется автоматически при первом старте демона. Если хочешь
        отозвать доступ — удали файл <code>~/.config/smurov-proxy/daemon-token</code>
        и перезапусти клиент.
      </p>
    </div>
  );
}
