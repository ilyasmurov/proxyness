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

interface RatePoint {
  t: number;
  down: number;
  up: number;
}

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
  download?: number;
  upload?: number;
  history?: RatePoint[];
}

const TRANSPORT_MODES: Array<{ key: string; label: string }> = [
  { key: "auto", label: "Auto" },
  { key: "udp", label: "UDP" },
  { key: "tls", label: "TLS" },
];

// ── Animated text: each character fades in with blur ──

function AnimatedText({
  text,
  startDelay,
  style,
}: {
  text: string;
  startDelay: number;
  style?: React.CSSProperties;
}) {
  return (
    <span style={{ display: "inline-flex", ...style }}>
      {text.split("").map((char, i) => (
        <span
          key={i}
          style={{
            opacity: 0,
            display: "inline-block",
            animation: `pn-letter-in 0.1s ease-out ${startDelay + i * 20}ms forwards`,
          }}
        >
          {char === " " ? "\u00A0" : char}
        </span>
      ))}
    </span>
  );
}

// ── Speed graph for disconnect button background ──

function formatSpeed(bps: number): string {
  if (bps < 1024) return `${bps} B/s`;
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
}

function GraphBackground({ history }: { history: RatePoint[] }) {
  const w = 120, h = 36;
  if (history.length < 2) return null;
  const maxVal = Math.max(1, ...history.map((p) => Math.max(p.down, p.up)));
  const step = w / (history.length - 1);
  const toPoints = (getter: (p: RatePoint) => number) =>
    history.map((p, i) => `${i * step},${h - (getter(p) / maxVal) * (h - 4) - 2}`).join(" ");

  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      style={{ position: "absolute", left: 0, right: 0, bottom: 0, height: "60%", width: "100%" }}
    >
      <polyline points={toPoints((p) => p.down)} fill="none" stroke="#4ade80" strokeWidth="2" opacity="0.5" />
      <polyline points={toPoints((p) => p.up)} fill="none" stroke="#60a5fa" strokeWidth="1.5" opacity="0.45" />
    </svg>
  );
}

// ── Main component ──

type VisualState = "disc" | "recon" | "conn";

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
  download = 0,
  upload = 0,
  history = [],
}: Props) {
  const [dismissed, setDismissed] = useState(false);
  const [dropdownOpen, setDropdownOpen] = useState(false);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const prevError = useRef(error);
  const badgeWrapRef = useRef<HTMLDivElement>(null);
  const barRef = useRef<HTMLDivElement>(null);
  const firstMount = useRef(true);
  const [animKey, setAnimKey] = useState(0);

  // Determine visual state
  const busy = loading || !!reconnecting;
  const visualState: VisualState = connected ? "conn" : busy ? "recon" : "disc";
  const prevState = useRef<VisualState>(visualState);

  // Bump animKey when visual state changes → re-mount animated content
  useEffect(() => {
    if (prevState.current !== visualState) {
      prevState.current = visualState;
      setAnimKey((k) => k + 1);
    }
  }, [visualState]);

  // Error dismiss timer
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
      if (badgeWrapRef.current && !badgeWrapRef.current.contains(e.target as Node)) {
        setDropdownOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [dropdownOpen]);

  // Bar entrance animation on first mount only
  useEffect(() => {
    firstMount.current = false;
  }, []);

  const showError = error && !dismissed;

  const handleModeSelect = (mode: string) => {
    setDropdownOpen(false);
    if (mode !== transportMode && onTransportChange) {
      onTransportChange(mode);
    }
  };

  // Animation timing
  const isEntrance = firstMount.current;
  const contentBase = isEntrance ? 900 : 0; // after bar expands on first mount
  let t = contentBase;

  return (
    <div style={{ marginBottom: 20 }}>
      {/* Bar container */}
      <div
        ref={barRef}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          padding: "12px 16px",
          background: "#16213e",
          borderRadius: 10,
          border: "none",
          ...(isEntrance
            ? {
                transform: "scale(0)",
                opacity: 0,
                animation: [
                  "pn-bar-dot 0.2s ease-out 0s forwards",
                  "pn-bar-vertical 0.25s ease-out 0.2s forwards",
                  "pn-bar-expand 0.35s ease-out 0.45s forwards",
                ].join(", "),
              }
            : {}),
        }}
      >
        {/* Animated content — key forces re-mount on state change */}
        <div key={animKey} style={{ display: "flex", alignItems: "center", gap: 8, width: "100%" }}>

          {/* Icon */}
          {busy ? (
            <div
              style={{
                width: 14,
                height: 14,
                border: "2px solid #2a3040",
                borderTopColor: "#2196f3",
                borderRadius: "50%",
                animation: `pn-fade-in 0.3s ease-out ${t}ms forwards, pn-spin 0.8s linear ${t + 300}ms infinite`,
                opacity: 0,
                flexShrink: 0,
                marginRight: 4,
              }}
            />
          ) : connected ? (
            <div style={{ opacity: 0, animation: `pn-fade-in 0.3s ease-out ${t}ms forwards` }}>
              <SpinningGlobe />
            </div>
          ) : (
            <div
              style={{
                width: 18,
                height: 18,
                borderRadius: "50%",
                border: "3px solid #0f1830",
                flexShrink: 0,
                marginRight: 4,
                opacity: 0,
                animation: `pn-fade-in 0.3s ease-out ${t}ms forwards`,
              }}
            />
          )}

          {/* Status text */}
          {(() => { t += 200; return null; })()}
          {reconnecting ? (
            <AnimatedText
              text="Reconnecting..."
              startDelay={t}
              style={{ fontSize: 16, fontWeight: 600 }}
            />
          ) : loading ? (
            <AnimatedText
              text="Connecting..."
              startDelay={t}
              style={{ fontSize: 16, fontWeight: 600 }}
            />
          ) : connected ? (
            <>
              <AnimatedText
                text="Connected"
                startDelay={t}
                style={{ fontSize: 16, fontWeight: 600 }}
              />
              {(() => { t += "Connected".length * 20; return null; })()}
              <AnimatedText
                text={`to ${server}`}
                startDelay={t}
                style={{ fontSize: 16, color: "#888" }}
              />
              {(() => { t += `to ${server}`.length * 20; return null; })()}
              {transport && (
                <div
                  ref={badgeWrapRef}
                  style={{
                    position: "relative",
                    opacity: 0,
                    animation: `pn-fade-only 0.3s ease-out ${t}ms forwards`,
                  }}
                >
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
                      fontFamily: "'Figtree', system-ui, sans-serif",
                      display: "inline-flex",
                      alignItems: "center",
                      gap: 3,
                      lineHeight: 1,
                    }}
                  >
                    {transport}
                    <span style={{ fontSize: 14, opacity: 0.6, lineHeight: 1 }}>▾</span>
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
                              fontFamily: "'Figtree', system-ui, sans-serif",
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
                              <span style={{ color: "#4caf50", fontSize: 11 }}>✓</span>
                            )}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              )}
              {(() => { t += 50; return null; })()}
            </>
          ) : (
            <AnimatedText
              text="Disconnected"
              startDelay={t}
              style={{ fontSize: 16, fontWeight: 600 }}
            />
          )}

          {/* Right group */}
          <div style={{ display: "flex", alignItems: "center", gap: 20, marginLeft: "auto" }}>
            {connected && !busy && (
              <>
                <AnimatedText
                  text={formatUptime(uptime)}
                  startDelay={t}
                  style={{
                    color: "#aaa",
                    fontSize: 13,
                    fontFamily: "'Barlow', system-ui, sans-serif",
                  }}
                />
                {(() => { t += formatUptime(uptime).length * 20; return null; })()}
                <div style={{ display: "flex", flexDirection: "column", alignItems: "flex-start", gap: 1, minWidth: 75 }}>
                  <AnimatedText
                    text={`\u2193 ${formatSpeed(download)}`}
                    startDelay={t}
                    style={{
                      color: "#4ade80",
                      fontSize: 11,
                      fontFamily: "'Barlow', system-ui, sans-serif",
                      lineHeight: 1.1,
                    }}
                  />
                  <AnimatedText
                    text={`\u2191 ${formatSpeed(upload)}`}
                    startDelay={t}
                    style={{
                      color: "#60a5fa",
                      fontSize: 11,
                      fontFamily: "'Barlow', system-ui, sans-serif",
                      lineHeight: 1.1,
                    }}
                  />
                </div>
                {(() => { t += `\u2193 ${formatSpeed(download)}`.length * 20; return null; })()}
              </>
            )}

            {/* Action button */}
            {connected ? (
              <button
                onClick={onDisconnect}
                style={{
                  position: "relative",
                  padding: "8px 22px",
                  background: "#2a0a0a",
                  color: "#f44336",
                  border: "1px solid #f44336",
                  borderRadius: 6,
                  fontSize: 14,
                  fontWeight: 600,
                  cursor: "pointer",
                  flexShrink: 0,
                  overflow: "hidden",
                  minWidth: 120,
                  height: 36,
                  opacity: 0,
                  animation: `pn-fade-only 0.3s ease-out ${t}ms forwards`,
                }}
              >
                <GraphBackground history={history} />
                <span style={{ position: "relative", zIndex: 1 }}>Disconnect</span>
              </button>
            ) : (
              <button
                onClick={onConnect}
                disabled={loading}
                style={{
                  padding: "8px 22px",
                  background: busy ? "#2a3040" : "#4caf50",
                  color: "#fff",
                  border: "none",
                  borderRadius: 6,
                  fontSize: 14,
                  fontWeight: 600,
                  cursor: busy ? "wait" : "pointer",
                  opacity: 0,
                  flexShrink: 0,
                  animation: `pn-fade-only 0.3s ease-out ${t + ("Disconnected".length * 20) + 100}ms forwards`,
                }}
              >
                {busy ? (reconnecting ? "Disconnect" : "Connecting...") : "Connect"}
              </button>
            )}
          </div>
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
          <span style={{ color: "#f44336", fontSize: 13, flex: 1 }}>{error}</span>
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

// ── Spinning Globe ──

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
          animation: "pn-globe-scroll 10s linear infinite",
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
