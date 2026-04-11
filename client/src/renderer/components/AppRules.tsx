import { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useSites } from "../sites/useSites";
import type { LocalSite } from "../sites/types";

declare global {
  interface Window {
    tunProxy?: {
      start: (server: string, key: string) => void;
      stop: () => void;
      getStatus: () => Promise<{ status: string }>;
      getRules: () => Promise<{ mode: string; apps: string[]; no_tls_apps?: string[] }>;
      setRules: (rules: { mode: string; apps: string[]; no_tls_apps?: string[] }) => void;
      getInstalledApps: () => Promise<{ name: string; path: string }[]>;
    };
  }
}

interface KnownApp {
  id: string;
  name: string;
  color: string;
  letter: string;
  keywords: string[];
}

const KNOWN_APPS: KnownApp[] = [
  { id: "telegram", name: "Telegram", color: "#27A7E7", letter: "T", keywords: ["telegram"] },
  { id: "discord", name: "Discord", color: "#5865F2", letter: "D", keywords: ["discord"] },
  { id: "claude", name: "Claude Code", color: "#D97757", letter: "C", keywords: ["claude"] },
  { id: "cursor", name: "Cursor", color: "#00D1FF", letter: "Cu", keywords: ["cursor"] },
  { id: "slack", name: "Slack", color: "#E01E5A", letter: "S", keywords: ["slack"] },
];

// Brand SVG icon paths (Simple Icons, viewBox 0 0 24 24)
const ICON_PATHS: Record<string, string> = {
  telegram: "M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z",
  discord: "M20.317 4.3698a19.7913 19.7913 0 00-4.8851-1.5152.0741.0741 0 00-.0785.0371c-.211.3753-.4447.8648-.6083 1.2495-1.8447-.2762-3.68-.2762-5.4868 0-.1636-.3933-.4058-.8742-.6177-1.2495a.077.077 0 00-.0785-.037 19.7363 19.7363 0 00-4.8852 1.515.0699.0699 0 00-.0321.0277C.5334 9.0458-.319 13.5799.0992 18.0578a.0824.0824 0 00.0312.0561c2.0528 1.5076 4.0413 2.4228 5.9929 3.0294a.0777.0777 0 00.0842-.0276c.4616-.6304.8731-1.2952 1.226-1.9942a.076.076 0 00-.0416-.1057c-.6528-.2476-1.2743-.5495-1.8722-.8923a.077.077 0 01-.0076-.1277c.1258-.0943.2517-.1923.3718-.2914a.0743.0743 0 01.0776-.0105c3.9278 1.7933 8.18 1.7933 12.0614 0a.0739.0739 0 01.0785.0095c.1202.099.246.1981.3728.2924a.077.077 0 01-.0066.1276 12.2986 12.2986 0 01-1.873.8914.0766.0766 0 00-.0407.1067c.3604.698.7719 1.3628 1.225 1.9932a.076.076 0 00.0842.0286c1.961-.6067 3.9495-1.5219 6.0023-3.0294a.077.077 0 00.0313-.0552c.5004-5.177-.8382-9.6739-3.5485-13.6604a.061.061 0 00-.0312-.0286zM8.02 15.3312c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9555-2.4189 2.157-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.9555 2.4189-2.1569 2.4189zm7.9748 0c-1.1825 0-2.1569-1.0857-2.1569-2.419 0-1.3332.9554-2.4189 2.1569-2.4189 1.2108 0 2.1757 1.0952 2.1568 2.419 0 1.3332-.946 2.4189-2.1568 2.4189Z",
  claude: "M17.3041 3.541h-3.6718l6.696 16.918H24Zm-10.6082 0L0 20.459h3.7442l1.3693-3.5527h7.0052l1.3693 3.5528h3.7442L10.5363 3.5409Zm-.3712 10.2232 2.2914-5.9456 2.2914 5.9456Z",
  cursor: "M11.503.131 1.891 5.678a.84.84 0 0 0-.42.726v11.188c0 .3.162.575.42.724l9.609 5.55a1 1 0 0 0 .998 0l9.61-5.55a.84.84 0 0 0 .42-.724V6.404a.84.84 0 0 0-.42-.726L12.497.131a1.01 1.01 0 0 0-.996 0M2.657 6.338h18.55c.263 0 .43.287.297.515L12.23 22.918c-.062.107-.229.064-.229-.06V12.335a.59.59 0 0 0-.295-.51l-9.11-5.257c-.109-.063-.064-.23.061-.23",
  slack: "M5.042 15.165a2.528 2.528 0 0 1-2.52 2.523A2.528 2.528 0 0 1 0 15.165a2.527 2.527 0 0 1 2.522-2.52h2.52v2.52zM6.313 15.165a2.527 2.527 0 0 1 2.521-2.52 2.527 2.527 0 0 1 2.521 2.52v6.313A2.528 2.528 0 0 1 8.834 24a2.528 2.528 0 0 1-2.521-2.522v-6.313zM8.834 5.042a2.528 2.528 0 0 1-2.521-2.52A2.528 2.528 0 0 1 8.834 0a2.528 2.528 0 0 1 2.521 2.522v2.52H8.834zM8.834 6.313a2.528 2.528 0 0 1 2.521 2.521 2.528 2.528 0 0 1-2.521 2.521H2.522A2.528 2.528 0 0 1 0 8.834a2.528 2.528 0 0 1 2.522-2.521h6.312zM18.956 8.834a2.528 2.528 0 0 1 2.522-2.521A2.528 2.528 0 0 1 24 8.834a2.528 2.528 0 0 1-2.522 2.521h-2.522V8.834zM17.688 8.834a2.528 2.528 0 0 1-2.523 2.521 2.527 2.527 0 0 1-2.52-2.521V2.522A2.527 2.527 0 0 1 15.165 0a2.528 2.528 0 0 1 2.523 2.522v6.312zM15.165 18.956a2.528 2.528 0 0 1 2.523 2.522A2.528 2.528 0 0 1 15.165 24a2.527 2.527 0 0 1-2.52-2.522v-2.522h2.52zM15.165 17.688a2.527 2.527 0 0 1-2.52-2.523 2.526 2.526 0 0 1 2.52-2.52h6.313A2.527 2.527 0 0 1 24 15.165a2.528 2.528 0 0 1-2.522 2.523h-6.313z",
  youtube: "M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z",
  instagram: "M7.0301.084c-1.2768.0602-2.1487.264-2.911.5634-.7888.3075-1.4575.72-2.1228 1.3877-.6652.6677-1.075 1.3368-1.3802 2.127-.2954.7638-.4956 1.6365-.552 2.914-.0564 1.2775-.0689 1.6882-.0626 4.947.0062 3.2586.0206 3.6671.0825 4.9473.061 1.2765.264 2.1482.5635 2.9107.308.7889.72 1.4573 1.388 2.1228.6679.6655 1.3365 1.0743 2.1285 1.38.7632.295 1.6361.4961 2.9134.552 1.2773.056 1.6884.069 4.9462.0627 3.2578-.0062 3.668-.0207 4.9478-.0814 1.28-.0607 2.147-.2652 2.9098-.5633.7889-.3086 1.4578-.72 2.1228-1.3881.665-.6682 1.0745-1.3378 1.3795-2.1284.2957-.7632.4966-1.636.552-2.9124.056-1.2809.0692-1.6898.063-4.948-.0063-3.2583-.021-3.6668-.0817-4.9465-.0607-1.2797-.264-2.1487-.5633-2.9117-.3084-.7889-.72-1.4568-1.3876-2.1228C21.2982 1.33 20.628.9208 19.8378.6165 19.074.321 18.2017.1197 16.9244.0645 15.6471.0093 15.236-.005 11.977.0014 8.718.0076 8.31.0215 7.0301.0839m.1402 21.6932c-1.17-.0509-1.8053-.2453-2.2287-.408-.5606-.216-.96-.4771-1.3819-.895-.422-.4178-.6811-.8186-.9-1.378-.1644-.4234-.3624-1.058-.4171-2.228-.0595-1.2645-.072-1.6442-.079-4.848-.007-3.2037.0053-3.583.0607-4.848.05-1.169.2456-1.805.408-2.2282.216-.5613.4762-.96.895-1.3816.4188-.4217.8184-.6814 1.3783-.9003.423-.1651 1.0575-.3614 2.227-.4171 1.2655-.06 1.6447-.072 4.848-.079 3.2033-.007 3.5835.005 4.8495.0608 1.169.0508 1.8053.2445 2.228.408.5608.216.96.4754 1.3816.895.4217.4194.6816.8176.9005 1.3787.1653.4217.3617 1.056.4169 2.2263.0602 1.2655.0739 1.645.0796 4.848.0058 3.203-.0055 3.5834-.061 4.848-.051 1.17-.245 1.8055-.408 2.2294-.216.5604-.4763.96-.8954 1.3814-.419.4215-.8181.6811-1.3783.9-.4224.1649-1.0577.3617-2.2262.4174-1.2656.0595-1.6448.072-4.8493.079-3.2045.007-3.5825-.006-4.848-.0608M16.953 5.5864A1.44 1.44 0 1 0 18.39 4.144a1.44 1.44 0 0 0-1.437 1.4424M5.8385 12.012c.0067 3.4032 2.7706 6.1557 6.173 6.1493 3.4026-.0065 6.157-2.7701 6.1506-6.1733-.0065-3.4032-2.771-6.1565-6.174-6.1498-3.403.0067-6.156 2.771-6.1496 6.1738M8 12.0077a4 4 0 1 1 4.008 3.9921A3.9996 3.9996 0 0 1 8 12.0077",
  x: "M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z",
  facebook: "M9.101 23.691v-7.98H6.627v-3.667h2.474v-1.58c0-4.085 1.848-5.978 5.858-5.978.401 0 .955.042 1.468.103a8.68 8.68 0 0 1 1.141.195v3.325a8.623 8.623 0 0 0-.653-.036 26.805 26.805 0 0 0-.733-.009c-.707 0-1.259.096-1.675.309a1.686 1.686 0 0 0-.679.622c-.258.42-.374.995-.374 1.752v1.297h3.919l-.386 2.103-.287 1.564h-3.246v8.245C19.396 23.238 24 18.179 24 12.044c0-6.627-5.373-12-12-12s-12 5.373-12 12c0 5.628 3.874 10.35 9.101 11.647Z",
  linkedin: "M20.447 20.452h-3.554v-5.569c0-1.328-.027-3.037-1.852-3.037-1.853 0-2.136 1.445-2.136 2.939v5.667H9.351V9h3.414v1.561h.046c.477-.9 1.637-1.85 3.37-1.85 3.601 0 4.267 2.37 4.267 5.455v6.286zM5.337 7.433c-1.144 0-2.063-.926-2.063-2.065 0-1.138.92-2.063 2.063-2.063 1.14 0 2.064.925 2.064 2.063 0 1.139-.925 2.065-2.064 2.065zm1.782 13.019H3.555V9h3.564v11.452zM22.225 0H1.771C.792 0 0 .774 0 1.729v20.542C0 23.227.792 24 1.771 24h20.451C23.2 24 24 23.227 24 22.271V1.729C24 .774 23.2 0 22.222 0h.003z",
  medium: "M13.537 12c0 3.7-2.986 6.7-6.669 6.7C3.185 18.7.2 15.7.2 12s2.985-6.7 6.668-6.7c3.683 0 6.669 3 6.669 6.7zm7.326 0c0 3.482-1.493 6.307-3.334 6.307-1.842 0-3.335-2.825-3.335-6.307s1.493-6.307 3.335-6.307c1.841 0 3.334 2.825 3.334 6.307zM24 12c0 3.12-.527 5.65-1.175 5.65-.65 0-1.176-2.53-1.176-5.65s.527-5.65 1.176-5.65C23.473 6.35 24 8.88 24 12z",
  youtrack: "M1.306 51.245a.25.25 0 0 1-.076-.31l7.636-15.726L.058 24.691a.25.25 0 0 1 .032-.352L25.78 2.932a12.665 12.665 0 0 1 15.884-.26 12.59 12.59 0 0 1 3.597 15.436l-2.8 5.767q1.638-.55 3.241-1l12.674-3.64a.25.25 0 0 1 .313.186l5.306 23.585a.253.253 0 0 1-.215.307c-1.682.212-10.858 1.53-22.281 6.33-12.944 5.435-21.485 13.162-22.695 14.292a.246.246 0 0 1-.319.014z",
};

const ICON_VIEWBOXES: Record<string, string> = {
  youtrack: "0 0 64 64",
};

const SITE_ICON_MAP: Record<string, string> = {
  "youtube.com": "youtube",
  "instagram.com": "instagram",
  "twitter.com": "x",
  "facebook.com": "facebook",
  "discord.com": "discord",
  "linkedin.com": "linkedin",
  "medium.com": "medium",
  "claude.ai": "claude",
  "youtrack.cloud": "youtrack",
  "web.telegram.org": "telegram",
};

const SITE_COLORS: Record<string, string> = {
  "youtube.com": "#FF0000",
  "instagram.com": "#E4405F",
  "twitter.com": "#ccc",
  "facebook.com": "#0866FF",
  "discord.com": "#5865F2",
  "linkedin.com": "#0A66C2",
  "medium.com": "#ccc",
  "claude.ai": "#D97757",
  "youtrack.cloud": "#FB43FF",
  "web.telegram.org": "#27A7E7",
};

function BrandIcon({ iconKey, size = 16, color = "#fff" }: { iconKey: string; size?: number; color?: string }) {
  const path = ICON_PATHS[iconKey];
  if (!path) return null;
  const viewBox = ICON_VIEWBOXES[iconKey] || "0 0 24 24";
  return (
    <svg viewBox={viewBox} width={size} height={size} fill={color} style={{ display: "block" }}>
      <path d={path} />
    </svg>
  );
}

// Label derived from a domain: "reddit.com" -> "Reddit", "x.com" -> "X",
// "www.example.com" -> "Example".
function labelFromDomain(domain: string): string {
  const stripped = domain.replace(/^www\./, "");
  const first = stripped.split(".")[0];
  if (!first) return domain;
  return first.charAt(0).toUpperCase() + first.slice(1);
}

// Stable HSL hue from a domain so each fallback avatar gets its own color.
function hashHue(str: string): number {
  let h = 0;
  for (let i = 0; i < str.length; i++) h = (h * 31 + str.charCodeAt(i)) | 0;
  return Math.abs(h) % 360;
}

// HSL → hex so colors work with the "${color}14" hex-alpha pattern.
function hslToHex(h: number, s: number, l: number): string {
  s /= 100;
  l /= 100;
  const a = s * Math.min(l, 1 - l);
  const f = (n: number) => {
    const k = (n + h / 30) % 12;
    const c = l - a * Math.max(Math.min(k - 3, 9 - k, 1), -1);
    return Math.round(255 * c).toString(16).padStart(2, "0");
  };
  return `#${f(0)}${f(8)}${f(4)}`;
}

// Stable color for a site: brand color if known, otherwise hex from domain hash.
function siteColor(domain: string): string {
  return SITE_COLORS[domain] || hslToHex(hashHue(domain), 60, 55);
}

// Letter-avatar fallback used when the favicon can't be fetched.
function LetterAvatar({ name, domain, size = 40 }: { name: string; domain: string; size?: number }) {
  const hue = hashHue(domain);
  const letter = (name.charAt(0) || "?").toUpperCase();
  return (
    <div
      style={{
        width: size,
        height: size,
        borderRadius: 8,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        color: "#fff",
        fontWeight: 700,
        fontSize: size * 0.5,
        textTransform: "uppercase",
        letterSpacing: "-0.5px",
        background: `linear-gradient(135deg, hsl(${hue}, 65%, 50%), hsl(${(hue + 25) % 360}, 65%, 38%))`,
        flexShrink: 0,
      }}
    >
      {letter}
    </div>
  );
}

// SiteTileIcon renders the best available icon for a site:
//  1. Built-in site with a Simple Icons entry -> BrandIcon (vector brand mark)
//  2. Otherwise -> Google S2 favicons API, with LetterAvatar fallback when
//     the image is missing, errors, or is Google's 16x16 generic globe.
function SiteTileIcon({
  domain,
  name,
  color,
  size = 40,
  monochrome,
}: {
  domain: string;
  name: string;
  color: string;
  size?: number;
  monochrome?: boolean;
}) {
  const iconKey = SITE_ICON_MAP[domain];
  const [failed, setFailed] = useState(false);

  // Reset the "failed" flag when the domain changes so re-rendering with a
  // new site attempts the favicon fetch fresh.
  useEffect(() => {
    setFailed(false);
  }, [domain]);

  if (iconKey) {
    return <BrandIcon iconKey={iconKey} size={size} color={monochrome ? "#555" : color} />;
  }

  if (failed) {
    return <LetterAvatar name={name} domain={domain} size={size} />;
  }

  const src = `https://www.google.com/s2/favicons?domain=${encodeURIComponent(domain)}&sz=128`;
  return (
    <img
      src={src}
      alt=""
      width={size}
      height={size}
      style={{
        width: size,
        height: size,
        objectFit: "contain",
        borderRadius: 6,
        filter: monochrome ? "grayscale(1) opacity(0.5)" : undefined,
      }}
      onError={() => setFailed(true)}
      onLoad={(e) => {
        // Google returns a 16x16 generic globe for unknown domains. Treat
        // any image at or below that size as "no real favicon".
        if ((e.currentTarget as HTMLImageElement).naturalWidth <= 16) {
          setFailed(true);
        }
      }}
    />
  );
}

const STORAGE_KEY_NO_TLS = "smurov-proxy-no-tls";

function loadNoTLS(): Set<string> {
  const saved = localStorage.getItem(STORAGE_KEY_NO_TLS);
  if (saved) {
    try { return new Set(JSON.parse(saved)); } catch {}
  }
  return new Set();
}

function saveNoTLS(noTLS: Set<string>) {
  localStorage.setItem(STORAGE_KEY_NO_TLS, JSON.stringify([...noTLS]));
}

type Mode = "all" | "selected";

interface ResolvedApp {
  app: KnownApp;
  paths: string[];
}

interface Props {
  visible: boolean;
  mode?: Mode;
  onModeChange?: (m: Mode) => void;
  hideModeSwitch?: boolean;
}

export function AppRules({ visible, mode: modeProp, onModeChange, hideModeSwitch }: Props) {
  const [modeState, setModeState] = useState<Mode>("all");
  const mode = modeProp ?? modeState;
  const setMode = (m: Mode) => {
    setModeState(m);
    onModeChange?.(m);
  };
  const [resolved, setResolved] = useState<ResolvedApp[]>([]);
  const [enabled, setEnabled] = useState<Set<string>>(new Set(KNOWN_APPS.map((a) => a.id)));
  // Initial-sync guard. We must NOT push rules on mount until both the
  // installed-apps list and the daemon's saved rules have loaded — otherwise
  // applyRules("selected") runs with resolved=[] and pushes {proxy_only, []},
  // which drops every app's traffic to direct (proxy=false). Bug shipped in
  // 1.31.0: AppRules used to live in every view, so visible-but-unloaded was
  // harmless; in the Panorama redesign it mounts only for the Selected tab,
  // making the empty-push window user-visible.
  const [ready, setReady] = useState(false);

  // Browser sites — backed by the sites sync module.
  const { sites: localSites, ready: sitesReady, addSite, removeSite: removeSiteById, toggleSite: toggleSiteById } = useSites();
  // All-sites toggle: a local-only mode flag that bypasses per-site picks.
  const [allSitesOn, setAllSitesOn] = useState<boolean>(
    () => localStorage.getItem("smurov-proxy-all-sites-on") !== "false"
  );
  const [noTLS, setNoTLS] = useState<Set<string>>(loadNoTLS);
  const [browsersOn] = useState(() => localStorage.getItem("smurov-proxy-browsers-on") !== "false");
  const [addSiteModalOpen, setAddSiteModalOpen] = useState(false);
  // Set of hosts with active SOCKS5 connections, refreshed from the daemon.
  // Each element is a raw hostname as seen by the tunnel (e.g. "m.youtube.com").
  const [activeHosts, setActiveHosts] = useState<string[]>([]);

  // Poll /tunnel/active-hosts every 2s while the picker is visible so the
  // LIVE indicator on browser tiles reflects real traffic.
  useEffect(() => {
    if (!visible) return;
    let cancelled = false;
    const poll = async () => {
      try {
        const res = await fetch("http://127.0.0.1:9090/tunnel/active-hosts");
        if (!res.ok) return;
        const data: { hosts?: string[] } = await res.json();
        if (!cancelled) setActiveHosts(data.hosts || []);
      } catch {
        // Silently ignore — daemon might be restarting.
      }
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [visible]);

  // enabledSet and liveSites MUST be memoized: they are passed as props to
  // SitesGrid — without useMemo they'd be fresh references every render,
  // breaking memo/reconciliation.

  // Map from active hosts to the set of site ids that are currently live.
  // A site is live when any of its domains matches (by suffix) at least one active host.
  const liveSites = useMemo(() => {
    if (activeHosts.length === 0) return new Set<number>();
    const live = new Set<number>();
    const hostMatches = (host: string, pattern: string): boolean =>
      host === pattern || host.endsWith("." + pattern);
    for (const s of localSites) {
      for (const host of activeHosts) {
        if (s.domains.some((p) => hostMatches(host, p))) {
          live.add(s.id);
          break;
        }
      }
    }
    return live;
  }, [activeHosts, localSites]);

  // Derive enabled set (by id) for SitesGrid.
  const enabledSet = useMemo(
    () => new Set(localSites.filter((s) => s.enabled).map((s) => s.id)),
    [localSites]
  );

  useEffect(() => {
    if (!visible) return;
    setReady(false);

    // Load installed apps AND daemon rules together, then flip `ready`.
    // The rules-push effect below waits for this flag to avoid firing with
    // an empty `resolved` (which produces {proxy_only, []} = direct-for-all).
    const fallbackRules: { mode: string; apps: string[]; no_tls_apps?: string[] } = {
      mode: "proxy_all_except",
      apps: [],
    };
    Promise.all([
      window.tunProxy?.getInstalledApps?.() ?? Promise.resolve([]),
      window.tunProxy?.getRules?.() ?? Promise.resolve(fallbackRules),
    ]).then(([installed, rules]) => {
      const results: ResolvedApp[] = [];
      for (const app of KNOWN_APPS) {
        const paths: string[] = [];
        for (const inst of installed || []) {
          const lower = (inst.name + " " + inst.path).toLowerCase();
          if (app.keywords.some((kw) => lower.includes(kw))) {
            paths.push(inst.path.toLowerCase());
          }
        }
        if (paths.length > 0) {
          results.push({ app, paths });
        }
      }
      setResolved(results);

      // Only sync mode from daemon when the mode isn't externally controlled.
      // Otherwise getRules → setMode → onModeChange → parent setTrafficMode
      // would flip the tab back and race with our rules push.
      if (rules && modeProp === undefined) {
        if (rules.mode === "proxy_all_except") setMode("all");
        else if (rules.mode === "proxy_only") setMode("selected");
      }

      // Restore the enabled set from whatever the daemon already has — this
      // is how a user's previous selection survives a restart.
      if (rules?.apps && rules.apps.length > 0) {
        const savedPaths = new Set(rules.apps.map((a: string) => a.toLowerCase()));
        const enabledIds = new Set<string>();
        for (const app of KNOWN_APPS) {
          for (const sp of savedPaths) {
            if (app.keywords.some((kw) => sp.includes(kw))) {
              enabledIds.add(app.id);
              break;
            }
          }
        }
        setEnabled(enabledIds);
      }

      if (rules?.no_tls_apps && rules.no_tls_apps.length > 0) {
        const noTLSPaths = new Set(rules.no_tls_apps.map((a: string) => a.toLowerCase()));
        const noTLSIds = new Set<string>();
        for (const app of KNOWN_APPS) {
          for (const sp of noTLSPaths) {
            if (app.keywords.some((kw) => sp.includes(kw))) {
              noTLSIds.add(app.id);
              break;
            }
          }
        }
        setNoTLS(noTLSIds);
        saveNoTLS(noTLSIds);
      }

      setReady(true);
    });
  }, [visible]);

  const applyPac = useCallback(
    (on: boolean) => {
      if (!on) {
        window.sysproxy?.disable();
        return;
      }
      window.sysproxy?.setPacSites({ proxy_all: allSitesOn });
      window.sysproxy?.enable();
    },
    [allSitesOn]
  );

  // Re-apply PAC whenever browsersOn or site selection changes.
  useEffect(() => {
    applyPac(browsersOn);
  }, [applyPac, browsersOn]);

  const applyRulesTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const applyRules = useCallback((m: Mode, enabledIds: Set<string>, resolvedApps: ResolvedApp[], bOn: boolean, noTLSIds: Set<string>) => {
    if (applyRulesTimer.current) clearTimeout(applyRulesTimer.current);
    applyRulesTimer.current = setTimeout(() => {
      if (m === "all") {
        window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
        window.sysproxy?.setPacSites({ proxy_all: true });
        window.sysproxy?.enable();
      } else {
        const paths: string[] = [];
        const noTLSPaths: string[] = [];
        for (const r of resolvedApps) {
          if (enabledIds.has(r.app.id)) {
            paths.push(...r.paths);
            if (noTLSIds.has(r.app.id)) {
              noTLSPaths.push(...r.paths);
            }
          }
        }
        window.tunProxy?.setRules({ mode: "proxy_only", apps: paths, no_tls_apps: noTLSPaths });
        applyPac(bOn);
      }
    }, 100);
  }, [applyPac]);

  const handleModeChange = (m: Mode) => {
    setMode(m);
    applyRules(m, enabled, resolved, browsersOn, noTLS);
  };

  // React to externally-controlled mode changes (parent switcher).
  // Waits on `ready` so we don't push {proxy_only, []} before resolved/rules
  // have loaded. See the initial-sync guard comment where `ready` is declared.
  useEffect(() => {
    if (modeProp === undefined) return;
    if (!ready) return;
    applyRules(modeProp, enabled, resolved, browsersOn, noTLS);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modeProp, ready]);

  const toggleApp = (appId: string) => {
    setEnabled((prev) => {
      const next = new Set(prev);
      if (next.has(appId)) next.delete(appId);
      else next.add(appId);
      applyRules(mode, next, resolved, browsersOn, noTLS);
      return next;
    });
  };

  const toggleNoTLS = (appId: string) => {
    setNoTLS((prev) => {
      const next = new Set(prev);
      if (next.has(appId)) next.delete(appId);
      else next.add(appId);
      saveNoTLS(next);
      applyRules(mode, enabled, resolved, browsersOn, next);
      return next;
    });
  };

  const handleToggleTile = async (site: LocalSite) => {
    if (allSitesOn) {
      // Clicking a tile while "all" is on: switch to per-site mode.
      setAllSitesOn(false);
      localStorage.setItem("smurov-proxy-all-sites-on", "false");
    }
    try {
      await toggleSiteById(site.id, !site.enabled);
    } catch (e) {
      console.error("[AppRules] toggle failed:", e);
    }
  };

  const handleToggleAll = () => {
    const next = !allSitesOn;
    setAllSitesOn(next);
    localStorage.setItem("smurov-proxy-all-sites-on", String(next));
  };

  // addSiteByDomain normalizes the input and adds the site to the list if
  // it's new. Called by the AddSiteModal.
  const addSiteByDomain = async (raw: string) => {
    let domain = raw.trim().toLowerCase();
    if (!domain) return;
    domain = domain.replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/^www\./, "");
    if (!domain) return;
    // If a site with this primary domain already exists, just enable it.
    const existing = localSites.find((s) => s.domains[0] === domain);
    if (existing) {
      if (!existing.enabled) {
        try {
          await toggleSiteById(existing.id, true);
        } catch (e) {
          console.error("[AppRules] toggle failed:", e);
        }
      }
      return;
    }
    const label = labelFromDomain(domain);
    try {
      await addSite(domain, label);
    } catch (e) {
      console.error("[AppRules] add failed:", e);
    }
  };

  const handleRemoveSite = async (siteId: number) => {
    try {
      await removeSiteById(siteId);
    } catch (e) {
      console.error("[AppRules] remove failed:", e);
    }
  };

  // Auto-add popular/seed sites that aren't in the user's list yet.
  // Runs once on mount. Sites are added disabled so they show in the grid
  // but don't affect routing until the user toggles them on.
  const seedLoaded = useRef(false);
  useEffect(() => {
    if (seedLoaded.current || !sitesReady || localSites === undefined) return;
    seedLoaded.current = true;
    (async () => {
      try {
        const catalog = await (window as any).appInfo?.daemonSearchSites("");
        if (!Array.isArray(catalog) || catalog.length === 0) return;
        const existingDomains = new Set(localSites.map((s: LocalSite) => s.domains[0]));
        for (const entry of catalog) {
          if (!existingDomains.has(entry.primary_domain)) {
            try {
              await addSite(entry.primary_domain, entry.label);
            } catch {}
          }
        }
      } catch {}
    })();
  }, [sitesReady, localSites, addSite]);

  if (!visible) return null;

  return (
    <div style={{ marginTop: 16, padding: 12 }}>
      {!hideModeSwitch && (
      <div style={{ fontSize: 13, fontWeight: 600, fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif", letterSpacing: 0.3, marginBottom: 8 }}>Traffic</div>
      )}

      {!hideModeSwitch && (
      <div style={{ display: "flex", gap: 6, marginBottom: 12 }}>
        {([["all", "All traffic"], ["selected", "Selected apps"]] as const).map(([key, label]) => (
          <button
            key={key}
            onClick={() => handleModeChange(key)}
            style={{
              flex: 1, padding: "6px 0", fontSize: 11,
              background: mode === key ? "#1a3a5c" : "transparent",
              border: `1px solid ${mode === key ? "#3b82f6" : "#333"}`,
              borderRadius: 6, color: mode === key ? "#fff" : "#888",
              cursor: "pointer",
            }}
          >
            {label}
          </button>
        ))}
      </div>
      )}

      {mode === "all" ? (
        <div style={{ color: "#666", fontSize: 12, textAlign: "center", padding: "4px 0" }}>
          All traffic goes through proxy
        </div>
      ) : (
        <div style={{ display: "grid", gridTemplateColumns: "240px 1fr", gap: 20, alignItems: "start" }}>
          {/* LEFT COLUMN — Applications */}
          <div style={{ animation: "smurov-blur-row 0.4s cubic-bezier(0.25,1,0.5,1) 0.05s both" }}>
            <div style={{
              display: "flex", alignItems: "center", gap: 8, marginBottom: 10, minHeight: 24,
            }}>
              <div style={{
                fontSize: 11, color: "#888", textTransform: "uppercase" as const,
                letterSpacing: 1, fontWeight: 600,
                fontFamily: "'Barlow', system-ui, sans-serif",
              }}>
                Applications
              </div>
              <span style={{ fontSize: 10, color: "#555", fontFamily: "'Figtree', system-ui, sans-serif" }}>
                {resolved.filter(({ app }) => enabled.has(app.id)).length} of {resolved.length}
              </span>
            </div>
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              {resolved.map(({ app }, i) => (
                <div key={app.id} style={{ animation: `smurov-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) ${0.1 + i * 0.04}s both` }}>
                  <AppToggle app={app} isOn={enabled.has(app.id)} noTLS={noTLS.has(app.id)} onToggle={toggleApp} onToggleTLS={toggleNoTLS} />
                </div>
              ))}
            </div>
          </div>

          {/* RIGHT COLUMN — Browser Sites */}
          <div style={{ animation: "smurov-blur-row 0.4s cubic-bezier(0.25,1,0.5,1) 0.1s both" }}>
          <SitesGrid
            sites={localSites}
            enabledSites={enabledSet}
            liveSites={liveSites}
            allSitesOn={allSitesOn}
            onToggleAll={handleToggleAll}
            onToggleSite={handleToggleTile}
            onRemoveSite={handleRemoveSite}
            onAddSite={() => setAddSiteModalOpen(true)}
          />
          </div>
        </div>
      )}

      {addSiteModalOpen && (
        <AddSiteModal
          onClose={() => setAddSiteModalOpen(false)}
          existingSiteIds={new Set(localSites.map((s) => s.id))}
          onAdd={(domains) => {
            for (const d of domains) addSiteByDomain(d);
            setAddSiteModalOpen(false);
          }}
        />
      )}
    </div>
  );
}

function AppToggle({ app, isOn, noTLS, onToggle, onToggleTLS }: {
  app: KnownApp;
  isOn: boolean;
  noTLS: boolean;
  onToggle: (id: string) => void;
  onToggleTLS: (id: string) => void;
}) {
  return (
    <div
      onClick={() => onToggle(app.id)}
      style={{
        display: "flex", alignItems: "center", gap: 8,
        padding: "4px 6px", borderRadius: 4,
        cursor: "pointer",
        transition: "background 0.1s",
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = "oklch(0.19 0.018 250)"; }}
      onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; }}
    >
      <div style={{
        width: 22, height: 22, borderRadius: 5,
        background: isOn ? app.color : "oklch(0.23 0.016 250)",
        display: "flex", alignItems: "center", justifyContent: "center",
        fontSize: 9, fontWeight: 700,
        fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
        color: isOn ? "#fff" : "oklch(0.40 0.01 250)",
        flexShrink: 0,
        opacity: isOn ? 1 : 0.3,
        transition: "all 0.2s cubic-bezier(0.25,1,0.5,1)",
        transform: isOn ? "scale(1)" : "scale(0.85)",
      }}>
        {ICON_PATHS[app.id]
          ? <BrandIcon iconKey={app.id} size={13} color={isOn ? "#fff" : "oklch(0.40 0.01 250)"} />
          : app.letter}
      </div>
      <div style={{
        fontSize: 12, fontWeight: 500,
        fontFamily: "'Figtree', system-ui, sans-serif",
        color: isOn ? "oklch(0.93 0.006 250)" : "oklch(0.40 0.01 250)",
        flex: 1,
        transition: "color 0.2s cubic-bezier(0.25,1,0.5,1)",
      }}>
        {app.name}
      </div>
      {isOn ? (
        <div style={{ display: "flex", alignItems: "center", gap: 0, animation: "smurov-blur-fade 0.25s cubic-bezier(0.25,1,0.5,1) both" }}>
          <span style={{
            fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
            fontSize: 9, fontWeight: 600, letterSpacing: 1,
            textTransform: "uppercase" as const,
            color: "oklch(0.68 0.12 235)",
          }}>
            Proxy
          </span>
          <span style={{
            fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
            fontSize: 9, color: "oklch(0.30 0.014 250)", margin: "0 3px",
          }}>/</span>
          <span
            onClick={(e) => { e.stopPropagation(); onToggleTLS(app.id); }}
            title={noTLS ? "Click to enable TLS" : "Click to disable TLS"}
            style={{
              fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              fontSize: 8, fontWeight: 600, letterSpacing: 0.5,
              color: noTLS ? "oklch(0.78 0.155 75)" : "oklch(0.72 0.15 150)",
              background: noTLS ? "oklch(0.19 0.035 75)" : "oklch(0.16 0.025 150)",
              border: `1px solid ${noTLS ? "oklch(0.78 0.155 75 / 0.2)" : "oklch(0.72 0.15 150 / 0.2)"}`,
              transition: "all 0.2s cubic-bezier(0.25,1,0.5,1)",
              padding: "1px 5px",
              borderRadius: 3,
              cursor: "pointer",
            }}
          >
            {noTLS ? "Raw" : "TLS"}
          </span>
        </div>
      ) : (
        <span style={{
          fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
          fontSize: 9, fontWeight: 600, letterSpacing: 1,
          textTransform: "uppercase" as const,
          color: "oklch(0.40 0.01 250)",
          animation: "smurov-blur-fade 0.25s cubic-bezier(0.25,1,0.5,1) both",
        }}>
          Direct
        </span>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// SitesGrid — grid of tiles for the browser sites picker
// ---------------------------------------------------------------------------

function SitesGrid({
  sites,
  enabledSites,
  liveSites,
  allSitesOn,
  onToggleAll,
  onToggleSite,
  onRemoveSite,
  onAddSite,
}: {
  sites: LocalSite[];
  enabledSites: Set<number>;
  liveSites: Set<number>;
  allSitesOn: boolean;
  onToggleAll: () => void;
  onToggleSite: (site: LocalSite) => void;
  onRemoveSite: (siteId: number) => void;
  onAddSite: () => void;
}) {
  const enabledCount = sites.filter((s) => enabledSites.has(s.id)).length;

  return (
    <div>
      {/* Section header: label + all/selected toggle + Add button */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 8,
          marginBottom: 10,
          minHeight: 24,
        }}
      >
        <div
          style={{
            fontSize: 11,
            color: "#888",
            textTransform: "uppercase",
            letterSpacing: 1,
            fontWeight: 600,
            fontFamily: "'Barlow', system-ui, sans-serif",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          <span>Browser sites</span>
          {liveSites.size > 0 && (
            <span
              style={{
                fontSize: 10,
                color: "#4caf50",
                textTransform: "none",
                letterSpacing: 0,
                display: "flex",
                alignItems: "center",
                gap: 5,
                fontFamily: "'Barlow', system-ui, sans-serif",
              }}
            >
              <span
                style={{
                  width: 6,
                  height: 6,
                  borderRadius: "50%",
                  background: "#4caf50",
                  boxShadow: "0 0 4px rgba(76,175,80,0.8)",
                  animation: "smurov-pulse 1.5s ease-in-out infinite",
                }}
              />
              {liveSites.size} active
            </span>
          )}
        </div>
        {/* All / Selected toggle */}
        <div
          style={{
            display: "inline-flex",
            padding: 2,
            background: "oklch(0.15 0.014 250)",
            borderRadius: 5,
            gap: 1,
          }}
        >
          {(["all", "selected"] as const).map((opt) => {
            const isActive = opt === "all" ? allSitesOn : !allSitesOn;
            return (
              <button
                key={opt}
                onClick={onToggleAll}
                style={{
                  padding: "5px 14px",
                  borderRadius: 5,
                  border: "none",
                  fontSize: 12,
                  fontWeight: isActive ? 600 : 500,
                  fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
                  letterSpacing: 0.3,
                  color: isActive ? "oklch(0.78 0.155 75)" : "oklch(0.40 0.01 250)",
                  background: isActive ? "oklch(0.19 0.035 75)" : "transparent",
                  cursor: "pointer",
                  transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
                }}
              >
                {opt === "all" ? "All" : "Selected"}
              </button>
            );
          })}
        </div>
        <span style={{ fontSize: 10, color: "#555", fontFamily: "'Figtree', system-ui, sans-serif" }}>
          {allSitesOn ? "" : `${enabledCount} of ${sites.length}`}
        </span>
        <div style={{ flex: 1 }} />
        {!allSitesOn && (
          <button
            onClick={onAddSite}
            style={{
              padding: "5px 14px",
              background: "oklch(0.19 0.018 250)",
              border: "1px solid oklch(0.30 0.014 250)",
              borderRadius: 5,
              color: "oklch(0.60 0.012 250)",
              fontSize: 12,
              fontWeight: 600,
              cursor: "pointer",
              fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              letterSpacing: 0.5,
              display: "inline-flex",
              alignItems: "center",
              gap: 5,
              transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
              lineHeight: 1,
            }}
            onMouseEnter={(e) => { e.currentTarget.style.color = "oklch(0.93 0.006 250)"; e.currentTarget.style.borderColor = "oklch(0.40 0.01 250)"; }}
            onMouseLeave={(e) => { e.currentTarget.style.color = "oklch(0.60 0.012 250)"; e.currentTarget.style.borderColor = "oklch(0.30 0.014 250)"; }}
          >
            Add site
            <svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>
          </button>
        )}
      </div>

      {/* Content: all-sites message OR individual site grid */}
      {allSitesOn ? (
        <div style={{
          padding: "20px 16px",
          borderRadius: 8,
          background: "oklch(0.155 0.016 250)",
          border: "1px solid oklch(0.24 0.013 250)",
          display: "flex",
          alignItems: "center",
          gap: 16,
          animation: "smurov-blur-fade 0.35s cubic-bezier(0.25,1,0.5,1) both",
        }}>
          <div style={{ animation: "smurov-blur-dot 0.4s cubic-bezier(0.25,1,0.5,1) 0.1s both" }}>
            <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="oklch(0.78 0.155 75)" strokeWidth="1.5" style={{ flexShrink: 0, opacity: 0.7 }}>
              <circle cx="12" cy="12" r="10" />
              <ellipse cx="12" cy="12" rx="10" ry="4" />
              <line x1="2" y1="12" x2="22" y2="12" />
              <ellipse cx="12" cy="12" rx="4" ry="10" />
            </svg>
          </div>
          <div>
            <div style={{ fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif", fontSize: 14, fontWeight: 600, color: "oklch(0.93 0.006 250)", letterSpacing: 0.3, marginBottom: 3, animation: "smurov-blur-heavy 0.4s cubic-bezier(0.25,1,0.5,1) 0.15s both" }}>
              All browser traffic is proxied
            </div>
            <div style={{ fontFamily: "'Figtree', system-ui, sans-serif", fontSize: 12, color: "oklch(0.50 0.01 250)", lineHeight: 1.5, animation: "smurov-blur-light 0.35s cubic-bezier(0.25,1,0.5,1) 0.25s both" }}>
              Every website you open in any browser goes through the proxy server.
              Switch to Selected to choose specific sites.
            </div>
          </div>
        </div>
      ) : (
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(4, 1fr)",
            gap: 6,
          }}
        >
          {sites.map((site, i) => (
            <div key={site.id} style={{ animation: `smurov-blur-light 0.3s cubic-bezier(0.25,1,0.5,1) ${0.08 + i * 0.03}s both` }}>
              <SiteTile
                site={site}
                enabled={enabledSites.has(site.id)}
                live={liveSites.has(site.id)}
                dimmed={false}
                onClick={() => onToggleSite(site)}
                onRemove={() => onRemoveSite(site.id)}
              />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// All browsers mega-tile (spans 2 columns)
function AllBrowsersTile({
  enabled,
  live,
  onClick,
}: {
  enabled: boolean;
  live: boolean;
  onClick: () => void;
}) {
  return (
    <div
      onClick={onClick}
      style={{
        gridColumn: "span 2",
        position: "relative",
        padding: 10,
        borderRadius: 8,
        cursor: "pointer",
        transition: "transform 0.15s ease, background 0.2s, border-color 0.2s, color 0.2s",
        minHeight: 60,
        background: enabled
          ? "linear-gradient(135deg, rgba(76, 175, 80, 0.18), rgba(76, 175, 80, 0.06))"
          : "#0d111c",
        border: enabled
          ? "1px solid rgba(76, 175, 80, 0.5)"
          : "1px solid #222",
        color: enabled ? "#4caf50" : "#555",
      }}
      onMouseEnter={(e) => {
        e.currentTarget.style.transform = "translateY(-2px)";
      }}
      onMouseLeave={(e) => {
        e.currentTarget.style.transform = "none";
      }}
    >
      {enabled && live && <LiveLabel />}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          width: "100%",
          height: "100%",
        }}
      >
        <div style={{ flexShrink: 0 }}>
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.3">
            <circle cx="12" cy="12" r="10" />
            <ellipse cx="12" cy="12" rx="10" ry="4" />
            <line x1="2" y1="12" x2="22" y2="12" />
            <ellipse cx="12" cy="12" rx="4" ry="10" />
          </svg>
        </div>
        <div style={{ flex: 1 }}>
          <div
            style={{
              fontSize: 13,
              fontWeight: 700,
              fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              letterSpacing: 0.3,
              color: enabled ? "#eee" : "#666",
              marginBottom: 2,
            }}
          >
            All browsers
          </div>
          <div
            style={{
              fontSize: 10,
              fontFamily: "'Figtree', system-ui, sans-serif",
              color: enabled ? "#888" : "#555",
              lineHeight: 1.3,
            }}
          >
            Route every browser site through the proxy
          </div>
        </div>
      </div>
    </div>
  );
}

// Individual site tile
function SiteTile({
  site,
  enabled,
  live,
  dimmed,
  onClick,
  onRemove,
}: {
  site: LocalSite;
  enabled: boolean;
  live: boolean;
  dimmed: boolean;
  onClick: () => void;
  onRemove?: () => void;
}) {
  const primaryDomain = site.domains[0] || "";
  const color = siteColor(primaryDomain);
  const [removing, setRemoving] = useState(false);

  const handleRemove = () => {
    if (!onRemove) return;
    setRemoving(true);
    setTimeout(() => onRemove(), 300);
  };

  return (
    <div
      onClick={removing ? undefined : onClick}
      style={{
        position: "relative",
        padding: "8px 6px 8px",
        borderRadius: 8,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        gap: 4,
        cursor: removing ? "default" : "pointer",
        transition: "all 0.2s cubic-bezier(0.25,1,0.5,1)",
        minHeight: 64,
        background: enabled ? `${color}14` : "oklch(0.13 0.012 250)",
        border: enabled ? `1px solid ${color}40` : "1px solid oklch(0.22 0.012 250)",
        filter: removing ? "blur(8px) grayscale(1)" : dimmed ? "grayscale(0.8)" : undefined,
        opacity: removing ? 0 : dimmed ? 0.35 : 1,
        transform: removing ? "scale(0.8)" : undefined,
        pointerEvents: removing ? "none" : undefined,
      }}
      onMouseEnter={(e) => {
        if (dimmed || removing) return;
        e.currentTarget.style.borderColor = enabled ? `${color}70` : "oklch(0.35 0.014 250)";
      }}
      onMouseLeave={(e) => {
        if (removing) return;
        e.currentTarget.style.borderColor = enabled ? `${color}40` : "oklch(0.22 0.012 250)";
      }}
    >
      {enabled && live && !dimmed && <LiveLabel />}
      {onRemove && !removing && (
        <button
          onClick={(e) => {
            e.stopPropagation();
            handleRemove();
          }}
          title="Remove site"
          style={{
            position: "absolute",
            top: 3,
            left: 3,
            width: 14,
            height: 14,
            borderRadius: 3,
            background: "transparent",
            border: "none",
            color: "#555",
            fontSize: 14,
            cursor: "pointer",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            lineHeight: 1,
            padding: 0,
          }}
          onMouseEnter={(e) => {
            e.currentTarget.style.background = "#2a3040";
            e.currentTarget.style.color = "#f44336";
          }}
          onMouseLeave={(e) => {
            e.currentTarget.style.background = "transparent";
            e.currentTarget.style.color = "#555";
          }}
        >
          ×
        </button>
      )}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          marginBottom: 2,
          transition: "opacity 0.2s cubic-bezier(0.25,1,0.5,1)",
          opacity: enabled ? 1 : 0.4,
        }}
      >
        <SiteTileIcon
          domain={primaryDomain}
          name={site.label}
          color={color}
          size={24}
          monochrome={!enabled}
        />
      </div>
      <div
        style={{
          fontSize: 11,
          fontWeight: 600,
          fontFamily: "'Figtree', system-ui, sans-serif",
          textAlign: "center",
          lineHeight: 1.2,
          color: enabled ? "#eee" : "#666",
          transition: "color 0.25s cubic-bezier(0.25,1,0.5,1)",
        }}
      >
        {site.label}
      </div>
      <div
        style={{
          fontSize: 9,
          fontFamily: "'Barlow', system-ui, sans-serif",
          textAlign: "center",
          opacity: 0.8,
          color: enabled ? "#888" : "#555",
          transition: "color 0.25s cubic-bezier(0.25,1,0.5,1)",
        }}
      >
        {primaryDomain}
      </div>
    </div>
  );
}

// Green LIVE pill with pulsing glow
function LiveLabel() {
  return (
    <div
      style={{
        position: "absolute",
        top: 3,
        right: 3,
        fontSize: 7,
        fontWeight: 700,
        color: "#4caf50",
        background: "rgba(76, 175, 80, 0.12)",
        border: "1px solid rgba(76, 175, 80, 0.4)",
        padding: "1px 4px",
        borderRadius: 3,
        letterSpacing: 0.5,
        fontFamily: "'Barlow', system-ui, sans-serif",
        animation: "smurov-live-glow 1.5s ease-in-out infinite",
      }}
    >
      ● LIVE
    </div>
  );
}

// ---------------------------------------------------------------------------
// AddSiteModal — modal dialog for adding a new custom site with live preview
// ---------------------------------------------------------------------------

interface CatalogResult {
  id: number;
  label: string;
  primary_domain: string;
}

function AddSiteModal({
  onClose,
  onAdd,
  existingSiteIds,
}: {
  onClose: () => void;
  onAdd: (domains: string[]) => void;
  existingSiteIds: Set<number>;
}) {
  const [value, setValue] = useState("");
  const [results, setResults] = useState<CatalogResult[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [searching, setSearching] = useState(false);
  const [closing, setClosing] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const animClose = () => {
    setClosing(true);
    setTimeout(onClose, 180);
  };

  useEffect(() => {
    inputRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") animClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);

  }, [onClose]);

  // Debounced search
  useEffect(() => {
    const q = value.trim();
    if (q.length < 2) {
      setResults([]);
      setSearching(false);
      return;
    }
    setSearching(true);
    const timer = setTimeout(async () => {
      try {
        const res = await (window as any).appInfo?.daemonSearchSites(q);
        if (Array.isArray(res)) setResults(res);
        else setResults([]);
      } catch {
        setResults([]);
      }
      setSearching(false);
    }, 300);
    return () => clearTimeout(timer);
  }, [value]);

  // Clear selection when results change
  useEffect(() => {
    setSelected(new Set());
  }, [results]);

  const toggleSelect = (id: number) => {
    if (existingSiteIds.has(id)) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/^https?:\/\//, "")
    .replace(/\/.*$/, "");
  const cleanDomain = normalized.replace(/^www\./, "");
  const looksLikeDomain = cleanDomain.includes(".");

  const canSubmit = selected.size > 0 || (looksLikeDomain && results.length === 0);

  const submit = () => {
    if (selected.size > 0) {
      const domains = results
        .filter((r) => selected.has(r.id))
        .map((r) => r.primary_domain);
      onAdd(domains);
    } else if (looksLikeDomain) {
      onAdd([cleanDomain]);
    }
  };

  const hasResults = results.length > 0;

  return (
    <div
      onClick={(e) => {
        if (e.target === e.currentTarget) animClose();
      }}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0, 0, 0, 0.5)",
        backdropFilter: "blur(3px)",
        WebkitBackdropFilter: "blur(3px)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1000,
        opacity: closing ? 0 : 1,
        transition: "opacity 0.18s cubic-bezier(0.25,1,0.5,1)",
        animation: "smurov-backdrop-in 0.25s cubic-bezier(0.25,1,0.5,1)",
      }}
    >
      <div
        style={{
          width: 420,
          background: "oklch(0.155 0.016 250 / 0.85)",
          backdropFilter: "blur(7px)",
          WebkitBackdropFilter: "blur(7px)",
          border: "1px solid oklch(0.24 0.013 250)",
          borderRadius: 10,
          padding: 20,
          boxShadow: "0 16px 48px rgba(0, 0, 0, 0.4)",
          position: "relative",
          transform: closing ? "scale(0.95) translateY(8px)" : "scale(1) translateY(0)",
          opacity: closing ? 0 : 1,
          transition: "transform 0.18s cubic-bezier(0.25,1,0.5,1), opacity 0.18s cubic-bezier(0.25,1,0.5,1)",
          animation: "smurov-fade-in 0.25s cubic-bezier(0.25,1,0.5,1)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: 14, animation: "smurov-blur-heavy 0.4s cubic-bezier(0.25,1,0.5,1) 0.05s both" }}>
          <div style={{ fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif", fontSize: 16, fontWeight: 700, color: "oklch(0.93 0.006 250)", letterSpacing: 0.3 }}>
            Add site
          </div>
          <button
            onClick={animClose}
            aria-label="Close"
            style={{
              width: 24, height: 24, borderRadius: 4,
              background: "transparent", border: "none",
              color: "oklch(0.40 0.01 250)", fontSize: 14,
              cursor: "pointer", display: "flex",
              alignItems: "center", justifyContent: "center",
              transition: "all 0.1s",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.background = "oklch(0.23 0.016 250)"; e.currentTarget.style.color = "oklch(0.93 0.006 250)"; }}
            onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = "oklch(0.40 0.01 250)"; }}
          >
            ✕
          </button>
        </div>
        <input
          ref={inputRef}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !hasResults) submit();
          }}
          placeholder="Search or enter domain..."
          style={{
            width: "100%",
            padding: "8px 12px",
            background: "oklch(0.12 0.014 250)",
            border: "1px solid oklch(0.24 0.013 250)",
            borderRadius: 5,
            color: "oklch(0.93 0.006 250)",
            fontSize: 13,
            outline: "none",
            fontFamily: "'Figtree', system-ui, sans-serif",
            marginBottom: 12,
            transition: "border-color 0.12s",
            animation: "smurov-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.12s both",
          }}
        />

        {/* Search results as card grid */}
        {hasResults && (
          <div style={{ marginBottom: 14 }}>
            <div
              style={{
                maxHeight: 240,
                overflowY: "auto",
                display: "grid",
                gridTemplateColumns: "repeat(3, 1fr)",
                gap: 6,
              }}
            >
              {results.map((site, i) => {
                const alreadyAdded = existingSiteIds.has(site.id);
                const isSelected = selected.has(site.id);
                const color = siteColor(site.primary_domain);
                const active = isSelected || alreadyAdded;
                return (
                  <div
                    key={site.id}
                    onClick={() => toggleSelect(site.id)}
                    style={{
                      padding: "8px 6px",
                      borderRadius: 6,
                      animation: `smurov-blur-light 0.25s cubic-bezier(0.25,1,0.5,1) ${0.03 + i * 0.03}s both`,
                      display: "flex",
                      flexDirection: "column",
                      alignItems: "center",
                      justifyContent: "center",
                      gap: 4,
                      minHeight: 72,
                      cursor: alreadyAdded ? "default" : "pointer",
                      transition: "all 0.1s",
                      background: isSelected
                        ? "oklch(0.18 0.028 235)"
                        : alreadyAdded
                        ? "oklch(0.14 0.02 150)"
                        : "oklch(0.12 0.014 250)",
                      border: isSelected
                        ? "1px solid oklch(0.68 0.12 235 / 0.4)"
                        : alreadyAdded
                        ? "1px solid oklch(0.72 0.15 150 / 0.3)"
                        : "1px solid oklch(0.24 0.013 250)",
                      opacity: alreadyAdded ? 0.4 : 1,
                      position: "relative",
                    }}
                    onMouseEnter={(e) => {
                      if (!alreadyAdded) { e.currentTarget.style.borderColor = "oklch(0.30 0.014 250)"; e.currentTarget.style.background = "oklch(0.19 0.018 250)"; }
                    }}
                    onMouseLeave={(e) => {
                      if (!alreadyAdded && !isSelected) { e.currentTarget.style.borderColor = "oklch(0.24 0.013 250)"; e.currentTarget.style.background = "oklch(0.12 0.014 250)"; }
                    }}
                  >
                    {alreadyAdded && (
                      <div style={{
                        position: "absolute", top: 3, right: 4,
                        fontSize: 7, fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
                        color: "oklch(0.72 0.15 150)", fontWeight: 700, letterSpacing: 0.5,
                      }}>
                        ADDED
                      </div>
                    )}
                    <SiteTileIcon domain={site.primary_domain} name={site.label} color={color} size={24} monochrome={!active} />
                    <div style={{
                      fontSize: 10, fontWeight: 600, fontFamily: "'Figtree', system-ui, sans-serif",
                      textAlign: "center", lineHeight: 1.2,
                      color: active ? "oklch(0.93 0.006 250)" : "oklch(0.60 0.012 250)",
                    }}>
                      {site.label}
                    </div>
                    <div style={{
                      fontSize: 8, fontFamily: "'Barlow', system-ui, sans-serif",
                      textAlign: "center", color: active ? "oklch(0.50 0.01 250)" : "oklch(0.40 0.01 250)",
                    }}>
                      {site.primary_domain}
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        {/* Manual preview — shown when no catalog results and input looks like a domain */}
        {!hasResults && looksLikeDomain && !searching && (
          <div style={{ marginBottom: 12 }}>
            <div style={{
              fontSize: 9, color: "oklch(0.40 0.01 250)", textTransform: "uppercase" as const,
              letterSpacing: 1.5, fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              fontWeight: 600, marginBottom: 6,
            }}>
              Manual entry
            </div>
            <div style={{
              padding: "10px 12px", background: "oklch(0.12 0.014 250)",
              border: "1px solid oklch(0.24 0.013 250)", borderRadius: 6,
              display: "flex", alignItems: "center", gap: 10,
              animation: "smurov-blur-light 0.3s cubic-bezier(0.25,1,0.5,1) 0.05s both",
            }}>
              <SiteTileIcon domain={cleanDomain} name={labelFromDomain(cleanDomain)} color={siteColor(cleanDomain)} size={24} />
              <div>
                <div style={{ fontSize: 12, fontWeight: 600, fontFamily: "'Figtree', system-ui, sans-serif", color: "oklch(0.93 0.006 250)" }}>
                  {labelFromDomain(cleanDomain)}
                </div>
                <div style={{ fontSize: 9, fontFamily: "'Barlow', system-ui, sans-serif", color: "oklch(0.50 0.01 250)" }}>
                  {cleanDomain}
                </div>
              </div>
            </div>
          </div>
        )}

        {/* Searching indicator */}
        {searching && (
          <div style={{ textAlign: "center", padding: "14px 0", marginBottom: 12, color: "oklch(0.40 0.01 250)", fontSize: 12, fontFamily: "'Figtree', system-ui, sans-serif" }}>
            Searching...
          </div>
        )}

        {/* Empty state */}
        {!hasResults && !searching && !looksLikeDomain && (
          <div style={{ textAlign: "center", padding: "16px 0", marginBottom: 12, color: "oklch(0.35 0.008 250)", fontSize: 12, fontFamily: "'Figtree', system-ui, sans-serif" }}>
            {cleanDomain ? "No matches — keep typing or enter a full domain" : "Type to search the catalog"}
          </div>
        )}

        <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", animation: "smurov-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) 0.2s both" }}>
          <button
            onClick={animClose}
            style={{
              padding: "6px 16px", borderRadius: 4,
              fontSize: 12, fontWeight: 600,
              cursor: "pointer", background: "transparent",
              color: "oklch(0.60 0.012 250)",
              border: "1px solid oklch(0.24 0.013 250)",
              fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              letterSpacing: 0.3,
              transition: "all 0.1s",
            }}
            onMouseEnter={(e) => { e.currentTarget.style.borderColor = "oklch(0.30 0.014 250)"; e.currentTarget.style.color = "oklch(0.93 0.006 250)"; }}
            onMouseLeave={(e) => { e.currentTarget.style.borderColor = "oklch(0.24 0.013 250)"; e.currentTarget.style.color = "oklch(0.60 0.012 250)"; }}
          >
            Cancel
          </button>
          <button
            onClick={submit}
            disabled={!canSubmit}
            style={{
              padding: "6px 16px", borderRadius: 4,
              fontSize: 12, fontWeight: 600,
              cursor: canSubmit ? "pointer" : "not-allowed",
              background: canSubmit ? "oklch(0.68 0.12 235)" : "oklch(0.23 0.016 250)",
              color: canSubmit ? "oklch(0.15 0.01 235)" : "oklch(0.40 0.01 250)",
              border: "none",
              fontFamily: "'Barlow Semi Condensed', system-ui, sans-serif",
              letterSpacing: 0.3,
              opacity: canSubmit ? 1 : 0.5,
              transition: "all 0.1s",
            }}
          >
            {selected.size > 0 ? `Add (${selected.size})` : "Add"}
          </button>
        </div>
      </div>
    </div>
  );
}

function Code({ children }: { children: React.ReactNode }) {
  return (
    <code
      style={{
        fontFamily: "'Barlow', system-ui, sans-serif",
        color: "#cbd5e1",
        background: "rgba(0,0,0,0.25)",
        padding: "1px 4px",
        borderRadius: 3,
        fontSize: 10,
      }}
    >
      {children}
    </code>
  );
}
