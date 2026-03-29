// Allow self-signed cert on our VPS for auto-updater
process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

import { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain } from "electron";
import path from "path";
import { autoUpdater } from "electron-updater";
import { startDaemon, stopDaemon, startHelper, stopHelper } from "./daemon";
import { enableSystemProxy, disableSystemProxy } from "./sysproxy";
import { getInstalledApps } from "./apps";

let mainWindow: BrowserWindow | null = null;
let tray: Tray | null = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 400,
    height: 500,
    resizable: false,
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

  // macOS: minimize to tray on close. Windows: quit on close.
  if (process.platform === "darwin") {
    mainWindow.on("close", (e) => {
      if (mainWindow && !(app as any).isQuitting) {
        e.preventDefault();
        mainWindow.hide();
      }
    });
  }
}

function createTray() {
  const icon = nativeImage.createEmpty();
  tray = new Tray(icon);
  tray.setToolTip("SmurovProxy");

  const contextMenu = Menu.buildFromTemplate([
    {
      label: "Show",
      click: () => mainWindow?.show(),
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

function setupAutoUpdater() {
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = true;

  autoUpdater.on("update-downloaded", () => {
    mainWindow?.webContents.send("update-downloaded");
  });

  autoUpdater.on("download-progress", (progress) => {
    mainWindow?.webContents.send("update-progress", Math.round(progress.percent));
  });

  ipcMain.handle("check-update-version", async () => {
    try {
      const ymlFile = process.platform === "darwin" ? "latest-mac.yml" : "latest.yml";
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 10000);
      const res = await fetch(`https://82.97.246.65/download/${ymlFile}`, {
        signal: controller.signal,
      });
      clearTimeout(timeout);
      const text = await res.text();
      const match = text.match(/^version:\s*(.+)$/m);
      if (!match) return { hasUpdate: false, latestVersion: null };
      const latestVersion = match[1].trim();
      const currentVersion = app.getVersion();
      return { hasUpdate: isNewer(latestVersion, currentVersion), latestVersion };
    } catch {
      return { hasUpdate: false, latestVersion: null, error: true };
    }
  });

  ipcMain.on("download-update", async () => {
    try {
      await autoUpdater.checkForUpdates();
      autoUpdater.downloadUpdate();
    } catch {}
  });

  ipcMain.on("install-update", () => {
    autoUpdater.quitAndInstall();
  });

  ipcMain.handle("get-version", () => app.getVersion());

  ipcMain.on("enable-proxy", () => {
    enableSystemProxy();
  });

  ipcMain.on("disable-proxy", () => {
    disableSystemProxy();
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

  ipcMain.handle("get-installed-apps", () => getInstalledApps());
}

app.whenReady().then(() => {
  startDaemon();
  startHelper();
  createWindow();
  createTray();
  setupAutoUpdater();
});

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
