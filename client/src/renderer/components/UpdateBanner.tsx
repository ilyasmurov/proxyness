import { useState, useEffect } from "react";

declare global {
  interface Window {
    updater?: {
      onUpdateAvailable: (cb: (version: string) => void) => void;
      onUpdateDownloaded: (cb: () => void) => void;
      onUpdateProgress: (cb: (percent: number) => void) => void;
      downloadUpdate: () => void;
      installUpdate: () => void;
    };
  }
}

export function UpdateBanner() {
  const [version, setVersion] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    if (!window.updater) return;
    window.updater.onUpdateAvailable((v) => setVersion(v));
    window.updater.onUpdateProgress((p) => setProgress(p));
    window.updater.onUpdateDownloaded(() => { setDownloading(false); setReady(true); });
  }, []);

  if (!version) return null;

  return (
    <div style={{
      padding: "10px 12px",
      marginBottom: 16,
      background: "#1a2744",
      border: "1px solid #2a4a7a",
      borderRadius: 8,
      fontSize: 13,
    }}>
      {ready ? (
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>v{version} ready</span>
          <button
            onClick={() => window.updater?.installUpdate()}
            style={{
              padding: "4px 12px", background: "#3b82f6", color: "#fff",
              border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer",
            }}
          >
            Restart & Update
          </button>
        </div>
      ) : downloading ? (
        <div>
          <div style={{ marginBottom: 4 }}>Downloading v{version}... {progress}%</div>
          <div style={{ height: 4, background: "#333", borderRadius: 2 }}>
            <div style={{ height: 4, width: `${progress}%`, background: "#3b82f6", borderRadius: 2 }} />
          </div>
        </div>
      ) : (
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>Update v{version} available</span>
          <button
            onClick={() => { setDownloading(true); window.updater?.downloadUpdate(); }}
            style={{
              padding: "4px 12px", background: "#3b82f6", color: "#fff",
              border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer",
            }}
          >
            Update
          </button>
        </div>
      )}
    </div>
  );
}
