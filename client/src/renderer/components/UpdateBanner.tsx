import { useState, useEffect } from "react";

declare global {
  interface Window {
    updater?: {
      onUpdateAvailable: (cb: (version: string) => void) => void;
      onUpdateDownloaded: (cb: () => void) => void;
      onUpdateProgress: (cb: (percent: number) => void) => void;
      onUpdateNotAvailable: (cb: () => void) => void;
      downloadUpdate: () => void;
      installUpdate: () => void;
      checkForUpdates: () => void;
    };
  }
}

export function UpdateBanner() {
  const [version, setVersion] = useState<string | null>(null);
  const [downloading, setDownloading] = useState(false);
  const [checking, setChecking] = useState(false);
  const [progress, setProgress] = useState(0);
  const [ready, setReady] = useState(false);
  const [upToDate, setUpToDate] = useState(false);

  useEffect(() => {
    if (!window.updater) return;
    window.updater.onUpdateAvailable((v) => { setVersion(v); setChecking(false); setUpToDate(false); });
    window.updater.onUpdateProgress((p) => setProgress(p));
    window.updater.onUpdateDownloaded(() => { setDownloading(false); setReady(true); });
    window.updater.onUpdateNotAvailable(() => { setChecking(false); setUpToDate(true); });
  }, []);

  const handleCheck = () => {
    setChecking(true);
    setUpToDate(false);
    window.updater?.checkForUpdates();
  };

  if (!version && !checking && !upToDate) {
    return (
      <button
        onClick={handleCheck}
        style={{
          width: "100%", padding: "8px 0", marginBottom: 16,
          background: "transparent", border: "1px solid #333",
          borderRadius: 8, color: "#888", fontSize: 13, cursor: "pointer",
        }}
      >
        Check for updates
      </button>
    );
  }

  if (checking) {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, color: "#888", fontSize: 13, textAlign: "center" }}>
        Checking for updates...
      </div>
    );
  }

  if (upToDate && !version) {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, color: "#555", fontSize: 13, textAlign: "center" }}>
        Up to date
      </div>
    );
  }

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
