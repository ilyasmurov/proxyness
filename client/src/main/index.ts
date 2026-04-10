import { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain, shell, powerMonitor, net, Notification } from "electron";
import { spawn } from "child_process";
import path from "path";
import fs from "fs";
import { startDaemon, stopDaemon, startHelper, stopHelper, getLogs, clearLogs, waitForDaemonReady } from "./daemon";
import { enableSystemProxy, disableSystemProxy } from "./sysproxy";
import { getInstalledApps } from "./apps";
import { getDaemonToken, cachedDaemonToken } from "./extension";

// Diagnostic crash logger — opt-in, off by default. Enable by launching
// the app with `--debug` or setting SMUROV_DEBUG=1 in the environment.
// When active, writes unhandled main-process exceptions, renderer crashes
// and per-phase boot traces to ~/Desktop/smurov-crash.log so we can debug
// startup/runtime failures that leave no trace in Event Viewer or
// %APPDATA% logs. Kept around permanently so future reports can be
// triaged by asking the user to relaunch with the flag.
// Opt-in crash logger. Enable by launching with `--trace` (Electron
// reserves --debug for its legacy Node inspector, so we can't use that
// name). SMUROV_DEBUG=1 env var works on macOS but NOT on Windows,
// because requireAdministrator elevation drops env vars on the
// elevated child — so on Windows the only reliable switch is --trace.
const DEBUG_ENABLED = process.argv.includes("--trace") || process.env.SMUROV_DEBUG === "1";
const CRASH_LOG = path.join(require("os").homedir(), "Desktop", "smurov-crash.log");
function logCrash(tag: string, err: unknown) {
  if (!DEBUG_ENABLED) return;
  try {
    const stamp = new Date().toISOString();
    const msg = err instanceof Error ? `${err.message}\n${err.stack}` : String(err);
    fs.appendFileSync(CRASH_LOG, `[${stamp}] ${tag}\n${msg}\n\n`);
  } catch {
    // best-effort: if we can't even write the log, there's nothing else to do
  }
}
function bootTrace(step: string) {
  if (!DEBUG_ENABLED) return;
  try {
    const stamp = new Date().toISOString();
    fs.appendFileSync(CRASH_LOG, `[${stamp}] boot: ${step}\n`);
  } catch {}
}
if (DEBUG_ENABLED) {
  process.on("uncaughtException", (err) => logCrash("uncaughtException", err));
  process.on("unhandledRejection", (reason) => logCrash("unhandledRejection", reason));
  bootTrace("process started (debug mode)");
}

const UPDATE_BASE = "https://github.com/ilyasmurov/smurov-proxy/releases/latest/download";
const DEFAULT_CONFIG_URL = "https://95.181.162.242/api/client-config";

interface ServerNotification {
  id: string;
  type: "update" | "migration" | "maintenance" | "info";
  title: string;
  message?: string;
  action?: { label: string; type: string; url?: string; server?: string };
  created_at: string;
}

interface CachedConfig {
  config_url: string;
  proxy_server: string;
  relay_url: string;
  notifications: ServerNotification[];
  fetched_at: number;
}

let cachedConfig: CachedConfig | null = null;

function configCachePath(): string {
  return path.join(app.getPath("userData"), "config-cache.json");
}

function readConfigCache(): CachedConfig | null {
  try {
    return JSON.parse(fs.readFileSync(configCachePath(), "utf-8"));
  } catch {
    return null;
  }
}

function writeConfigCache(cfg: CachedConfig) {
  try {
    fs.writeFileSync(configCachePath(), JSON.stringify(cfg));
  } catch (err) {
    console.error("[config] cache write error:", err);
  }
}

let mainWindow: BrowserWindow | null = null;
let logsWindow: BrowserWindow | null = null;
let updateWindow: BrowserWindow | null = null;
let loaderWindow: BrowserWindow | null = null;
let tray: Tray | null = null;

// mainBootCompleted guards the one-time setup that happens after the first
// successful boot (createWindow / createTray / setupIpc / poller). If the
// user retries from the loader after success this would re-register IPC
// handlers and start a duplicate poller, so we early-return when the flag
// is set.
let mainBootCompleted = false;

// systemProxyActive tracks whether the user wants the macOS/Windows
// system PAC proxy turned on. We need this so the daemon-cache poller
// (started below) only re-runs enableSystemProxy() — which bumps the
// cache-busting timestamp on the PAC URL — when it's actually wanted,
// avoiding accidentally re-enabling system proxy after the user disabled it.
let systemProxyActive = false;
let lastSitesSnapshot = "";

// refreshSystemProxyIfActive forces Chrome/macOS to re-fetch the PAC by
// calling enableSystemProxy() (which bumps the cache-busting `?t=...` query
// param). Called after every site mutation we know about so PAC content
// changes propagate immediately.
function refreshSystemProxyIfActive() {
  if (systemProxyActive) {
    enableSystemProxy();
  }
}

// startSitesCachePoller polls daemon /sites/my every 500ms and triggers
// a system-proxy refresh whenever the cache snapshot changes. This catches
// mutations that bypass main process IPC — namely the browser-extension
// popup's add/toggle/remove flows, which go popup → service-worker → daemon
// HTTP without ever touching the desktop client.
//
// 500ms was picked as the sweet spot between responsiveness and load. The
// fetch hits the local loopback daemon so round-trip cost is negligible;
// the user-perceived delay from "site added via popup" to "Chrome sees
// fresh PAC" is now poller-tick (≤500ms) + macOS notify (~100ms) + Chrome
// PAC refetch (~50ms) ≈ 650ms worst case, down from ~2.5s before.
function startSitesCachePoller() {
  setInterval(async () => {
    try {
      const r = await fetch("http://127.0.0.1:9090/sites/my");
      if (!r.ok) return;
      const json = await r.text();
      if (json !== lastSitesSnapshot) {
        const previous = lastSitesSnapshot;
        lastSitesSnapshot = json;
        // Don't refresh on the very first observation — that's just the
        // poller learning the initial state, not a real change.
        if (previous !== "") {
          refreshSystemProxyIfActive();
          mainWindow?.webContents.send("daemon-sites-changed");
        }
      }
    } catch {
      // daemon not reachable — leave snapshot as-is, next tick will retry
    }
  }, 500);
}

// MIN_LOADER_VISIBLE_MS guarantees the loader is on screen long enough for
// the user to actually perceive it, even when the daemon is already warm
// and /health responds in single-digit milliseconds. Without this floor the
// window flashes invisibly between create and destroy.
const MIN_LOADER_VISIBLE_MS = 600;

// loaderShownAt is set the moment the loader window's contents finish loading
// (ready-to-show fires). bootMainApp uses it to compute the elapsed visible
// time and pad with a delay before destroying.
let loaderShownAt = 0;

// createLoaderWindow shows the small splash window that the user sees while
// the daemon and helper are starting. It's frameless, non-resizable, has no
// system close/min/max buttons (closable: false) — only the custom × button
// inside loader.html can dismiss it, and that route runs the full quit flow
// via the loader-quit IPC handler. Bringing the window up before runBoot()
// means the user always sees feedback during the boot wait, even on the
// very first cold start when the daemon takes ~1s to bind its listener.
//
// Returns a Promise that resolves when ready-to-show fires. bootMainApp
// awaits this so we never call destroyLoaderWindow() against a window that
// hasn't actually rendered yet. Falls back via a 2s safety timer in case
// ready-to-show never fires (e.g. loader.html failed to load).
function createLoaderWindow(): Promise<void> {
  loaderWindow = new BrowserWindow({
    width: 440,
    height: 150,
    resizable: false,
    frame: false,
    movable: true,
    minimizable: false,
    maximizable: false,
    fullscreenable: false,
    closable: false,
    backgroundColor: "#1a1a2e",
    show: false,
    center: true,
    skipTaskbar: false,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: path.join(__dirname, "preload-loader.js"),
    },
  });

  if (process.env.VITE_DEV_SERVER_URL) {
    loaderWindow.loadURL(`${process.env.VITE_DEV_SERVER_URL}loader.html`);
  } else {
    loaderWindow.loadFile(path.join(__dirname, "../../dist/loader.html"));
  }

  // closable:false disables the system close button, but Cmd+W on macOS
  // can still fire a close event — intercept it so the only way out is the
  // custom × button (which goes through destroyLoaderWindow → app.quit).
  // destroyLoaderWindow() calls .destroy() which bypasses this handler.
  loaderWindow.on("close", (e) => {
    if (loaderWindow) e.preventDefault();
  });
  loaderWindow.on("closed", () => {
    loaderWindow = null;
  });

  return new Promise<void>((resolve) => {
    let resolved = false;
    const done = () => {
      if (resolved) return;
      resolved = true;
      loaderShownAt = Date.now();
      loaderWindow?.show();
      resolve();
    };
    loaderWindow!.once("ready-to-show", done);
    // Safety net: if ready-to-show somehow never fires (loader.html broken,
    // dev server unreachable, etc.) don't block the boot forever.
    setTimeout(done, 2000);
  });
}

function sendLoaderStatus(phase: "daemon" | "helper" | "ready" | "error", message: string) {
  loaderWindow?.webContents.send("loader-status", { phase, message });
}

function destroyLoaderWindow() {
  // destroy() bypasses any close handlers and is safe even on a window with
  // closable: false — we use it instead of close() because closable: false
  // makes close() a no-op.
  if (loaderWindow) {
    loaderWindow.destroy();
    loaderWindow = null;
  }
}

// runBoot is the actual boot sequence. It pushes status updates into the
// loader window between each phase and returns false on the first hard
// failure (currently: daemon /health never responds within waitForDaemonReady's
// 5s budget). On false, the loader sticks around with the error state and
// the user can click "Попробовать снова" to retry.
async function runBoot(): Promise<boolean> {
  sendLoaderStatus("daemon", "Starting daemon...");
  startDaemon();
  const ready = await waitForDaemonReady();
  if (!ready) {
    sendLoaderStatus("error", "Daemon failed to start");
    return false;
  }
  // On packaged macOS, the helper is a launchd service installed by the PKG —
  // startHelper() is a no-op there, so we don't show a "Starting helper" line
  // that would be a lie.
  const helperManaged = process.platform === "darwin" && app.isPackaged;
  if (!helperManaged) {
    sendLoaderStatus("helper", "Starting helper...");
  }
  startHelper();
  sendLoaderStatus("ready", "Ready");
  return true;
}

// bootMainApp orchestrates the boot sequence and the one-time post-boot
// setup. Idempotent via mainBootCompleted: if a retry path lands here after
// the main window is already up (shouldn't happen, but defensive) it bails.
async function bootMainApp() {
  if (mainBootCompleted) return;
  bootTrace("bootMainApp begin");
  const ok = await runBoot();
  bootTrace(`runBoot returned ${ok}`);
  if (!ok) return; // loader keeps showing the error + retry button
  // If the daemon was already warm and runBoot finished in milliseconds, the
  // loader would flash invisibly — pad the visible time so the user actually
  // perceives the splash. loaderShownAt is 0 if ready-to-show never fired,
  // in which case the whole elapsed check is a no-op (elapsed is huge).
  const elapsed = Date.now() - loaderShownAt;
  if (loaderShownAt > 0 && elapsed < MIN_LOADER_VISIBLE_MS) {
    await new Promise((r) => setTimeout(r, MIN_LOADER_VISIBLE_MS - elapsed));
  }
  // CRITICAL: create the main window BEFORE destroying the loader.
  // destroyLoaderWindow() calls BrowserWindow.destroy() which fires
  // window-all-closed synchronously if it was the only window, and on
  // Windows that handler calls app.quit() — so if we destroy first and
  // createWindow second, we end up calling createWindow() inside a
  // quitting app and the new window is closed immediately. Overlapping
  // the two (create main, then destroy loader) keeps the window count
  // above zero across the transition. See 1.27+ Windows crash.
  bootTrace("createWindow");
  createWindow();
  bootTrace("destroyLoaderWindow");
  destroyLoaderWindow();
  bootTrace("createTray");
  createTray();
  bootTrace("setupIpc");
  setupIpc();
  bootTrace("startSitesCachePoller");
  startSitesCachePoller();
  mainBootCompleted = true;
  bootTrace("bootMainApp done");
}

// setupLoaderIpc wires the two IPC channels the loader window can send
// to the main process. Must be called before createLoaderWindow so the
// handlers exist by the time the user can click anything.
function setupLoaderIpc() {
  ipcMain.on("loader-retry", async () => {
    // Tear down whatever half-started state the previous attempt left and
    // give the OS a beat to actually reap the processes before we spawn
    // fresh — without the wait, startDaemon() could race against the dying
    // process's port binding.
    stopDaemon();
    stopHelper();
    sendLoaderStatus("daemon", "Restarting...");
    await new Promise((r) => setTimeout(r, 300));
    await bootMainApp();
  });

  ipcMain.on("loader-quit", () => {
    // Full quit: kill daemon + helper, mark quitting so the main close
    // handler doesn't intercept (no main window exists yet anyway) and
    // exit the app.
    destroyLoaderWindow();
    stopDaemon();
    stopHelper();
    (app as any).isQuitting = true;
    app.quit();
  });
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 840,
    height: 580,
    resizable: false,
    frame: false,
    transparent: true,
    hasShadow: false,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: path.join(__dirname, "preload.js"),
    },
  });

  // Renderer diagnostics — everything that can go wrong on the renderer side
  // gets captured here because main-process-only logging missed the 1.27+
  // Windows startup crash entirely (main finished boot cleanly, window died).
  mainWindow.webContents.on("render-process-gone", (_e, details) => {
    logCrash("render-process-gone", JSON.stringify(details));
  });
  mainWindow.webContents.on("did-fail-load", (_e, code, desc, url) => {
    logCrash("did-fail-load", `${code} ${desc} ${url}`);
  });
  mainWindow.webContents.on("preload-error", (_e, preload, err) => {
    logCrash("preload-error", `${preload}: ${err?.stack || err}`);
  });
  mainWindow.webContents.on("console-message", (_e, level, message, line, sourceId) => {
    bootTrace(`renderer console[${level}] ${sourceId}:${line} ${message}`);
  });
  mainWindow.webContents.on("did-start-loading", () => bootTrace("wc did-start-loading"));
  mainWindow.webContents.on("did-finish-load", () => bootTrace("wc did-finish-load"));
  mainWindow.webContents.on("dom-ready", () => bootTrace("wc dom-ready"));
  mainWindow.webContents.on("unresponsive", () => bootTrace("wc unresponsive"));
  mainWindow.webContents.on("crashed" as any, (_e: any, killed: boolean) => bootTrace(`wc crashed killed=${killed}`));
  mainWindow.on("closed", () => bootTrace("mainWindow closed event"));
  mainWindow.on("show", () => bootTrace("mainWindow show event"));
  mainWindow.on("ready-to-show", () => bootTrace("mainWindow ready-to-show"));

  // Refresh config when the user returns to the window.
  mainWindow.on("show", () => { pollConfig(); });
  mainWindow.on("focus", () => { pollConfig(); });

  if (process.env.VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(process.env.VITE_DEV_SERVER_URL);
  } else {
    mainWindow.loadFile(path.join(__dirname, "../../dist/index.html"));
  }

  // Minimize to tray on close (both platforms)
  mainWindow.on("close", (e) => {
    if (mainWindow && !(app as any).isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    }
  });
}

let trayConnected = false;

function trayIconPath(connected: boolean): string {
  const buildDir = app.isPackaged
    ? path.join(process.resourcesPath, "app.asar", "build")
    : path.join(__dirname, "../../build");

  if (process.platform === "darwin") {
    return path.join(buildDir, connected ? "trayConnectedTemplate.png" : "trayTemplate.png");
  }
  return path.join(buildDir, connected ? "trayConnected.png" : "tray.png");
}

function loadTrayIcon(connected: boolean): Electron.NativeImage {
  const icon = nativeImage.createFromPath(trayIconPath(connected));
  if (process.platform === "darwin") icon.setTemplateImage(true);
  return icon;
}

function updateTrayMenu() {
  if (!tray) return;
  const connectLabel = trayConnected ? "Disconnect" : "Connect";
  const contextMenu = Menu.buildFromTemplate([
    {
      label: "Show",
      click: () => mainWindow?.show(),
    },
    { type: "separator" },
    {
      label: connectLabel,
      click: () => {
        mainWindow?.webContents.send(trayConnected ? "tray-disconnect" : "tray-connect");
      },
    },
    { type: "separator" },
    {
      label: "Quit",
      click: () => {
        (app as any).isQuitting = true;
        app.quit();
      },
    },
  ]);
  tray.setContextMenu(contextMenu);
}

function setTrayConnected(connected: boolean) {
  if (trayConnected === connected) return;
  trayConnected = connected;
  tray?.setImage(loadTrayIcon(connected));
  updateTrayMenu();
}

function createTray() {
  tray = new Tray(loadTrayIcon(false));
  tray.setToolTip("SmurovProxy");
  updateTrayMenu();

  tray.on("double-click", () => {
    mainWindow?.show();
  });
}

let installerPath = "";

// Uses Electron's net.fetch (Chromium network stack) instead of the Node.js
// built-in fetch so that the request goes through the system proxy / PAC
// script. SmurovProxy enables a system proxy in TUN mode, so the update
// check is routed through the VPN — otherwise on Windows the direct fetch
// to github.com fails because GitHub is blocked at the ISP level in Russia
// and Node's undici fetch ignores the system proxy.
async function fetchYml(): Promise<{ version: string; filename: string } | null> {
  const ymlFile = process.platform === "darwin" ? "latest-mac.yml" : "latest.yml";
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 15000);
  try {
    const res = await net.fetch(`${UPDATE_BASE}/${ymlFile}`, {
      signal: controller.signal,
      redirect: "follow",
    });
    clearTimeout(timeout);
    if (!res.ok) {
      console.error(`[update] fetch ${ymlFile} returned ${res.status}`);
      return null;
    }
    const text = await res.text();
    const ver = text.match(/^version:\s*(.+)$/m);
    const p = text.match(/^path:\s*(.+)$/m);
    if (!ver || !p) {
      console.error(`[update] failed to parse ${ymlFile}: ${text.slice(0, 200)}`);
      return null;
    }
    return { version: ver[1].trim(), filename: p[1].trim() };
  } catch (err) {
    clearTimeout(timeout);
    console.error(`[update] fetch ${ymlFile} failed:`, err);
    return null;
  }
}

function isNewer(latest: string, current: string): boolean {
  const l = latest.replace(/^v/, "").split(".").map(Number);
  const c = current.replace(/^v/, "").split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if ((l[i] || 0) > (c[i] || 0)) return true;
    if ((l[i] || 0) < (c[i] || 0)) return false;
  }
  return false;
}

function sendUpdate(channel: string, ...args: any[]) {
  mainWindow?.webContents.send(channel, ...args);
  updateWindow?.webContents.send(channel, ...args);
}

// Throttle for config polling so rapid window show/focus events don't
// hammer the server. The interval-based check runs every 30 min.
let lastConfigPollAt = 0;
const CONFIG_POLL_MIN_INTERVAL_MS = 30_000;

// Background config poller — replaces direct GitHub polling.
// Fetches /api/client-config from the config service (through proxy),
// caches to disk, and pushes notifications to all windows.
async function pollConfig() {
  const now = Date.now();
  if (now - lastConfigPollAt < CONFIG_POLL_MIN_INTERVAL_MS) return;
  lastConfigPollAt = now;

  const configUrl = cachedConfig?.config_url || DEFAULT_CONFIG_URL;
  // Read the stored key from the renderer's localStorage isn't possible
  // from main — so we read it from the config cache or skip if no key.
  // The key is passed via the "store-key" IPC from renderer on connect.
  if (!storedDeviceKey) return;

  try {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 15000);
    const res = await net.fetch(
      `${configUrl}?key=${encodeURIComponent(storedDeviceKey)}&v=${app.getVersion()}`,
      { signal: controller.signal, redirect: "follow" },
    );
    clearTimeout(timeout);
    if (!res.ok) return;

    const data = (await res.json()) as CachedConfig;
    data.fetched_at = now;
    cachedConfig = data;
    writeConfigCache(data);
    sendUpdate("config-updated", data);

    // OS push notification for new server notifications
    for (const n of data.notifications || []) {
      if (shownNotificationIds.has(n.id)) continue;
      shownNotificationIds.add(n.id);
      if (Notification.isSupported()) {
        new Notification({ title: n.title, body: n.message || "", silent: false }).show();
      }
    }
  } catch (err) {
    console.error("[config] poll failed:", err);
  }
}

let storedDeviceKey = "";
const shownNotificationIds = new Set<string>();

function setupIpc() {
  ipcMain.handle("get-config", () => cachedConfig);

  // Manual update check (update window + settings menu)
  ipcMain.handle("check-update-version", async () => {
    try {
      const info = await fetchYml();
      if (!info) return { hasUpdate: false, latestVersion: null, error: true };
      return {
        hasUpdate: isNewer(info.version, app.getVersion()),
        latestVersion: info.version,
      };
    } catch {
      return { hasUpdate: false, latestVersion: null, error: true };
    }
  });

  // Renderer stores the device key so main process can poll config
  ipcMain.on("store-key", (_e, key: string) => {
    storedDeviceKey = key;
    pollConfig(); // immediate first poll when key is available
  });

  ipcMain.on("download-update", async () => {
    // Uses net.fetch for the same reason as fetchYml: Node's https doesn't
    // respect the system proxy, so direct download from github.com would
    // fail on Windows where GitHub is blocked at the ISP level.
    let file: fs.WriteStream | null = null;
    try {
      const info = await fetchYml();
      if (!info) {
        sendUpdate("update-error");
        return;
      }

      const dest = path.join(app.getPath("temp"), info.filename);
      file = fs.createWriteStream(dest);

      const res = await net.fetch(`${UPDATE_BASE}/${info.filename}`, {
        redirect: "follow",
      });
      if (!res.ok || !res.body) {
        console.error(`[update] download returned ${res.status}`);
        file.close();
        sendUpdate("update-error");
        return;
      }

      const total = parseInt(res.headers.get("content-length") || "0", 10);
      let downloaded = 0;
      let lastPercent = 0;

      const reader = res.body.getReader();
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        if (value) {
          file.write(Buffer.from(value));
          downloaded += value.length;
          if (total > 0) {
            const percent = Math.round((downloaded / total) * 100);
            if (percent !== lastPercent) {
              lastPercent = percent;
              sendUpdate("update-progress", percent);
            }
          } else {
            sendUpdate("update-progress", -downloaded);
          }
        }
      }

      const f = file;
      f.end(() => {
        installerPath = dest;
        sendUpdate("update-downloaded");
      });
    } catch (err) {
      console.error(`[update] download failed:`, err);
      if (file) file.close();
      sendUpdate("update-error");
    }
  });

  ipcMain.on("install-update", () => {
    if (installerPath) {
      stopDaemon();
      stopHelper();
      // Open PKG installer (macOS) or run exe (Windows)
      if (process.platform === "darwin") {
        spawn("open", [installerPath], { detached: true, stdio: "ignore" }).unref();
        app.exit(0);
      } else {
        // Windows: delay spawn to let child processes release file locks
        setTimeout(() => {
          spawn(installerPath, [], { detached: true, stdio: "ignore" }).unref();
          app.exit(0);
        }, 1000);
      }
    }
  });

  ipcMain.handle("get-version", () => app.getVersion());

  ipcMain.on("window-close", () => {
    mainWindow?.hide();
  });
  ipcMain.on("window-minimize", () => {
    mainWindow?.minimize();
  });
  ipcMain.handle("get-logs", () => getLogs());
  ipcMain.handle("clear-logs", () => clearLogs());

  ipcMain.on("open-logs", () => {
    if (logsWindow) {
      logsWindow.focus();
      return;
    }
    logsWindow = new BrowserWindow({
      width: 600,
      height: 400,
      minWidth: 400,
      minHeight: 200,
      title: "SmurovProxy — Logs",
      backgroundColor: "#1a1a2e",
      autoHideMenuBar: true,
      webPreferences: {
        nodeIntegration: false,
        contextIsolation: true,
        preload: path.join(__dirname, "preload-logs.js"),
      },
    });

    if (process.env.VITE_DEV_SERVER_URL) {
      logsWindow.loadURL(`${process.env.VITE_DEV_SERVER_URL}logs.html`);
    } else {
      logsWindow.loadFile(path.join(__dirname, "../../dist/logs.html"));
    }

    logsWindow.on("closed", () => {
      logsWindow = null;
    });
  });

  ipcMain.on("open-update", () => {
    if (updateWindow) {
      updateWindow.focus();
      return;
    }
    updateWindow = new BrowserWindow({
      width: 360,
      height: 200,
      resizable: false,
      title: "SmurovProxy — Updates",
      backgroundColor: "#1a1a2e",
      autoHideMenuBar: true,
      webPreferences: {
        nodeIntegration: false,
        contextIsolation: true,
        preload: path.join(__dirname, "preload-update.js"),
      },
    });

    if (process.env.VITE_DEV_SERVER_URL) {
      updateWindow.loadURL(`${process.env.VITE_DEV_SERVER_URL}update.html`);
    } else {
      updateWindow.loadFile(path.join(__dirname, "../../dist/update.html"));
    }

    updateWindow.on("closed", () => {
      updateWindow = null;
    });
  });

  ipcMain.on("enable-proxy", () => {
    systemProxyActive = true;
    enableSystemProxy();
  });

  ipcMain.on("disable-proxy", () => {
    systemProxyActive = false;
    disableSystemProxy();
  });

  ipcMain.on("pac-sites", (_e, data: { proxy_all: boolean }) => {
    // The `sites` field is no longer accepted — daemon owns the domain list.
    fetch("http://127.0.0.1:9090/pac/sites", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ proxy_all: data.proxy_all }),
    })
      .then(() => enableSystemProxy())
      .catch(() => {});
  });

  ipcMain.handle("daemon-set-enabled", async (_e, siteId: number, enabled: boolean) => {
    const token = cachedDaemonToken();
    if (!token) throw new Error("daemon token unavailable — restart daemon");
    const r = await fetch("http://127.0.0.1:9090/sites/set-enabled", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ site_id: siteId, enabled }),
    });
    if (!r.ok) throw new Error(`daemon ${r.status}`);
    const json = await r.json(); // { ok: true, my_sites: [...] }
    refreshSystemProxyIfActive();
    return json;
  });

  ipcMain.handle("daemon-add-site", async (_e, primaryDomain: string, label: string) => {
    const token = cachedDaemonToken();
    if (!token) throw new Error("daemon token unavailable — restart daemon");
    const r = await fetch("http://127.0.0.1:9090/sites/add", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ primary_domain: primaryDomain, label }),
    });
    if (!r.ok) throw new Error(`daemon ${r.status}`);
    const json = await r.json(); // { site_id, deduped }
    refreshSystemProxyIfActive();
    return json;
  });

  ipcMain.handle("daemon-remove-site", async (_e, siteId: number) => {
    const token = cachedDaemonToken();
    if (!token) throw new Error("daemon token unavailable — restart daemon");
    const r = await fetch("http://127.0.0.1:9090/sites/remove", {
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
      body: JSON.stringify({ site_id: siteId }),
    });
    if (!r.ok) throw new Error(`daemon ${r.status}`);
    const json = await r.json(); // { ok: true, my_sites: [...] }
    refreshSystemProxyIfActive();
    return json;
  });

  ipcMain.handle("daemon-list-sites", async () => {
    // No auth needed for /sites/my (localhost-only, read-only).
    const r = await fetch("http://127.0.0.1:9090/sites/my");
    if (!r.ok) throw new Error(`daemon ${r.status}`);
    return await r.json(); // { my_sites: [...] }
  });

  ipcMain.handle("daemon-search-sites", async (_e, q: string) => {
    const r = await fetch(`http://127.0.0.1:9090/sites/search?q=${encodeURIComponent(q)}`);
    if (!r.ok) throw new Error(`daemon ${r.status}`);
    return await r.json(); // CatalogSite[]
  });

  ipcMain.handle("tun-start", async (_e, server: string, key: string) => {
    try {
      const res = await fetch("http://127.0.0.1:9090/tun/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          server,
          key,
          helper_addr: process.platform === "darwin"
            ? "/var/run/smurov-helper.sock"
            : "127.0.0.1:9091",
        }),
      });
      if (!res.ok) return { ok: false, error: await res.text() };
      return { ok: true };
    } catch {
      return { ok: false, error: "Daemon not running" };
    }
  });

  ipcMain.handle("tun-stop", async () => {
    try {
      await fetch("http://127.0.0.1:9090/tun/stop", { method: "POST" });
      return { ok: true };
    } catch {
      return { ok: false };
    }
  });

  ipcMain.handle("tun-status", async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/tun/status");
      return await res.json();
    } catch {
      return { status: "inactive" };
    }
  });

  ipcMain.handle("tun-rules-get", async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/tun/rules");
      return await res.json();
    } catch {
      return { mode: "proxy_all_except", apps: [] };
    }
  });

  ipcMain.on("tun-rules-set", (_e, rules: any) => {
    fetch("http://127.0.0.1:9090/tun/rules", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rules),
    }).catch(() => {});
  });

  ipcMain.handle("transport-get", async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/transport");
      return await res.json();
    } catch {
      return { mode: "auto", active: "tls" };
    }
  });

  ipcMain.handle("transport-set", async (_e, mode: string) => {
    try {
      await fetch("http://127.0.0.1:9090/transport", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mode }),
      });
      return { ok: true };
    } catch {
      return { ok: false };
    }
  });

  ipcMain.handle("get-installed-apps", () => getInstalledApps());

  ipcMain.handle("get-daemon-token", () => getDaemonToken());

  ipcMain.handle("get-seed-sites", () => {
    try {
      const resourcesPath = app.isPackaged
        ? path.join(process.resourcesPath, "resources")
        : path.join(__dirname, "../../resources");
      const content = fs.readFileSync(path.join(resourcesPath, "seed_sites.json"), "utf-8");
      return JSON.parse(content);
    } catch (err) {
      console.error("[seed] failed to load seed_sites.json:", err);
      return [];
    }
  });

  ipcMain.on("tray-status", (_e, connected: boolean) => {
    setTrayConnected(connected);
  });

  ipcMain.on("show-notification", (_e, data: { title: string; body: string }) => {
    if (!Notification.isSupported()) return;
    const opts: Electron.NotificationConstructorOptions = {
      title: data.title,
      body: data.body,
      silent: false,
    };
    // macOS pulls the icon from the app bundle automatically; passing it
    // is a no-op there. Windows needs it explicitly or the notification
    // shows a blank placeholder.
    if (process.platform === "win32") {
      const buildDir = app.isPackaged
        ? path.join(process.resourcesPath, "app.asar", "build")
        : path.join(__dirname, "../../build");
      opts.icon = path.join(buildDir, "icon.png");
    }
    new Notification(opts).show();
  });

  // After macOS sleep/wake (or Windows suspend/resume), the UDP socket the
  // daemon holds is silently dead: server-side NAT has forgotten us and any
  // existing streams are stuck. Notify the renderer so it can tear down the
  // old transport and reconnect fresh instead of waiting for the keepalive
  // deadTicker to notice.
  powerMonitor.on("resume", () => {
    mainWindow?.webContents.send("system-resumed");
  });
}

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    mainWindow?.show();
    mainWindow?.focus();
  });

  app.whenReady().then(async () => {
    setupLoaderIpc();
    // Await ready-to-show so loaderShownAt is populated before bootMainApp
    // measures elapsed visible time. Without the await, a fast boot could
    // race past the window before it's actually on screen.
    await createLoaderWindow();
    // bootMainApp drives startDaemon → waitForDaemonReady → startHelper and
    // only opens the main window after the daemon is really accepting
    // requests. The loader covers the wait (and shows an error+retry if
    // anything goes wrong) — see runBoot / daemon.ts:waitForDaemonReady.
    await bootMainApp();

    // Load cached config from disk on startup; seed shown IDs so the
    // first poll doesn't spam OS notifications for old entries.
    cachedConfig = readConfigCache();
    for (const n of cachedConfig?.notifications || []) {
      shownNotificationIds.add(n.id);
    }

    // Config poller — replaces GitHub update polling. 30min interval.
    setInterval(pollConfig, 30 * 60 * 1000);
  });
}

app.on("before-quit", () => {
  disableSystemProxy();
  stopDaemon();
  stopHelper();
});

app.on("window-all-closed", () => {
  bootTrace(`window-all-closed fired, windows=${BrowserWindow.getAllWindows().length}`);
  if (process.platform !== "darwin") {
    app.quit();
  }
});
app.on("before-quit", () => bootTrace("app before-quit"));
app.on("will-quit", () => bootTrace("app will-quit"));
app.on("quit", (_e, code) => bootTrace(`app quit exitCode=${code}`));

app.on("activate", () => {
  mainWindow?.show();
});
