import { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain, shell, powerMonitor, net } from "electron";
import { spawn } from "child_process";
import path from "path";
import fs from "fs";
import { startDaemon, stopDaemon, startHelper, stopHelper, getLogs, clearLogs } from "./daemon";
import { enableSystemProxy, disableSystemProxy } from "./sysproxy";
import { getInstalledApps } from "./apps";

const UPDATE_BASE = "https://github.com/ilyasmurov/smurov-proxy/releases/latest/download";

let mainWindow: BrowserWindow | null = null;
let logsWindow: BrowserWindow | null = null;
let updateWindow: BrowserWindow | null = null;
let tray: Tray | null = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 840,
    height: 580,
    resizable: false,
    frame: false,
    transparent: false,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
      preload: path.join(__dirname, "preload.js"),
    },
  });

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

function isNewer(latest: string, current: string): boolean {
  const l = latest.split(".").map(Number);
  const c = current.split(".").map(Number);
  for (let i = 0; i < 3; i++) {
    if ((l[i] || 0) > (c[i] || 0)) return true;
    if ((l[i] || 0) < (c[i] || 0)) return false;
  }
  return false;
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

function sendUpdate(channel: string, ...args: any[]) {
  mainWindow?.webContents.send(channel, ...args);
  updateWindow?.webContents.send(channel, ...args);
}

function setupIpc() {
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
      backgroundColor: "#0b0f1a",
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
      backgroundColor: "#0b0f1a",
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
    enableSystemProxy();
  });

  ipcMain.on("disable-proxy", () => {
    disableSystemProxy();
  });

  ipcMain.on("pac-sites", (_e, data: { proxy_all: boolean; sites: string[] }) => {
    fetch("http://127.0.0.1:9090/pac/sites", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    })
      .then(() => enableSystemProxy())
      .catch(() => {});
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

  app.whenReady().then(() => {
    startDaemon();
    startHelper();
    createWindow();
    createTray();
    setupIpc();
  });
}

app.on("before-quit", () => {
  disableSystemProxy();
  stopDaemon();
  stopHelper();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("activate", () => {
  mainWindow?.show();
});
