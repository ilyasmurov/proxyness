import { useState, useEffect, useRef, useCallback, ClipboardEvent } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { useStats } from "./hooks/useStats";
import { StatusBar } from "./components/StatusBar";
import { type ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";

import { NotificationBanner } from "./components/NotificationBanner";
import earthBgUrl from "./assets/earth-bg.mp4";

type TrafficMode = "all" | "selected";

// Migrate localStorage keys from the old "smurov-proxy-*" namespace to "proxyness-*".
// Runs once on first launch after the rename — copies old value if new key is absent,
// then deletes the old key so this is a no-op on subsequent launches.
if (typeof localStorage !== "undefined") {
  const renames: [string, string][] = [
    ["smurov-proxy-key",           "proxyness-key"],
    ["smurov-proxy-mode",          "proxyness-mode"],
    ["smurov-proxy-no-tls",        "proxyness-no-tls"],
    ["smurov-proxy-all-sites-on",  "proxyness-all-sites-on"],
    ["smurov-proxy-browsers-on",   "proxyness-browsers-on"],
    ["smurov-proxy-local-sites",   "proxyness-local-sites"],
    ["smurov-proxy-last-sync-at",  "proxyness-last-sync-at"],
    ["smurov-proxy-sites",         "proxyness-sites"],
    ["smurov-proxy-enabled-sites", "proxyness-enabled-sites"],
    ["smurov-proxy-pending-ops",   "proxyness-pending-ops"],
  ];
  for (const [oldK, newK] of renames) {
    const v = localStorage.getItem(oldK);
    if (v !== null && localStorage.getItem(newK) === null) {
      localStorage.setItem(newK, v);
    }
    if (v !== null) localStorage.removeItem(oldK);
  }
}

// Migrate legacy users off the long-dead "browsers-only" ProxyMode.
// The mode was removed in 1.33.0 along with its tab UI; leaving the old
// value in localStorage would pin those installs in an unreachable state.
if (typeof localStorage !== "undefined" && localStorage.getItem("proxyness-mode") === "socks5") {
  localStorage.setItem("proxyness-mode", "tun");
}

const SERVERS = [
  { id: "aeza",    label: "Aeza NL",    addr: "95.181.162.242:443" },
  { id: "timeweb", label: "Timeweb NL", addr: "82.97.246.65:443"   },
];
const STORAGE_KEY = "proxyness-key";
const SERVER_STORAGE_KEY = "proxyness-server";
const defaultServerId = () => localStorage.getItem(SERVER_STORAGE_KEY) || "timeweb";
const serverAddrFor = (id: string) => SERVERS.find((s) => s.id === id)?.addr || SERVERS[0].addr;

// ---------------------------------------------------------------------------
// Settings Page (sidebar nav variant)
// ---------------------------------------------------------------------------
type SettingsSection = "general" | "notifications" | "extension" | "account" | "diagnostics";

const NOTIF_KEYS = {
  connect: "notif-connect",
  disconnect: "notif-disconnect",
  reconnect: "notif-reconnect",
  updates: "notif-updates",
} as const;
const notifDefault = (key: string) => localStorage.getItem(key) !== "false"; // enabled by default

function SettingsPage({ version, transportMode, onTransportChange, onChangeKey, onHelperError, isConnected, serverId, servers, onServerChange, c, fd, fb, fm }: {
  version: string;
  transportMode: string;
  onTransportChange: (mode: string) => void;
  onChangeKey: () => void;
  onHelperError?: (err: string) => void;
  isConnected: boolean;
  serverId: string;
  servers: { id: string; label: string; addr: string }[];
  onServerChange: (id: string) => void;
  c: Record<string, string>;
  fd: string; fb: string; fm: string;
}) {
  const [section, setSection] = useState<SettingsSection>("general");
  const [token, setToken] = useState("");
  const [copied, setCopied] = useState(false);
  const [notifConnect, setNotifConnect] = useState(() => notifDefault(NOTIF_KEYS.connect));
  const [notifDisconnect, setNotifDisconnect] = useState(() => notifDefault(NOTIF_KEYS.disconnect));
  const [notifReconnect, setNotifReconnect] = useState(() => notifDefault(NOTIF_KEYS.reconnect));
  const [notifUpdates, setNotifUpdates] = useState(() => notifDefault(NOTIF_KEYS.updates));
  // Keyboard nav — ArrowUp/ArrowDown cycle focus + active section
  // between sidebar nav items. Standard tablist pattern.
  const NAV_SECTIONS: SettingsSection[] = ["general", "notifications", "extension", "account", "diagnostics"];
  const navRefs = useRef<Record<SettingsSection, HTMLButtonElement | null>>({
    general: null, notifications: null, extension: null, account: null, diagnostics: null,
  });
  const handleNavKey = (e: React.KeyboardEvent, id: SettingsSection) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const idx = NAV_SECTIONS.indexOf(id);
    const nextIdx = e.key === "ArrowDown"
      ? (idx + 1) % NAV_SECTIONS.length
      : (idx - 1 + NAV_SECTIONS.length) % NAV_SECTIONS.length;
    const nextId = NAV_SECTIONS[nextIdx];
    setSection(nextId);
    // Focus lands on the newly-selected item after React commits.
    requestAnimationFrame(() => navRefs.current[nextId]?.focus());
  };

  useEffect(() => {
    (window as any).appInfo?.getDaemonToken().then((t: string) => setToken(t || ""));
  }, []);

  const copyToken = () => {
    navigator.clipboard.writeText(token);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const navItem = (id: SettingsSection, label: string) => (
    <button
      key={id}
      type="button"
      ref={(el) => { navRefs.current[id] = el; }}
      onClick={() => setSection(id)}
      onKeyDown={(e) => handleNavKey(e, id)}
      style={{
        padding: "7px 16px", fontFamily: fb, fontSize: 12, fontWeight: 500,
        color: section === id ? c.t1 : c.t3, cursor: "pointer",
        background: section === id ? c.bg2 : "transparent",
        border: "none", textAlign: "left" as const, width: "100%",
        transition: "all 0.1s",
      }}
      onMouseEnter={(e) => { if (section !== id) e.currentTarget.style.color = c.t2; e.currentTarget.style.background = c.bg2; }}
      onMouseLeave={(e) => { if (section !== id) { e.currentTarget.style.color = c.t3; e.currentTarget.style.background = "transparent"; } }}
    >
      {label}
    </button>
  );

  const sBtn = (label: string, onClick: () => void, danger?: boolean) => (
    <button
      onClick={onClick}
      style={{
        padding: "5px 14px", borderRadius: 5, fontFamily: fd, fontSize: 12,
        fontWeight: 600, letterSpacing: 0.3, cursor: "pointer",
        border: `1px solid ${danger ? "oklch(0.62 0.19 25 / 0.2)" : c.b1}`,
        background: danger ? "oklch(0.14 0.02 25)" : c.bg2,
        color: danger ? c.rd : c.t2,
        transition: "all 0.1s",
      }}
      onMouseEnter={(e) => { e.currentTarget.style.borderColor = danger ? "oklch(0.62 0.19 25 / 0.4)" : c.b1; e.currentTarget.style.color = danger ? c.rd : c.t1; }}
      onMouseLeave={(e) => { e.currentTarget.style.borderColor = danger ? "oklch(0.62 0.19 25 / 0.2)" : c.b1; e.currentTarget.style.color = danger ? c.rd : c.t2; }}
    >
      {label}
    </button>
  );

  // Animation stagger helper. Keeps inline-style noise readable when
  // cascading a whole section's children in on section change.
  // Durations match the ones used elsewhere (hero zone, mode bar).
  const anim = (kind: "heavy" | "med" | "light" | "fade" | "row", delay: number) => {
    const dur = kind === "heavy" ? 0.5 : kind === "row" ? 0.3 : 0.4;
    return `pn-blur-${kind} ${dur}s cubic-bezier(0.25,1,0.5,1) ${delay}s both`;
  };

  const fieldLabel = (text: string, delay = 0) => (
    <div style={{ fontFamily: fd, fontSize: 10, fontWeight: 600, color: c.t3, letterSpacing: 1.5, textTransform: "uppercase" as const, marginBottom: 6, animation: anim("fade", delay) }}>
      {text}
    </div>
  );

  const animatedDivider = (delay: number) => (
    <div style={{ height: 1, background: c.b1, margin: "16px 0", animation: anim("fade", delay) }} />
  );

  const divider = <div style={{ height: 1, background: c.b1, margin: "16px 0" }} />;

  return (
    <div style={{ display: "flex", flex: 1, overflow: "hidden", minHeight: 0, animation: "pn-blur-med 0.35s cubic-bezier(0.25,1,0.5,1) both" }}>
      {/* Sidebar nav */}
      <div style={{
        width: 160, flexShrink: 0, borderRight: `1px solid ${c.b1}`,
        padding: "16px 0", display: "flex", flexDirection: "column", gap: 1,
      }}>
        <div style={{ animation: "pn-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) 0.05s both" }}>{navItem("general", "General")}</div>
        <div style={{ animation: "pn-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) 0.1s both" }}>{navItem("notifications", "Notifications")}</div>
        <div style={{ animation: "pn-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) 0.15s both" }}>{navItem("extension", "Extension")}</div>
        <div style={{ animation: "pn-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) 0.2s both" }}>{navItem("account", "Account")}</div>
        <div style={{ animation: "pn-blur-row 0.3s cubic-bezier(0.25,1,0.5,1) 0.25s both" }}>{navItem("diagnostics", "Diagnostics")}</div>
      </div>

      {/* Panel */}
      <div key={section} style={{ flex: 1, padding: "20px 24px", overflowY: "auto", animation: "pn-blur-light 0.3s cubic-bezier(0.25,1,0.5,1) both" }}>

        {section === "general" && (
          <>
            <div style={{ fontFamily: fd, fontSize: 16, fontWeight: 700, color: c.t1, letterSpacing: 0.3, marginBottom: 4, animation: anim("heavy", 0.05) }}>General</div>
            <div style={{ fontFamily: fb, fontSize: 12, color: c.t3, marginBottom: 20, lineHeight: 1.5, animation: anim("light", 0.1) }}>App version and connection settings.</div>

            {fieldLabel("Version", 0.15)}
            <div style={{ display: "flex", alignItems: "center", gap: 12, marginBottom: 0, animation: anim("row", 0.2) }}>
              <span style={{ fontFamily: fm, fontSize: 12, color: c.t2, fontVariantNumeric: "tabular-nums" }}>{version || "—"}</span>
              {sBtn("Check for updates", () => (window as any).appInfo?.openUpdate())}
            </div>

            {animatedDivider(0.23)}

            {fieldLabel("Proxy Server", 0.27)}
            <div style={{ display: "flex", gap: 2, padding: 2, background: c.bg1, borderRadius: 5, width: "fit-content", marginBottom: 4, animation: anim("light", 0.3) }}>
              {servers.map((s) => {
                const active = serverId === s.id;
                const activeColor = isConnected ? c.am : c.t2;
                const activeBg = isConnected ? c.amb : c.bg2;
                return (
                  <button
                    key={s.id}
                    onClick={() => onServerChange(s.id)}
                    style={{
                      padding: "5px 14px", borderRadius: 4, border: "none",
                      fontFamily: fd, fontSize: 11, fontWeight: 600, letterSpacing: 0.3,
                      cursor: "pointer", transition: "all 0.1s",
                      background: active ? activeBg : "transparent",
                      color: active ? activeColor : c.t3,
                    }}
                  >
                    {s.label}
                  </button>
                );
              })}
            </div>

            {animatedDivider(0.35)}

            {fieldLabel("Transport Protocol", 0.4)}
            <div style={{ display: "flex", gap: 2, padding: 2, background: c.bg1, borderRadius: 5, width: "fit-content", animation: anim("light", 0.45) }}>
              {["auto", "udp", "tls"].map((m) => {
                const active = transportMode === m;
                // Mirror the traffic-mode switch: amber highlight only
                // when the proxy is connected, muted grey when idle, so
                // active-but-idle doesn't visually imply a running system.
                const activeColor = isConnected ? c.am : c.t2;
                const activeBg = isConnected ? c.amb : c.bg2;
                return (
                  <button
                    key={m}
                    onClick={() => onTransportChange(m)}
                    style={{
                      padding: "5px 14px", borderRadius: 4, border: "none",
                      fontFamily: fd, fontSize: 11, fontWeight: 600, letterSpacing: 0.3,
                      cursor: "pointer", transition: "all 0.1s",
                      background: active ? activeBg : "transparent",
                      color: active ? activeColor : c.t3,
                    }}
                  >
                    {m.toUpperCase()}
                  </button>
                );
              })}
            </div>
          </>
        )}

        {section === "notifications" && (() => {
          const toggle = (label: string, desc: string, value: boolean, set: (v: boolean) => void, storageKey: string, delay: number) => (
            <div style={{ display: "flex", alignItems: "center", gap: 12, animation: anim("row", delay) }}>
              <div style={{ flex: 1 }}>
                <div style={{ fontFamily: fd, fontSize: 13, fontWeight: 600, color: c.t1 }}>{label}</div>
                <div style={{ fontFamily: fb, fontSize: 11, color: c.t3, marginTop: 2 }}>{desc}</div>
              </div>
              <button
                onClick={() => { const next = !value; set(next); localStorage.setItem(storageKey, String(next)); }}
                style={{
                  width: 36, height: 20, borderRadius: 10, border: "none", cursor: "pointer",
                  background: value ? c.am : c.bg1,
                  position: "relative" as const, transition: "background 0.15s",
                  flexShrink: 0,
                }}
              >
                <div style={{
                  width: 14, height: 14, borderRadius: 7,
                  background: value ? c.bg0 : c.t3,
                  position: "absolute" as const, top: 3,
                  left: value ? 19 : 3,
                  transition: "left 0.15s, background 0.15s",
                }} />
              </button>
            </div>
          );
          return (
            <>
              <div style={{ fontFamily: fd, fontSize: 16, fontWeight: 700, color: c.t1, letterSpacing: 0.3, marginBottom: 4, animation: anim("heavy", 0.05) }}>Notifications</div>
              <div style={{ fontFamily: fb, fontSize: 12, color: c.t3, marginBottom: 20, lineHeight: 1.5, animation: anim("light", 0.1) }}>Control which system notifications are shown.</div>

              {toggle("Connected", "Notify when proxy connects", notifConnect, setNotifConnect, NOTIF_KEYS.connect, 0.15)}
              {animatedDivider(0.2)}
              {toggle("Disconnected", "Notify when proxy disconnects", notifDisconnect, setNotifDisconnect, NOTIF_KEYS.disconnect, 0.25)}
              {animatedDivider(0.3)}
              {toggle("Reconnecting", "Notify when proxy is reconnecting", notifReconnect, setNotifReconnect, NOTIF_KEYS.reconnect, 0.35)}
              {animatedDivider(0.4)}
              {toggle("Updates", "Notify about available updates", notifUpdates, (v) => {
                setNotifUpdates(v);
                (window as any).appInfo?.setNotifUpdates(v);
              }, NOTIF_KEYS.updates, 0.45)}
            </>
          );
        })()}

        {section === "extension" && (
          <>
            <div style={{ fontFamily: fd, fontSize: 16, fontWeight: 700, color: c.t1, letterSpacing: 0.3, marginBottom: 4, animation: anim("heavy", 0.05) }}>Browser Extension</div>
            <div style={{ fontFamily: fb, fontSize: 12, color: c.t3, marginBottom: 20, lineHeight: 1.5, animation: anim("light", 0.1) }}>
              Connect the Proxyness browser extension to control per-site proxying.
            </div>

            {fieldLabel("Install", 0.15)}
            <div style={{
              display: "flex", alignItems: "center", gap: 14, padding: "14px 16px",
              background: c.bg2, border: `1px solid ${c.b1}`, borderRadius: 8,
              animation: anim("row", 0.2),
            }}>
              <svg width="40" height="40" viewBox="0 0 128 128" style={{ flexShrink: 0 }}>
                <defs><mask id="ext-eye"><rect width="128" height="128" fill="white"/><ellipse cx="64" cy="48" rx="16" ry="16" fill="black"/></mask></defs>
                <path d="M16,48 C16,20 33.9,4 64,4 C94.1,4 112,20 112,48 L112,100 L96,84 L80,100 L64,84 L48,100 L32,84 L16,100 Z" fill={c.am} mask="url(#ext-eye)"/>
              </svg>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontFamily: fd, fontSize: 13, fontWeight: 600, color: c.t1, marginBottom: 2 }}>Proxyness for Chrome</div>
                <div style={{ fontFamily: fb, fontSize: 11, color: c.t3, lineHeight: 1.4 }}>Per-site proxy rules, site discovery, block detection</div>
              </div>
              <button
                onClick={() => (window as any).appInfo?.installExtension()}
                style={{
                  padding: "6px 16px", borderRadius: 5, fontFamily: fd, fontSize: 11,
                  fontWeight: 600, letterSpacing: 0.3, cursor: "pointer", flexShrink: 0,
                  border: `1px solid oklch(0.78 0.155 75 / 0.3)`,
                  background: "oklch(0.78 0.155 75 / 0.12)",
                  color: c.am, transition: "all 0.1s",
                }}
                onMouseEnter={(e) => { e.currentTarget.style.background = "oklch(0.78 0.155 75 / 0.2)"; e.currentTarget.style.borderColor = "oklch(0.78 0.155 75 / 0.5)"; }}
                onMouseLeave={(e) => { e.currentTarget.style.background = "oklch(0.78 0.155 75 / 0.12)"; e.currentTarget.style.borderColor = "oklch(0.78 0.155 75 / 0.3)"; }}
              >
                Install
              </button>
            </div>
            <div style={{ marginTop: 12, animation: anim("light", 0.25) }}>
              {[
                ["1", "Click Install to open the extension folder"],
                ["2", "Open chrome://extensions and enable Developer mode"],
                ["3", "Click Load unpacked and select the opened folder"],
              ].map(([n, text]) => (
                <div key={n} style={{ display: "flex", alignItems: "flex-start", gap: 8, marginBottom: 6 }}>
                  <div style={{
                    width: 16, height: 16, borderRadius: 8, flexShrink: 0, marginTop: 1,
                    background: "oklch(0.78 0.155 75 / 0.12)",
                    fontFamily: fd, fontSize: 10, fontWeight: 700, color: c.am,
                    display: "flex", alignItems: "center", justifyContent: "center",
                  }}>{n}</div>
                  <div style={{ fontFamily: fb, fontSize: 11, color: c.t3, lineHeight: 1.4 }}>{text}</div>
                </div>
              ))}
            </div>

            {animatedDivider(0.35)}

            {fieldLabel("Daemon Token", 0.4)}
            <div style={{ display: "flex", alignItems: "center", gap: 8, animation: anim("row", 0.45) }}>
              <div style={{
                flex: 1, padding: "6px 10px", background: c.bg2, border: `1px solid ${c.b1}`,
                borderRadius: 5, fontFamily: fm, fontSize: 11, color: c.t2,
                overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const,
                userSelect: "all" as const, cursor: "text",
              }}>
                {token || "(daemon not running)"}
              </div>
              <button
                onClick={copyToken}
                style={{
                  padding: "4px 10px", borderRadius: 4, fontFamily: fd, fontSize: 10,
                  fontWeight: 600, letterSpacing: 0.5, cursor: "pointer",
                  background: copied ? "oklch(0.16 0.025 150)" : c.bg2,
                  border: `1px solid ${copied ? "oklch(0.72 0.15 150 / 0.3)" : c.b1}`,
                  color: copied ? c.gn : c.t2,
                  transition: "all 0.15s",
                }}
              >
                {copied ? "Copied" : "Copy"}
              </button>
            </div>
          </>
        )}

        {section === "account" && (
          <>
            <div style={{ fontFamily: fd, fontSize: 16, fontWeight: 700, color: c.t1, letterSpacing: 0.3, marginBottom: 4, animation: anim("heavy", 0.05) }}>Account</div>
            <div style={{ fontFamily: fb, fontSize: 12, color: c.t3, marginBottom: 20, lineHeight: 1.5, animation: anim("light", 0.1) }}>Manage your device connection.</div>

            {fieldLabel("Access Key", 0.15)}
            <div style={{ display: "flex", alignItems: "center", gap: 12, animation: anim("row", 0.2) }}>
              <span style={{ fontFamily: fb, fontSize: 13, color: c.t2, flex: 1 }}>
                Disconnect and enter a different access key.
              </span>
              {sBtn("Change Key", onChangeKey, true)}
            </div>
          </>
        )}

        {section === "diagnostics" && (
          <>
            <div style={{ fontFamily: fd, fontSize: 16, fontWeight: 700, color: c.t1, letterSpacing: 0.3, marginBottom: 4, animation: anim("heavy", 0.05) }}>Diagnostics</div>
            <div style={{ fontFamily: fb, fontSize: 12, color: c.t3, marginBottom: 20, lineHeight: 1.5, animation: anim("light", 0.1) }}>View logs and manage background processes.</div>

            {fieldLabel("Logs", 0.15)}
            <div style={{ display: "flex", alignItems: "center", gap: 12, animation: anim("row", 0.2) }}>
              <span style={{ fontFamily: fb, fontSize: 13, color: c.t2, flex: 1 }}>
                Open the log viewer window.
              </span>
              {sBtn("Open Logs", () => (window as any).appInfo?.openLogs())}
            </div>

            {fieldLabel("Daemon", 0.3)}
            <div style={{ display: "flex", alignItems: "center", gap: 12, animation: anim("row", 0.35) }}>
              <span style={{ fontFamily: fb, fontSize: 13, color: c.t2, flex: 1 }}>
                Local SOCKS5 + TUN proxy engine.
              </span>
              {sBtn("Restart", () => (window as any).appInfo?.restartDaemon())}
            </div>

            {fieldLabel("Helper", 0.45)}
            <div style={{ display: "flex", alignItems: "center", gap: 12, animation: anim("row", 0.5) }}>
              <span style={{ fontFamily: fb, fontSize: 13, color: c.t2, flex: 1 }}>
                Privileged TUN device manager.
              </span>
              {sBtn("Restart", async () => {
                const s = await (window as any).appInfo?.restartHelper();
                if (s && !s.ok) onHelperError?.(s.error);
                else onHelperError?.("");
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

export function App() {
  const [key, setKey] = useState(() => localStorage.getItem(STORAGE_KEY) || "");
  const [serverId, setServerId] = useState<string>(defaultServerId);
  const SERVER = serverAddrFor(serverId);
  const [showSetup, setShowSetup] = useState(!key);
  const [keyError, setKeyError] = useState("");
  const [keyValidating, setKeyValidating] = useState(false);
  const [helperError, setHelperError] = useState("");

  // Fatal errors that mean the key/device can't connect — kick to setup
  const isKeyRejected = (err: string) =>
    err.includes("bound to a different machine") ||
    err.includes("machine id rejected") ||
    err.includes("invalid key");
  const [version, setVersion] = useState("");
  const [activeTab, setActiveTab] = useState<"main" | "settings">("main");
  const [trafficMode, setTrafficMode] = useState<"all" | "selected">("all");
  const { status: socksStatus, error: socksError, loading: socksLoading, connect, disconnect } = useDaemon();
  const [proxyMode, setProxyMode] = useState<ProxyMode>(
    () => (localStorage.getItem("proxyness-mode") as ProxyMode) || "tun"
  );

  // Transport state
  const [transportMode, setTransportMode] = useState<string>("auto");
  const [activeTransport, setActiveTransport] = useState<string>("");

  // TUN state
  const [tunStatus, setTunStatus] = useState<"inactive" | "active" | "reconnecting">("inactive");
  const [tunUptime, setTunUptime] = useState(0);
  const [tunLoading, setTunLoading] = useState(false);
  const [tunError, setTunError] = useState<string | null>(null);
  const [reconnecting, setReconnecting] = useState(false);
  // reconnectingRef mirrors `reconnecting` state but is updated synchronously.
  // The state-only guard in startReconnect was racy: when the polling effect
  // and a transport-error effect both fired in the same React tick, both saw
  // reconnecting=false (state hadn't flushed yet), both passed the guard, and
  // both spawned independent retry loops — each calling /tun/start, each
  // triggering "engine already active, restarting" on the daemon side.
  // Storming the engine restart path corrupts NAT state and burns CPU.
  const reconnectingRef = useRef(false);
  const reconnectRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const wasConnected = useRef(false);

  useEffect(() => {
    (window as any).appInfo?.getVersion().then((v: string) => setVersion(v));
    (window as any).appInfo?.getHelperStatus?.().then((s: { ok: boolean; error: string }) => {
      if (!s.ok) setHelperError(s.error);
    });
    // Send stored key to main process so config poller can start
    const storedKey = localStorage.getItem(STORAGE_KEY);
    if (storedKey) (window as any).updater?.storeKey(storedKey);
    // Sync notification prefs to main process
    (window as any).appInfo?.setNotifUpdates(notifDefault(NOTIF_KEYS.updates));
  }, []);



  const maxRetries = 5;
  const retryDelay = 5000;

  const startReconnect = useCallback(() => {
    // Synchronous guard via ref (see comment on reconnectingRef above).
    // The previous `if (reconnecting || ...)` check on React state was
    // racy across same-tick callers.
    if (reconnectingRef.current || !key) return;
    reconnectingRef.current = true;
    setReconnecting(true);
    setTunError(null);
    let attempt = 0;

    const finish = (err: string | null) => {
      reconnectingRef.current = false;
      setReconnecting(false);
      if (err !== undefined) setTunError(err);
    };

    const tryReconnect = async () => {
      attempt++;
      console.log(`[reconnect] attempt ${attempt}/${maxRetries}`);
      try {
        if (proxyMode === "tun") {
          // connect() returns false on non-ok (e.g. 409 from lockDevice race
          // against a stale server-side lock on cold start). Treat that as
          // a retryable failure instead of silently leaving SOCKS5 down.
          const ok = await connect(SERVER, key);
          if (!ok) throw new Error("socks5 connect failed");
          const result = await (window as any).tunProxy?.start(SERVER, key);
          if (result && !result.ok) throw new Error(result.error);
          setTunStatus("active");
          wasConnected.current = true;
        } else {
          const ok = await connect(SERVER, key);
          if (!ok) throw new Error("connect failed");
        }
        finish(null);
      } catch {
        if (attempt >= maxRetries) {
          finish("Server unavailable");
        } else {
          reconnectRef.current = setTimeout(tryReconnect, retryDelay);
        }
      }
    };

    tryReconnect();
  }, [key, proxyMode, connect, SERVER]);

  // Cleanup reconnect timer on unmount
  useEffect(() => {
    return () => {
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
    };
  }, []);

  // Poll TUN status when in TUN mode
  useEffect(() => {
    if (proxyMode !== "tun") return;
    const poll = async () => {
      try {
        const s = await (window as any).tunProxy?.getStatus();
        if (s) {
          // Map daemon's tun status enum into our local one (which now
          // includes the third "reconnecting" value).
          let next: "inactive" | "active" | "reconnecting" = "inactive";
          if (s.status === "active") next = "active";
          else if (s.status === "reconnecting") next = "reconnecting";
          setTunStatus(next);
          setTunUptime(s.uptime || 0);
          const active = next === "active";
          if (s.error) setTunError(s.error);
          // Only fire client-side startReconnect on a HARD disconnect, not
          // while the daemon is still trying to reconnect on its own.
          if (wasConnected.current && !active && next !== "reconnecting" && s.error) {
            if (isKeyRejected(s.error)) {
              (window as any).sysproxy?.disable();
              localStorage.removeItem(STORAGE_KEY);
              setKey("");
              setKeyError(s.error);
              setShowSetup(true);
            } else {
              startReconnect();
            }
          }
          wasConnected.current = active;
        }
        const tr = await (window as any).transport?.getMode();
        if (tr) {
          setTransportMode(tr.mode || "auto");
          setActiveTransport(tr.active || "");
        }
      } catch {}
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => clearInterval(interval);
  }, [proxyMode, startReconnect]);

  // Detect SOCKS5 server loss and auto-reconnect
  useEffect(() => {
    if (proxyMode !== "socks5") return;
    if (socksError && !reconnecting && key) {
      if (isKeyRejected(socksError)) {
        (window as any).sysproxy?.disable();
        localStorage.removeItem(STORAGE_KEY);
        setKey("");
        setKeyError(socksError);
        setShowSetup(true);
      } else {
        startReconnect();
      }
    }
  }, [socksError, proxyMode, reconnecting, key, startReconnect]);

  // Daemon-reported reconnecting state — distinct from the client-side
  // `reconnecting` flag (which drives startReconnect()'s loop). Both
  // mean the user should see "Reconnecting…" in the UI; OR them.
  const daemonReconnecting =
    socksStatus.status === "reconnecting" || tunStatus === "reconnecting";

  // Effective state based on mode
  const isConnected = proxyMode === "tun"
    ? tunStatus === "active"
    : socksStatus.status === "connected";
  const isLoading = reconnecting || (proxyMode === "tun" ? tunLoading : socksLoading);
  const currentError = reconnecting ? null : (proxyMode === "tun" ? tunError : socksError);
  const uptime = proxyMode === "tun" ? tunUptime : socksStatus.uptime;
  const stats = useStats(isConnected);

  const handleModeChange = async (m: ProxyMode) => {
    if (m === proxyMode) return;
    const wasConnected = isConnected;
    if (wasConnected) {
      if (proxyMode === "tun") await tunDisconnect();
      else await disconnect();
    }
    setProxyMode(m);
    localStorage.setItem("proxyness-mode", m);
    if (wasConnected && key) {
      if (m === "tun") await tunConnect(SERVER, key);
      else await connect(SERVER, key);
    }
  };

  const tunConnect = useCallback(async (server: string, k: string) => {
    setTunLoading(true);
    setTunError(null);
    try {
      // Start SOCKS5 tunnel + enable system proxy with PAC. This has to
      // run BEFORE the TUN engine: browsers rely on PAC/SOCKS5 for the
      // per-site proxy rules, and if this step fails silently the whole
      // "proxy one site in Chrome" flow is dead while the UI still shows
      // green because TUN (per-app routing) reports healthy.
      //
      // One retry with 800ms backoff covers transient lockDevice /
      // handshake races — e.g. the previous daemon session hasn't fully
      // released the device lock on the server yet when /connect reaches
      // it a beat too soon.
      let socksOk = await connect(server, k);
      if (!socksOk) {
        console.warn("[tunConnect] first /connect failed, retrying in 800ms");
        await new Promise((r) => setTimeout(r, 800));
        socksOk = await connect(server, k);
      }
      if (socksOk) {
        (window as any).sysproxy?.setPacSites({ proxy_all: true });
        (window as any).sysproxy?.enable();
      } else {
        // Don't bail — TUN still works for per-app proxying (Telegram,
        // Cursor, etc.). Just surface the failure so the user knows
        // browsers won't get per-site proxying until they reconnect.
        console.warn("[tunConnect] SOCKS5 tunnel failed twice, continuing with TUN only");
        setTunError("Browser proxy is off (SOCKS5 failed to start). Apps still go through TUN. Reconnect to retry.");
      }

      // Start TUN for apps
      const result = await (window as any).tunProxy?.start(server, k);
      if (result && !result.ok) {
        const err = result.error || "Failed to connect";
        if (isKeyRejected(err)) {
          (window as any).sysproxy?.disable();
          disconnect();
          localStorage.removeItem(STORAGE_KEY);
          setKey("");
          setKeyError(err);
          setShowSetup(true);
          return;
        }
        setTunError(err);
      } else {
        setTunStatus("active");
      }
    } catch {
      setTunError("Failed to connect");
    } finally {
      setTunLoading(false);
    }
  }, [connect]);

  const stopReconnect = useCallback(() => {
    if (reconnectRef.current) {
      clearTimeout(reconnectRef.current);
      reconnectRef.current = null;
    }
    setReconnecting(false);
    wasConnected.current = false;
  }, []);

  const tunDisconnect = useCallback(async () => {
    stopReconnect();
    setTunLoading(true);
    try {
      await (window as any).tunProxy?.stop();
      (window as any).sysproxy?.disable();
      disconnect();
      setTunStatus("inactive");
      setTunError(null);
    } catch {} finally {
      setTunLoading(false);
    }
  }, [disconnect, stopReconnect]);

  const handleServerChange = useCallback(
    async (id: string) => {
      if (id === serverId) return;
      localStorage.setItem(SERVER_STORAGE_KEY, id);
      setServerId(id);
      const nextAddr = serverAddrFor(id);
      if (!key) return;
      if (isConnected) {
        if (proxyMode === "tun") {
          await tunDisconnect();
          await new Promise((r) => setTimeout(r, 300));
          await tunConnect(nextAddr, key);
        } else {
          await disconnect();
          await new Promise((r) => setTimeout(r, 300));
          await connect(nextAddr, key);
        }
      }
    },
    [serverId, isConnected, key, proxyMode, tunConnect, tunDisconnect, connect, disconnect],
  );

  // Handle transport mode change from the StatusBar badge dropdown.
  // Persist the transport mode on the daemon. The new mode takes effect on
  // the next manual Connect — no automatic reconnect.
  const handleTransportChange = useCallback(
    async (mode: string) => {
      try {
        await (window as any).transport?.setMode(mode);
        setTransportMode(mode);
      } catch {
        /* ignore */
      }
    },
    [],
  );

  // Update tray icon based on connection status
  useEffect(() => {
    const state = isConnected ? "connected" : (daemonReconnecting || reconnecting) ? "connecting" : "disconnected";
    (window as any).appInfo?.setTrayStatus(state);
  }, [isConnected, daemonReconnecting, reconnecting]);

  // Desktop notifications on connection state transitions.
  // prevNotifState tracks the last state we notified on so we only fire
  // on real edges (connected → disconnected, etc.), not on every render.
  // Skip the very first run so we don't spam a "Disconnected" toast at
  // app launch before auto-connect has had a chance to run.
  const prevNotifState = useRef<"connected" | "disconnected" | "reconnecting" | null>(null);
  useEffect(() => {
    const current: "connected" | "disconnected" | "reconnecting" =
      isConnected ? "connected" : daemonReconnecting || reconnecting ? "reconnecting" : "disconnected";
    const prev = prevNotifState.current;
    prevNotifState.current = current;
    if (prev === null) return;
    if (prev === current) return;
    const notify = (window as any).appInfo?.showNotification;
    if (!notify) return;
    if (current === "connected" && notifDefault(NOTIF_KEYS.connect)) notify("Proxyness", "Connected");
    else if (current === "reconnecting" && notifDefault(NOTIF_KEYS.reconnect)) notify("Proxyness", "Reconnecting...");
    else if (current === "disconnected" && notifDefault(NOTIF_KEYS.disconnect)) notify("Proxyness", "Disconnected");
  }, [isConnected, daemonReconnecting, reconnecting]);

  // On system wake, the daemon's UDP session is silently dead (server forgot
  // us during sleep). Tear down the old state and reconnect instead of waiting
  // for the keepalive timeout. Uses a ref for the "was connected" check so the
  // resume handler always sees the latest value without re-subscribing on
  // every status change.
  const wasConnectedOnWakeRef = useRef(false);
  useEffect(() => {
    wasConnectedOnWakeRef.current = isConnected;
  }, [isConnected]);
  useEffect(() => {
    const app = (window as any).appInfo;
    if (!app?.onSystemResumed) return;
    const cleanup = app.onSystemResumed(async () => {
      if (!wasConnectedOnWakeRef.current || !key) return;
      console.log("[wake] system resumed, forcing reconnect");
      if (proxyMode === "tun") {
        await tunDisconnect();
      } else {
        await disconnect();
      }
      // Let the network stack settle before trying to reach the server.
      await new Promise((r) => setTimeout(r, 500));
      startReconnect();
    });
    return cleanup;
  }, [key, proxyMode, tunDisconnect, disconnect, startReconnect]);

  // Handle tray connect/disconnect clicks
  useEffect(() => {
    const app = (window as any).appInfo;
    if (!app) return;
    app.onTrayConnect(() => {
      if (!isConnected && key) {
        if (proxyMode === "tun") {
          tunConnect(SERVER, key);
        } else {
          connect(SERVER, key);
        }
      }
    });
    app.onTrayDisconnect(() => {
      if (isConnected) {
        if (proxyMode === "tun") {
          tunDisconnect();
        } else {
          disconnect();
        }
      }
    });
  }, [key, isConnected, proxyMode, connect, disconnect, tunConnect, tunDisconnect, SERVER]);


  const connectWithKey = async (k: string) => {
    const trimmed = k.trim();
    if (!trimmed) return;
    setKeyError("");
    setKeyValidating(true);
    try {
      const res = await fetch(
        `http://127.0.0.1:9090/validate-key?server=${encodeURIComponent(SERVER)}&key=${encodeURIComponent(trimmed)}`
      );
      const body = await res.json();
      if (!body.valid) {
        setKeyError("Invalid key");
        return;
      }
    } catch {
      setKeyError("Server unreachable");
      return;
    } finally {
      setKeyValidating(false);
    }
    localStorage.setItem(STORAGE_KEY, trimmed);
    setKey(trimmed);
    setShowSetup(false);
    (window as any).updater?.storeKey(trimmed);
    if (proxyMode === "tun") {
      tunConnect(SERVER, trimmed);
    } else {
      connect(SERVER, trimmed);
    }
  };

  const handlePaste = (e: ClipboardEvent<HTMLInputElement>) => {
    const pasted = e.clipboardData.getData("text");
    if (pasted.trim()) {
      connectWithKey(pasted);
    }
  };

  const handleReset = () => {
    if (proxyMode === "tun") {
      tunDisconnect();
    } else {
      disconnect();
    }
    localStorage.removeItem(STORAGE_KEY);
    setKey("");
    setShowSetup(true);
  };

  const handleTrafficModeChange = (m: TrafficMode) => {
    if (proxyMode !== "tun") handleModeChange("tun");
    setTrafficMode(m);
    // AppRules only mounts in the Selected view, so when the user picks
    // "All" the component unmounts without pushing rules. Without this
    // explicit push the daemon stays on whatever the Selected tab last
    // sent — usually {proxy_only, [paths]} — and every non-listed app
    // (including Telegram) keeps getting routed direct.
    if (m === "all") {
      (window as any).tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
      (window as any).sysproxy?.setPacSites({ proxy_all: true });
      (window as any).sysproxy?.enable();
    }
  };

  const fmtSpeed = (b: number) => {
    if (b < 1024) return `${Math.round(b)} B/s`;
    if (b < 1048576) return `${(b / 1024).toFixed(1)} KB/s`;
    return `${(b / 1048576).toFixed(1)} MB/s`;
  };
  const fmtUptime = (s: number) => {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    const sec = s % 60;
    return [h, m, sec].map((v) => String(v).padStart(2, "0")).join(":");
  };

  // Color tokens
  const c = {
    bg0: "oklch(0.12 0.014 250)",
    bg1: "oklch(0.155 0.016 250)",
    bg2: "oklch(0.19 0.018 250)",
    bgh: "oklch(0.26 0.015 250)",
    b1: "oklch(0.24 0.013 250)",
    t1: "oklch(0.93 0.006 250)",
    t2: "oklch(0.60 0.012 250)",
    t3: "oklch(0.40 0.01 250)",
    am: "oklch(0.78 0.155 75)",
    amd: "oklch(0.55 0.09 75)",
    amb: "oklch(0.19 0.035 75)",
    bl: "oklch(0.68 0.12 235)",
    gn: "oklch(0.72 0.15 150)",
    rd: "oklch(0.62 0.19 25)",
    rdb: "oklch(0.17 0.03 25)",
  };
  const fd = "'Barlow Semi Condensed', 'Barlow', system-ui, sans-serif";
  const fb = "'Figtree', system-ui, sans-serif";
  const fm = "'Barlow', system-ui, sans-serif";

  const titleBtn = (children: React.ReactNode, onClick: () => void) => (
    <button
      onClick={onClick}
      style={{
        width: 26, height: 26, borderRadius: 5,
        background: "transparent", border: "none",
        color: c.t3, fontSize: 12, cursor: "pointer",
        display: "flex", alignItems: "center", justifyContent: "center",
        transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
      }}
      onMouseEnter={(e) => { e.currentTarget.style.background = c.bgh; e.currentTarget.style.color = c.t1; }}
      onMouseLeave={(e) => { e.currentTarget.style.background = "transparent"; e.currentTarget.style.color = c.t3; }}
    >
      {children}
    </button>
  );

  // Active-tab accent follows the connection state: amber when live,
  // muted medium grey when idle. Idle navigation stays visible but
  // subdued — same rule as the traffic-mode and transport-protocol
  // switches, keeping disconnected state visually quiet across the
  // whole mode row.
  const modeTabAccent = isConnected ? c.am : c.t2;
  const modeTabStyle = (active: boolean): React.CSSProperties => ({
    padding: "8px 16px",
    fontFamily: fd, fontSize: 13, fontWeight: active ? 600 : 500,
    letterSpacing: 0.3,
    color: active ? modeTabAccent : c.t3,
    cursor: "pointer",
    background: "transparent",
    border: "none",
    borderBottom: `2px solid ${active ? modeTabAccent : "transparent"}`,
    marginBottom: -1,
    transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
  });

  return (
    <div style={{ height: "100vh", display: "flex", flexDirection: "column" }}>
      {/* ===== TITLEBAR ===== */}
      <div
        style={{
          position: "fixed", top: 0, left: 0, right: 0, height: 36,
          display: "flex", alignItems: "center", justifyContent: "space-between",
          padding: "0 12px", zIndex: 100,
          background: "transparent",
          // @ts-ignore
          WebkitAppRegion: "drag",
        }}
      >
        <div style={{ fontFamily: fd, fontSize: 11, color: c.t3, fontWeight: 600, letterSpacing: 0.5, textTransform: "uppercase" as const }}>
          Proxyness {version && <span style={{ fontWeight: 400, textTransform: "none" as const, letterSpacing: 0 }}>v{version}</span>}
          {version && version.includes("beta") && (
            <span style={{
              fontSize: 9, fontWeight: 700, color: c.am,
              background: c.amb, borderRadius: 3,
              padding: "1px 5px", marginLeft: 6, letterSpacing: 1,
            }}>BETA</span>
          )}
        </div>
        {/* @ts-ignore */}
        <div style={{ display: "flex", gap: 2, WebkitAppRegion: "no-drag" }}>
          {titleBtn(<span style={{ fontSize: 16, marginTop: 2 }}>&minus;</span>, () => (window as any).appInfo?.minimizeWindow())}
          {titleBtn(<span style={{ fontSize: 12 }}>&#10005;</span>, () => (window as any).appInfo?.closeWindow())}
        </div>
      </div>

      {/* ===== VIDEO / STATUS + TABS (unified) ===== */}
      {!showSetup && (
        <div style={{
          position: "relative", overflow: "hidden",
          borderBottom: `1px solid ${c.b1}`, flexShrink: 0,
          background: c.bg1,
        }}>
          {/* Video background — covers both status and tabs */}
          <video
            ref={(el) => {
              if (!el) return;
              if (isConnected) { el.play().catch(() => {}); } else { el.pause(); }
            }}
            autoPlay loop muted playsInline
            style={{
              position: "absolute", inset: 0, width: "100%", height: "200%",
              objectFit: "cover", marginTop: -40,
              filter: isConnected
                ? "brightness(0.4) saturate(1.2)"
                : "brightness(0.3) saturate(0) grayscale(1)",
              transition: "filter 1s cubic-bezier(0.25,1,0.5,1)",
            }}
            src={earthBgUrl}
          />
          {/* Gradient overlay */}
          <div style={{
            position: "absolute", inset: 0,
            background: `linear-gradient(to bottom, oklch(0.12 0.014 250 / 0.3), oklch(0.12 0.014 250 / 0.85) 70%, oklch(0.12 0.014 250 / 0.95) 100%)`,
          }} />

          {/* Status content — Variant C: Big Left + Metrics Right.
              Height is natural (no fixed value) so the hero grows to
              fit the traffic-mode switch sub-row that lives below. */}
          <div
            key={isConnected ? "connected" : (reconnecting || daemonReconnecting) ? "reconnecting" : "disconnected"}
            style={{
              position: "relative", zIndex: 5,
              display: "flex", alignItems: "center",
              padding: "36px 24px 0", gap: 16,
            }}
          >
            {/* Ghost status icon — eye open/closed/blinking */}
            <svg width="28" height="34" viewBox="0 0 28 34" fill="none" style={{ flexShrink: 0, animation: "pn-blur-dot 0.3s cubic-bezier(0.25,1,0.5,1) 0.2s both" }}>
              {/* Body */}
              <path
                d="M14 1 C8 1 3 6.5 3 14 L3 27 L5.8 24.5 L8.6 27 L11.3 24.5 L14 27 L16.7 24.5 L19.4 27 L22.2 24.5 L25 27 L25 14 C25 6.5 20 1 14 1Z"
                fill={isConnected ? "oklch(0.26 0.02 250)" : "oklch(0.20 0.015 250)"}
              />
              {/* Body highlight */}
              <path
                d="M14 1 C18 1 22 3.5 23.5 8 L23.5 12 C21 10.5 18 9.5 14 9.5 C10 9.5 7 10.5 4.5 12 L4.5 8 C6 3.5 10 1 14 1Z"
                fill={isConnected ? "oklch(0.32 0.018 250)" : "oklch(0.24 0.014 250)"}
              />
              {(reconnecting || daemonReconnecting) ? (
                /* Blinking eye — reconnecting */
                <g style={{ transformOrigin: "14px 15px", animation: "pn-blink 1.8s ease-in-out infinite" }}>
                  <ellipse cx="14" cy="15" rx="5.5" ry="5.5" fill={c.am} />
                  <ellipse cx="15" cy="15.5" rx="2.2" ry="3" fill="oklch(0.14 0.02 250)" />
                  <circle cx="12.5" cy="13.2" r="1.1" fill="oklch(0.92 0.08 75)" />
                </g>
              ) : isConnected ? (
                /* Open eye — connected */
                <>
                  <ellipse cx="14" cy="15" rx="5.5" ry="5.5" fill={c.am} />
                  <ellipse cx="15" cy="15.5" rx="2.2" ry="3" fill="oklch(0.14 0.02 250)" />
                  <circle cx="12.5" cy="13.2" r="1.1" fill="oklch(0.92 0.08 75)" />
                </>
              ) : (
                /* Closed eye — disconnected */
                <path d="M8.5 15.5 Q14 18 19.5 15.5" stroke="oklch(0.38 0.010 250)" strokeWidth="2" fill="none" strokeLinecap="round" />
              )}
            </svg>

            {/* Left: status + server */}
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <span style={{
                fontFamily: fd, fontSize: 22, fontWeight: 700, letterSpacing: 0.3, lineHeight: 1,
                color: (reconnecting || daemonReconnecting) ? c.am : isConnected ? c.t1 : c.t3,
                animation: "pn-blur-heavy 0.5s cubic-bezier(0.25,1,0.5,1) 0.25s both",
              }}>
                {isConnected ? "Connected" : (reconnecting || daemonReconnecting) ? "Reconnecting" : "Disconnected"}
              </span>
              <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                {isConnected ? (
                  <>
                    <span style={{ fontFamily: fb, fontSize: 12, color: c.t3, animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                      {SERVER.replace(":443", "")}
                    </span>
                    <span style={{
                      fontFamily: fd, fontSize: 9, fontWeight: 600, letterSpacing: 1,
                      textTransform: "uppercase" as const,
                      color: c.amd, padding: "1px 5px", background: c.amb, borderRadius: 3,
                      animation: "pn-blur-badge 0.3s cubic-bezier(0.25,1,0.5,1) 0.4s both",
                    }}>
                      {activeTransport || (proxyMode === "socks5" ? "SOCKS5" : "UDP")}
                    </span>
                  </>
                ) : (reconnecting || daemonReconnecting) ? (
                  <span style={{ fontFamily: fb, fontSize: 12, color: c.t3, animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                    Restoring connection
                  </span>
                ) : helperError ? (
                  <span style={{ fontFamily: fb, fontSize: 12, color: "oklch(0.65 0.2 25)", animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                    Helper failed: {helperError}
                  </span>
                ) : currentError ? (
                  <span style={{ fontFamily: fb, fontSize: 12, color: "oklch(0.65 0.2 25)", animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                    {currentError}
                  </span>
                ) : (
                  <span style={{ fontFamily: fb, fontSize: 12, color: c.t3, animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                    Ready to connect
                  </span>
                )}
              </div>
            </div>

            {/* Right: metrics + button */}
            <div style={{ marginLeft: "auto", display: "flex", alignItems: "center", gap: 20 }}>
              {isConnected && (
                <>
                  <div style={{ display: "flex", flexDirection: "column", gap: 1, textAlign: "right" as const, animation: "pn-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) 0.35s both" }}>
                    <span style={{ fontFamily: fm, fontSize: 13, fontWeight: 600, fontVariantNumeric: "tabular-nums", color: c.gn }}>
                      ↓ {fmtSpeed(stats.download)}
                    </span>
                    <span style={{ fontFamily: fd, fontSize: 8, fontWeight: 500, color: c.t3, letterSpacing: 1, textTransform: "uppercase" as const }}>
                      Down
                    </span>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 1, textAlign: "right" as const, animation: "pn-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) 0.4s both" }}>
                    <span style={{ fontFamily: fm, fontSize: 13, fontWeight: 600, fontVariantNumeric: "tabular-nums", color: c.bl }}>
                      ↑ {fmtSpeed(stats.upload)}
                    </span>
                    <span style={{ fontFamily: fd, fontSize: 8, fontWeight: 500, color: c.t3, letterSpacing: 1, textTransform: "uppercase" as const }}>
                      Up
                    </span>
                  </div>
                  <div style={{ display: "flex", flexDirection: "column", gap: 1, textAlign: "right" as const, animation: "pn-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) 0.45s both" }}>
                    <span style={{ fontFamily: fm, fontSize: 13, fontWeight: 600, fontVariantNumeric: "tabular-nums", color: c.t2 }}>
                      {fmtUptime(uptime)}
                    </span>
                    <span style={{ fontFamily: fd, fontSize: 8, fontWeight: 500, color: c.t3, letterSpacing: 1, textTransform: "uppercase" as const }}>
                      Uptime
                    </span>
                  </div>
                </>
              )}
              <button
                onClick={() => {
                  if (isConnected || reconnecting || daemonReconnecting) {
                    if (proxyMode === "tun") tunDisconnect();
                    else disconnect();
                  } else if (key) {
                    if (proxyMode === "tun") tunConnect(SERVER, key);
                    else connect(SERVER, key);
                  }
                }}
                disabled={(isLoading && !reconnecting && !daemonReconnecting) || (!isConnected && !!helperError)}
                style={{
                  fontFamily: fd, fontWeight: 600, letterSpacing: 0.5,
                  borderRadius: 4, cursor: "pointer",
                  display: "flex", alignItems: "center", justifyContent: "center",
                  transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
                  animation: `pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) ${isConnected ? "0.5s" : "0.35s"} both`,
                  ...(isConnected || reconnecting || daemonReconnecting ? {
                    fontSize: 11, padding: "5px 14px", minWidth: 80,
                    background: c.rdb,
                    border: "1px solid oklch(0.62 0.19 25 / 0.15)",
                    color: c.rd,
                  } : !isConnected && helperError ? {
                    fontSize: 13, padding: "8px 24px", minWidth: 100,
                    background: c.bg2,
                    border: `1px solid ${c.b1}`,
                    color: c.t3,
                    cursor: "not-allowed",
                  } : {
                    fontSize: 13, padding: "8px 24px", minWidth: 100,
                    background: c.amb,
                    border: "1px solid oklch(0.78 0.155 75 / 0.2)",
                    color: c.am,
                  }),
                  opacity: (isLoading && !reconnecting && !daemonReconnecting) ? 0.5 : 1,
                }}
              >
                {(reconnecting || daemonReconnecting) ? "Cancel" : isConnected ? "Disconnect" : isLoading ? (
                  <div style={{
                    width: 14, height: 14, borderRadius: "50%",
                    border: `2px solid oklch(0.78 0.155 75 / 0.3)`,
                    borderTopColor: c.am,
                    animation: "pn-spin 0.7s linear infinite",
                  }} />
                ) : "Connect"}
              </button>
            </div>
          </div>

          {/* Traffic-mode sub-row — lives in its own container below the
              status row so the upper row stays vertically centered
              within its own box. Indented past the status dot so the
              pill aligns with the "Disconnected / Ready to connect"
              text above it. Reflects trafficMode regardless of page. */}
          <div style={{
            position: "relative", zIndex: 5,
            padding: "10px 24px 14px 68px",
          }}>
            <div style={{
              display: "inline-flex",
              padding: 3,
              borderRadius: 6,
              background: `oklch(0.155 0.016 250 / 0.6)`,
              border: `1px solid ${c.b1}`,
              animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.5s both",
            }}>
              {(["all", "selected"] as TrafficMode[]).map((m) => {
                const active = trafficMode === m;
                // When the proxy is connected, light up the active segment
                // in amber to match the "live" visual language (status
                // badge, Connect button ready state). When idle, use a
                // muted mid-grey foreground so the switch stays visible
                // but doesn't scream — matches mode tabs + transport
                // selector for a consistent "quiet when off" rule.
                const activeColor = isConnected ? c.am : c.t2;
                const activeBg = isConnected ? c.amb : c.bg2;
                return (
                  <button
                    key={m}
                    onClick={() => { handleTrafficModeChange(m); setActiveTab("main"); }}
                    style={{
                      padding: "4px 12px",
                      fontFamily: fd, fontSize: 11, fontWeight: active ? 600 : 500,
                      letterSpacing: 0.3,
                      color: active ? activeColor : c.t3,
                      cursor: "pointer",
                      background: active ? activeBg : "transparent",
                      border: "none",
                      borderRadius: 4,
                      transition: "all 0.12s cubic-bezier(0.25,1,0.5,1)",
                    }}
                    onMouseEnter={(e) => { if (!active) e.currentTarget.style.color = c.t2; }}
                    onMouseLeave={(e) => { if (!active) e.currentTarget.style.color = c.t3; }}
                  >
                    {m === "all" ? "All traffic" : "Selected"}
                  </button>
                );
              })}
            </div>
          </div>

          {/* Page tabs — Main ↔ Settings. Traffic mode lives in the
              sub-row above; this row is purely for page navigation. */}
          <div style={{
            position: "relative", zIndex: 5,
            display: "flex", alignItems: "center", gap: 0,
            padding: "0 24px",
            borderTop: `1px solid oklch(0.24 0.013 250 / 0.5)`,
          }}>
            <button
              onClick={() => setActiveTab("main")}
              style={modeTabStyle(activeTab === "main")}
              onMouseEnter={(e) => { if (activeTab !== "main") e.currentTarget.style.color = c.t2; }}
              onMouseLeave={(e) => { if (activeTab !== "main") e.currentTarget.style.color = c.t3; }}
            >
              Main
            </button>
            <div style={{ flex: 1 }} />
            <button
              onClick={() => setActiveTab("settings")}
              style={{
                ...modeTabStyle(activeTab === "settings"),
                display: "flex", alignItems: "center", gap: 4,
              }}
              onMouseEnter={(e) => { if (activeTab !== "settings") e.currentTarget.style.color = c.t2; }}
              onMouseLeave={(e) => { if (activeTab !== "settings") e.currentTarget.style.color = c.t3; }}
            >
              <span style={{ fontSize: 15, lineHeight: 1 }}>&#9881;</span>
              Settings
            </button>
          </div>
        </div>
      )}

      {/* ===== NOTIFICATION BANNER ===== */}
      {!showSetup && (
        <div style={{ padding: "20px 24px 0" }}>
          <NotificationBanner />
        </div>
      )}

      {/* ===== CONTENT ===== */}
      {showSetup ? (
        <div style={{ flex: 1, position: "relative", overflow: "hidden" }}>
          {/* Video background */}
          <video
            autoPlay loop muted playsInline
            style={{
              position: "absolute", inset: 0, width: "100%", height: "100%",
              objectFit: "cover",
              animation: "pn-blur-bg 0.9s cubic-bezier(0.25,1,0.5,1) both",
            }}
            src={earthBgUrl}
          />
          {/* Gradient: solid left → transparent right */}
          <div style={{
            position: "absolute", inset: 0, zIndex: 1,
            background: `linear-gradient(to right, oklch(0.12 0.014 250 / 0.92) 45%, oklch(0.12 0.014 250 / 0.3) 100%)`,
          }} />
          {/* Form content */}
          <div style={{
            position: "relative", zIndex: 5, height: "100%",
            display: "flex", flexDirection: "column", justifyContent: "center",
            padding: "0 0 0 64px",
          }}>
            <div style={{ fontFamily: fd, fontSize: 42, fontWeight: 300, color: c.t1, letterSpacing: 3, textTransform: "uppercase" as const, lineHeight: 1, marginBottom: 4, animation: "pn-blur-heavy 0.7s cubic-bezier(0.25,1,0.5,1) 0.15s both" }}>
              Proxyness
            </div>
            <div style={{ fontFamily: fd, fontSize: 14, fontWeight: 600, color: c.amd, marginBottom: 48, letterSpacing: 4, textTransform: "uppercase" as const, animation: "pn-blur-med 0.6s cubic-bezier(0.25,1,0.5,1) 0.3s both" }}>
              Secure system-level proxy<br />for apps and browsers
            </div>
            <div style={{ maxWidth: 320 }}>
              <div style={{ fontFamily: fd, fontSize: 10, fontWeight: 600, color: c.t3, letterSpacing: 1.5, textTransform: "uppercase" as const, marginBottom: 8, animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.45s both" }}>
                Access Key
              </div>
              <input
                style={{
                  width: "100%", padding: "10px 14px",
                  background: "oklch(0.155 0.016 250 / 0.7)",
                  backdropFilter: "blur(6px)", WebkitBackdropFilter: "blur(6px)",
                  border: `1px solid ${keyError ? "oklch(0.65 0.2 25)" : c.b1}`, borderRadius: 5,
                  color: c.t1, fontFamily: fb, fontSize: 14,
                  outline: "none", transition: "border-color 0.15s",
                  animation: "pn-blur-light 0.5s cubic-bezier(0.25,1,0.5,1) 0.5s both",
                }}
                type="password"
                value={key}
                onChange={(e) => { setKey(e.target.value); setKeyError(""); }}
                placeholder="Paste your access key"
                onPaste={handlePaste}
                onKeyDown={(e) => e.key === "Enter" && !keyValidating && connectWithKey(key)}
                onFocus={(e) => { e.currentTarget.style.borderColor = keyError ? "oklch(0.65 0.2 25)" : c.am; }}
                onBlur={(e) => { e.currentTarget.style.borderColor = keyError ? "oklch(0.65 0.2 25)" : c.b1; }}
              />
              <div style={{ fontFamily: fb, fontSize: 11, color: keyError ? "oklch(0.65 0.2 25)" : c.t3, marginTop: 10, animation: "pn-blur-fade 0.4s cubic-bezier(0.25,1,0.5,1) 0.6s both" }}>
                {keyValidating ? "Checking key..." : keyError || "Paste your access key and press Enter"}
              </div>
            </div>
          </div>
        </div>
      ) : activeTab === "settings" ? (
        <SettingsPage
          version={version}
          transportMode={transportMode}
          onTransportChange={handleTransportChange}
          onChangeKey={handleReset}
          onHelperError={setHelperError}
          isConnected={isConnected}
          serverId={serverId}
          servers={SERVERS}
          onServerChange={handleServerChange}
          c={c} fd={fd} fb={fb} fm={fm}
        />
      ) : (
        <div key={trafficMode} style={{ flex: 1, overflowY: "auto", minHeight: 0, padding: "16px 24px" }}>
          {trafficMode === "all" && (
            <div style={{ paddingTop: 24 }}>
              <div style={{ fontFamily: fd, fontSize: 15, fontWeight: 600, color: c.t2, letterSpacing: 0.3, marginBottom: 4, animation: "pn-blur-heavy 0.5s cubic-bezier(0.25,1,0.5,1) 0.05s both" }}>
                All system traffic routed through proxy
              </div>
              <div style={{ fontFamily: fb, fontSize: 13, color: c.t3, lineHeight: 1.6, animation: "pn-blur-light 0.4s cubic-bezier(0.25,1,0.5,1) 0.15s both" }}>
                Every connection from this device goes through the server.<br />
                Switch to Selected to choose specific apps and sites.
              </div>
            </div>
          )}
          {trafficMode === "selected" && (
            <div style={{ animation: "pn-blur-fade 0.3s cubic-bezier(0.25,1,0.5,1) both" }}>
              <AppRules visible mode="selected" onModeChange={setTrafficMode} hideModeSwitch isConnected={isConnected} />
            </div>
          )}
        </div>
      )}
    </div>
  );
}
