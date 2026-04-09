import { useState, useEffect, useRef } from "react";

declare global {
  interface Window {
    updater?: {
      checkVersion: () => Promise<{ hasUpdate: boolean; latestVersion: string | null; error?: boolean }>;
      downloadUpdate: () => void;
      installUpdate: () => void;
      onUpdateProgress: (cb: (percent: number) => void) => void;
      onUpdateDownloaded: (cb: () => void) => void;
      onUpdateError: (cb: () => void) => void;
      onUpdateAvailable: (cb: (version: string) => void) => void;
    };
  }
}

type State = "idle" | "checking" | "update" | "downloading" | "ready" | "uptodate" | "error";

export function UpdateBanner() {
  const [state, setState] = useState<State>("idle");
  const [version, setVersion] = useState("");
  const [progress, setProgress] = useState(0);

  // Mirror state in a ref so the onUpdateAvailable handler (registered
  // once in the []-deps effect) can read the latest state without having
  // to re-register on every transition.
  const stateRef = useRef(state);
  stateRef.current = state;

  useEffect(() => {
    if (!window.updater) return;
    window.updater.onUpdateProgress((p) => {
      if (p < 0) {
        // No content-length: p is negative bytes downloaded
        setProgress(Math.round(-p / 1024 / 1024));
      } else {
        setProgress(p);
      }
    });
    window.updater.onUpdateDownloaded(() => setState("ready"));
    window.updater.onUpdateError(() => {
      setState("error");
      setTimeout(() => setState("idle"), 3000);
    });
    // Pushed from main process: background poller / show+focus hooks.
    // Don't clobber an active download or a ready-to-install state — if
    // the user already started the flow for this or an older version, let
    // them finish without the banner flipping underneath them.
    window.updater.onUpdateAvailable((nextVersion) => {
      if (stateRef.current === "downloading" || stateRef.current === "ready") return;
      setVersion(nextVersion);
      setState("update");
    });
    // Silent auto-check on startup
    window.updater.checkVersion().then((r) => {
      if (r?.hasUpdate && r.latestVersion) {
        setVersion(r.latestVersion);
        setState("update");
      }
    }).catch(() => {});
  }, []);

  const handleCheck = async () => {
    setState("checking");
    try {
      const r = await window.updater?.checkVersion();
      if (!r || r.error) {
        setState("error");
        setTimeout(() => setState("idle"), 3000);
        return;
      }
      if (r.hasUpdate && r.latestVersion) {
        setVersion(r.latestVersion);
        setState("update");
      } else {
        setState("uptodate");
        setTimeout(() => setState("idle"), 3000);
      }
    } catch {
      setState("error");
      setTimeout(() => setState("idle"), 3000);
    }
  };

  if (state === "error") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, color: "#ef4444", fontSize: 13, textAlign: "center" }}>
        Connection error
      </div>
    );
  }

  if (state === "idle") {
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

  if (state === "checking") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, color: "#888", fontSize: 13, textAlign: "center" }}>
        Checking for updates...
      </div>
    );
  }

  if (state === "uptodate") {
    return (
      <div style={{ padding: "10px 12px", marginBottom: 16, color: "#555", fontSize: 13, textAlign: "center" }}>
        Up to date
      </div>
    );
  }

  return (
    <div style={{
      padding: "10px 12px", marginBottom: 16,
      background: "#1a2744", border: "1px solid #2a4a7a",
      borderRadius: 8, fontSize: 13,
    }}>
      {state === "ready" ? (
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
      ) : state === "downloading" ? (
        <div>
          <div style={{ marginBottom: 4 }}>Downloading v{version}... {progress >= 0 ? `${progress}%` : `${-progress} MB`}</div>
          <div style={{ height: 4, background: "#333", borderRadius: 2 }}>
            <div style={{ height: 4, width: `${progress}%`, background: "#3b82f6", borderRadius: 2 }} />
          </div>
        </div>
      ) : (
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>Update v{version} available</span>
          <button
            onClick={() => { setState("downloading"); window.updater?.downloadUpdate(); }}
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
