import React from "react";

interface RatePoint {
  t: number;
  down: number;
  up: number;
}

interface Props {
  download: number;
  upload: number;
  history: RatePoint[];
}

function formatSpeed(bps: number): string {
  if (bps < 1024) return `${bps} B/s`;
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
}

export function SpeedGraph({ download, upload, history }: Props) {
  const width = 200;
  const height = 30;

  const maxVal = Math.max(1, ...history.map((p) => Math.max(p.down, p.up)));

  const toPoints = (getter: (p: RatePoint) => number): string => {
    if (history.length < 2) return "";
    const step = width / (history.length - 1);
    return history
      .map((p, i) => `${i * step},${height - (getter(p) / maxVal) * (height - 2)}`)
      .join(" ");
  };

  return (
    <div
      style={{
        background: "#12122a",
        borderRadius: 6,
        padding: 8,
        marginBottom: 10,
      }}
    >
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          fontSize: 9,
          color: "#888",
          marginBottom: 4,
        }}
      >
        <span style={{ color: "#4ade80" }}>↓ {formatSpeed(download)}</span>
        <span style={{ color: "#60a5fa" }}>↑ {formatSpeed(upload)}</span>
      </div>
      <svg viewBox={`0 0 ${width} ${height}`} style={{ width: "100%", height: 28 }}>
        {history.length > 1 && (
          <>
            <polyline
              points={toPoints((p) => p.down)}
              fill="none"
              stroke="#4ade80"
              strokeWidth="1.5"
            />
            <polyline
              points={toPoints((p) => p.up)}
              fill="none"
              stroke="#60a5fa"
              strokeWidth="1.5"
            />
          </>
        )}
      </svg>
    </div>
  );
}
