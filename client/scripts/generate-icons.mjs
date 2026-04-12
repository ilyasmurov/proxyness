#!/usr/bin/env node
/**
 * Generate all icon assets from the Cyclops Ghost SVG.
 *
 * Outputs:
 *   build/icon.png            — 512x512 app icon (dark bg, colored ghost, open eye)
 *   build/icon256.png         — 256x256 app icon
 *   build/trayTemplate.png             — 16x16  macOS tray disconnected (black on transparent)
 *   build/trayTemplate@2x.png          — 32x32  macOS tray disconnected
 *   build/trayConnectedTemplate.png    — 16x16  macOS tray connected
 *   build/trayConnectedTemplate@2x.png — 32x32  macOS tray connected
 *   build/tray.png                     — 16x16  Windows tray disconnected (colored)
 *   build/trayConnected.png            — 16x16  Windows tray connected (colored)
 */

import sharp from "sharp";
import { writeFileSync, mkdirSync } from "fs";
import { join, dirname } from "path";
import { fileURLToPath } from "url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const BUILD = join(__dirname, "..", "build");
mkdirSync(BUILD, { recursive: true });

// ── SVG generators ──────────────────────────────────────────

/** Full-color app icon: dark rounded-rect bg + ghost + amber eye */
function appIconSvg(size) {
  const s = size;
  const r = s * 0.18; // corner radius
  const cx = s / 2, cy = s * 0.46;
  const bodyR = s * 0.32;
  // Ghost body scaled to sit nicely inside the rounded rect
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${s}" height="${s}" viewBox="0 0 ${s} ${s}">
  <rect width="${s}" height="${s}" rx="${r}" fill="#1a1d2e"/>
  <path d="
    M${cx} ${cy - bodyR}
    C${cx - bodyR * 0.85} ${cy - bodyR} ${cx - bodyR} ${cy - bodyR * 0.45} ${cx - bodyR} ${cy + bodyR * 0.1}
    L${cx - bodyR} ${cy + bodyR * 0.95}
    L${cx - bodyR * 0.72} ${cy + bodyR * 0.72}
    L${cx - bodyR * 0.44} ${cy + bodyR * 0.95}
    L${cx - bodyR * 0.16} ${cy + bodyR * 0.72}
    L${cx + bodyR * 0.16} ${cy + bodyR * 0.95}
    L${cx + bodyR * 0.44} ${cy + bodyR * 0.72}
    L${cx + bodyR * 0.72} ${cy + bodyR * 0.95}
    L${cx + bodyR} ${cy + bodyR * 0.72}
    L${cx + bodyR} ${cy + bodyR * 0.1}
    C${cx + bodyR} ${cy - bodyR * 0.45} ${cx + bodyR * 0.85} ${cy - bodyR} ${cx} ${cy - bodyR}
    Z" fill="#2d3148"/>
  <path d="
    M${cx} ${cy - bodyR}
    C${cx + bodyR * 0.6} ${cy - bodyR} ${cx + bodyR * 0.85} ${cy - bodyR * 0.6} ${cx + bodyR * 0.9} ${cy - bodyR * 0.15}
    L${cx + bodyR * 0.9} ${cy}
    C${cx + bodyR * 0.6} ${cy - bodyR * 0.2} ${cx + bodyR * 0.25} ${cy - bodyR * 0.3} ${cx} ${cy - bodyR * 0.3}
    C${cx - bodyR * 0.25} ${cy - bodyR * 0.3} ${cx - bodyR * 0.6} ${cy - bodyR * 0.2} ${cx - bodyR * 0.9} ${cy}
    L${cx - bodyR * 0.9} ${cy - bodyR * 0.15}
    C${cx - bodyR * 0.85} ${cy - bodyR * 0.6} ${cx - bodyR * 0.6} ${cy - bodyR} ${cx} ${cy - bodyR}
    Z" fill="#3a3f5a"/>
  <ellipse cx="${cx}" cy="${cy * 0.95}" rx="${bodyR * 0.42}" ry="${bodyR * 0.40}" fill="#d4a54a"/>
  <ellipse cx="${cx + bodyR * 0.08}" cy="${cy * 0.97}" rx="${bodyR * 0.18}" ry="${bodyR * 0.24}" fill="#1a1d2e"/>
  <circle cx="${cx - bodyR * 0.12}" cy="${cy * 0.90}" r="${bodyR * 0.09}" fill="#f0d080"/>
</svg>`;
}

/** Tray icon: monochrome ghost on transparent bg */
function traySvg(eyeOpen, size) {
  const s = size;
  const cx = s / 2, cy = s * 0.46;
  const r = s * 0.38;
  const sw = Math.max(1, s * 0.07);
  const body = `<path d="
    M${cx} ${cy - r}
    C${cx - r * 0.85} ${cy - r} ${cx - r} ${cy - r * 0.4} ${cx - r} ${cy + r * 0.15}
    L${cx - r} ${cy + r * 0.9}
    L${cx - r * 0.65} ${cy + r * 0.65}
    L${cx - r * 0.33} ${cy + r * 0.9}
    L${cx} ${cy + r * 0.65}
    L${cx + r * 0.33} ${cy + r * 0.9}
    L${cx + r * 0.65} ${cy + r * 0.65}
    L${cx + r} ${cy + r * 0.9}
    L${cx + r} ${cy + r * 0.15}
    C${cx + r} ${cy - r * 0.4} ${cx + r * 0.85} ${cy - r} ${cx} ${cy - r}
    Z" fill="black"/>`;
  const eye = eyeOpen
    ? `<ellipse cx="${cx}" cy="${cy}" rx="${r * 0.38}" ry="${r * 0.36}" fill="white"/>`
    : `<path d="M${cx - r * 0.35} ${cy + r * 0.05} Q${cx} ${cy + r * 0.22} ${cx + r * 0.35} ${cy + r * 0.05}" stroke="white" stroke-width="${sw}" fill="none" stroke-linecap="round"/>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${s}" height="${s}" viewBox="0 0 ${s} ${s}">${body}${eye}</svg>`;
}

/** Windows tray: colored ghost on transparent bg */
function trayColorSvg(eyeOpen, size) {
  const s = size;
  const cx = s / 2, cy = s * 0.46;
  const r = s * 0.40;
  const bodyColor = "#c8cad0";
  const eyeColor = eyeOpen ? "#d4a54a" : "#888";
  const body = `<path d="
    M${cx} ${cy - r}
    C${cx - r * 0.85} ${cy - r} ${cx - r} ${cy - r * 0.4} ${cx - r} ${cy + r * 0.15}
    L${cx - r} ${cy + r * 0.9}
    L${cx - r * 0.65} ${cy + r * 0.65}
    L${cx - r * 0.33} ${cy + r * 0.9}
    L${cx} ${cy + r * 0.65}
    L${cx + r * 0.33} ${cy + r * 0.9}
    L${cx + r * 0.65} ${cy + r * 0.65}
    L${cx + r} ${cy + r * 0.9}
    L${cx + r} ${cy + r * 0.15}
    C${cx + r} ${cy - r * 0.4} ${cx + r * 0.85} ${cy - r} ${cx} ${cy - r}
    Z" fill="${bodyColor}"/>`;
  const sw = Math.max(1.5, s * 0.08);
  const eye = eyeOpen
    ? `<ellipse cx="${cx}" cy="${cy}" rx="${r * 0.36}" ry="${r * 0.34}" fill="${eyeColor}"/><ellipse cx="${cx + r*0.06}" cy="${cy + r*0.04}" rx="${r * 0.14}" ry="${r * 0.20}" fill="#222"/>`
    : `<path d="M${cx - r * 0.32} ${cy + r * 0.05} Q${cx} ${cy + r * 0.2} ${cx + r * 0.32} ${cy + r * 0.05}" stroke="${eyeColor}" stroke-width="${sw}" fill="none" stroke-linecap="round"/>`;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${s}" height="${s}" viewBox="0 0 ${s} ${s}">${body}${eye}</svg>`;
}

// ── Generate PNGs ───────────────────────────────────────────

async function writePng(svgStr, outPath, width, height) {
  const buf = Buffer.from(svgStr);
  await sharp(buf).resize(width, height).png().toFile(outPath);
  console.log(`  ✓ ${outPath.replace(BUILD + "/", "")}  (${width}x${height})`);
}

async function main() {
  console.log("Generating Cyclops Ghost icons...\n");

  // App icons (full color, dark bg)
  await writePng(appIconSvg(512), join(BUILD, "icon.png"), 512, 512);
  await writePng(appIconSvg(256), join(BUILD, "icon256.png"), 256, 256);

  // macOS tray templates (black on transparent)
  await writePng(traySvg(false, 32), join(BUILD, "trayTemplate.png"), 16, 16);
  await writePng(traySvg(false, 32), join(BUILD, "trayTemplate@2x.png"), 32, 32);
  await writePng(traySvg(true, 32), join(BUILD, "trayConnectedTemplate.png"), 16, 16);
  await writePng(traySvg(true, 32), join(BUILD, "trayConnectedTemplate@2x.png"), 32, 32);

  // Windows tray (colored)
  await writePng(trayColorSvg(false, 32), join(BUILD, "tray.png"), 16, 16);
  await writePng(trayColorSvg(true, 32), join(BUILD, "trayConnected.png"), 16, 16);

  // Also save SVG sources for reference
  writeFileSync(join(BUILD, "ghost-icon.svg"), appIconSvg(512));
  writeFileSync(join(BUILD, "ghost-tray-open.svg"), traySvg(true, 64));
  writeFileSync(join(BUILD, "ghost-tray-closed.svg"), traySvg(false, 64));
  console.log("\n  + SVG sources saved to build/\n");

  console.log("Done!");
}

main().catch(e => { console.error(e); process.exit(1); });
