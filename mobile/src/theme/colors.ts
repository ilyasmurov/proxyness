// Panorama palette ported from client/src/renderer/App.tsx.
// React Native supports OKLCH on RN >= 0.76 / Hermes on New Arch,
// but not reliably on all render paths (shadows, borders) — so the
// palette is pre-resolved to hex for predictable rendering.
// Source OKLCH values are kept in the comments for reference.

export const colors = {
  bg0: "#1b1e25",
  bg1: "#23262e",
  bg2: "#2c2f37",
  bgh: "#3d4049",
  b1: "#383b43",
  t1: "#e6e8ed",
  t2: "#8d909a",
  t3: "#5b5e67",
  am: "#f5b74a",
  amd: "#b27d29",
  amb: "#3a2c17",
  bl: "#4fb7e9",
  gn: "#4fc98a",
  rd: "#d84a3a",
  rdb: "#2a1714",
  dangerBorder: "rgba(216,74,58,0.2)",
  dangerBorderHover: "rgba(216,74,58,0.4)",
  amberBorder: "rgba(245,183,74,0.2)",
  amberBadgeBg: "rgba(245,183,74,0.12)",
  overlayTop: "rgba(27,30,37,0.3)",
  overlayMid: "rgba(27,30,37,0.85)",
  overlayBot: "rgba(27,30,37,0.95)",
} as const;

export type ColorToken = keyof typeof colors;
