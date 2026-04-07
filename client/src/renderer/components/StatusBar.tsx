import { useState, useEffect, useRef } from "react";
import worldMapSvg from "../assets/world-map.svg?raw";

// Extract inner paths from the source SVG so we can re-wrap them in our own
// <svg> element with a doubled width for seamless horizontal scrolling.
const mapInner = (() => {
  const openIdx = worldMapSvg.indexOf(">", worldMapSvg.indexOf("<svg")) + 1;
  const closeIdx = worldMapSvg.lastIndexOf("</svg>");
  return worldMapSvg.slice(openIdx, closeIdx);
})();

// Source viewBox: "30.767 241.591 784.077 458.627"
const VB_X_MIN = 30.767;
const VB_Y_MIN = 241.591;
const VB_WIDTH = 784.077;
const VB_HEIGHT = 458.627;
const DOUBLED_VIEW_BOX = `${VB_X_MIN} ${VB_Y_MIN} ${VB_WIDTH * 2} ${VB_HEIGHT}`;
const doubledMap =
  mapInner + `<g transform="translate(${VB_WIDTH},0)">${mapInner}</g>`;

interface Props {
  connected: boolean;
  loading: boolean;
  reconnecting?: boolean;
  server: string;
  uptime: number;
  transport?: string;
  transportMode?: string;
  error: string | null;
  onConnect: () => void;
  onDisconnect: () => void;
  onTransportChange?: (mode: string) => void;
}

const TRANSPORT_MODES: Array<{ key: string; label: string }> = [
  { key: "auto", label: "Auto" },
  { key: "udp", label: "UDP" },
  { key: "tls", label: "TLS" },
];

export function StatusBar({
  connected,
  loading,
  reconnecting,
  server,
  uptime,
  transport,
  transportMode = "auto",
  error,
  onConnect,
  onDisconnect,
  onTransportChange,
}: Props) {
  const [dismissed, setDismissed] = useState(false);
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const prevError = useRef(error);
  const badgeWrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (error !== prevError.current) {
      setDismissed(false);
      prevError.current = error;
    }
    if (!error) return;
    if (timerRef.current) clearTimeout(timerRef.current);
    timerRef.current = setTimeout(() => setDismissed(true), 15000);
    return () => {
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [error]);

  // Close transport dropdown on outside click
  useEffect(() => {
    if (!dropdownOpen) return;
    const handler = (e: MouseEvent) => {
      if (
        badgeWrapRef.current &&
        !badgeWrapRef.current.contains(e.target as Node)
      ) {
        setDropdownOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [dropdownOpen]);

  const showError = error && !dismissed;
  const busy = loading || !!reconnecting;

  const handleModeSelect = (mode: string) => {
    setDropdownOpen(false);
    if (mode !== transportMode && onTransportChange) {
      onTransportChange(mode);
    }
  };

  return (
    <div style={{ marginBottom: 20 }}>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "12px 16px",
          background: "#16213e",
          borderRadius: 10,
          border: "1px solid #222",
        }}
      >
        {/* Status indicator */}
        {busy ? (
          <div
            style={{
              width: 14,
              height: 14,
              border: "2px solid #2a3040",
              borderTopColor: "#2196f3",
              borderRadius: "50%",
              animation: "smurov-spin 0.8s linear infinite",
              flexShrink: 0,
              marginRight: 4,
            }}
          />
        ) : connected ? (
          <SpinningGlobe />
        ) : (
          <div
            style={{
              width: 11,
              height: 11,
              borderRadius: "50%",
              background: "transparent",
              border: "1px solid #f44336",
              flexShrink: 0,
              marginRight: 5,
              marginLeft: 2,
            }}
          />
        )}

        {/* Status text */}
        {reconnecting ? (
          <span style={{ fontSize: 16, fontWeight: 600 }}>Reconnecting...</span>
        ) : loading ? (
          <span style={{ fontSize: 16, fontWeight: 600 }}>Connecting...</span>
        ) : connected ? (
          <>
            <span style={{ fontSize: 16, fontWeight: 600 }}>Connected</span>
            <span style={{ fontSize: 16, fontWeight: 400, color: "#888" }}>
              to {server}
            </span>
            {transport && (
              <div ref={badgeWrapRef} style={{ position: "relative" }}>
                <button
                  onClick={() => setDropdownOpen((v) => !v)}
                  style={{
                    fontSize: 11,
                    padding: "3px 7px 3px 8px",
                    borderRadius: 4,
                    background: transport === "udp" ? "#1a3a5c" : "#2a1a3a",
                    color: "#ccc",
                    textTransform: "uppercase",
                    fontWeight: 600,
                    letterSpacing: 0.5,
                    border: "none",
                    cursor: "pointer",
                    fontFamily: "inherit",
                    display: "inline-flex",
                    alignItems: "center",
                    gap: 3,
                    lineHeight: 1,
                  }}
                >
                  {transport}
                  <span style={{ fontSize: 14, opacity: 0.6, lineHeight: 1 }}>
                    ▾
                  </span>
                </button>
                {dropdownOpen && (
                  <div
                    style={{
                      position: "absolute",
                      top: "calc(100% + 4px)",
                      left: 0,
                      minWidth: 120,
                      background: "#1a1f2e",
                      border: "1px solid #333",
                      borderRadius: 6,
                      padding: 4,
                      zIndex: 50,
                      boxShadow: "0 4px 16px rgba(0, 0, 0, 0.5)",
                    }}
                  >
                    {TRANSPORT_MODES.map((m) => {
                      const selected = transportMode === m.key;
                      return (
                        <button
                          key={m.key}
                          onClick={() => handleModeSelect(m.key)}
                          style={{
                            display: "flex",
                            justifyContent: "space-between",
                            alignItems: "center",
                            width: "100%",
                            textAlign: "left",
                            padding: "6px 10px",
                            background: "transparent",
                            border: "none",
                            borderRadius: 4,
                            color: "#ccc",
                            fontSize: 12,
                            fontWeight: 600,
                            textTransform: "uppercase",
                            letterSpacing: 0.5,
                            cursor: "pointer",
                            fontFamily: "inherit",
                          }}
                          onMouseEnter={(e) => {
                            e.currentTarget.style.background = "#2a3040";
                          }}
                          onMouseLeave={(e) => {
                            e.currentTarget.style.background = "transparent";
                          }}
                        >
                          <span>{m.label}</span>
                          {selected && (
                            <span style={{ color: "#4caf50", fontSize: 11 }}>
                              ✓
                            </span>
                          )}
                        </button>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
          </>
        ) : (
          <span style={{ fontSize: 16, fontWeight: 600 }}>Disconnected</span>
        )}

        {/* Right-aligned group: uptime + connect/disconnect button */}
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 20,
            marginLeft: "auto",
          }}
        >
          {connected && !busy && (
            <span
              style={{
                color: "#aaa",
                fontSize: 13,
                fontFamily: "ui-monospace, SFMono-Regular, Menlo, monospace",
              }}
            >
              {formatUptime(uptime)}
            </span>
          )}
          <button
            onClick={connected ? onDisconnect : onConnect}
            disabled={busy}
            style={{
              padding: "8px 22px",
              background: busy
                ? "#2a3040"
                : connected
                  ? "#f44336"
                  : "#4caf50",
              color: "#fff",
              border: "none",
              borderRadius: 6,
              fontSize: 14,
              fontWeight: 600,
              cursor: busy ? "wait" : "pointer",
              opacity: busy ? 0.6 : 1,
              flexShrink: 0,
              transition: "background 0.15s ease",
            }}
          >
            {connected ? "Disconnect" : "Connect"}
          </button>
        </div>
      </div>

      {/* Error row */}
      {showError && (
        <div
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            marginTop: 8,
            padding: "0 4px",
          }}
        >
          <span style={{ color: "#f44336", fontSize: 13, flex: 1 }}>
            {error}
          </span>
          <button
            onClick={() => setDismissed(true)}
            style={{
              background: "none",
              border: "none",
              color: "#f44336",
              cursor: "pointer",
              fontSize: 14,
              padding: 0,
              lineHeight: 1,
            }}
          >
            ✕
          </button>
        </div>
      )}
    </div>
  );
}

// Wireframe Earth: real world map scrolling inside a circular clip with
// filled continents, an outer green ring, and sphere lighting. This is
// variant B "filled + ring + shadow".
function SpinningGlobe() {
  return (
    <div
      style={{
        width: 18,
        height: 18,
        borderRadius: "50%",
        overflow: "hidden",
        position: "relative",
        flexShrink: 0,
        marginRight: 4,
        boxShadow:
          "inset -3px 0 6px 1px rgba(0,0,0,0.7), inset 3px 0 6px 1px rgba(0,0,0,0.7), inset 0 -1px 3px rgba(0,0,0,0.4), 0 0 3px rgba(76,175,80,0.55)",
      }}
    >
      <svg
        viewBox={DOUBLED_VIEW_BOX}
        preserveAspectRatio="none"
        style={{
          display: "block",
          width: "200%",
          height: "100%",
          animation: "smurov-globe-scroll 10s linear infinite",
          WebkitMaskImage:
            "radial-gradient(ellipse 75% 110% at center, black 55%, transparent 100%)",
          maskImage:
            "radial-gradient(ellipse 75% 110% at center, black 55%, transparent 100%)",
          fill: "#4caf50",
          stroke: "none",
        }}
        dangerouslySetInnerHTML={{ __html: doubledMap }}
      />
      <div
        style={{
          position: "absolute",
          inset: 0,
          borderRadius: "50%",
          border: "1px solid #4caf50",
          pointerEvents: "none",
          zIndex: 3,
        }}
      />
      <div
        style={{
          position: "absolute",
          inset: 0,
          borderRadius: "50%",
          background:
            "radial-gradient(circle at 35% 30%, rgba(255,255,255,0.15), transparent 50%)",
          pointerEvents: "none",
          zIndex: 2,
        }}
      />
    </div>
  );
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return [h, m, s].map((v) => String(v).padStart(2, "0")).join(":");
}
