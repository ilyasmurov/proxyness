process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

import { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain, shell } from "electron";
import path from "path";
import fs from "fs";
import https from "https";
import { startDaemon, stopDaemon, startHelper, stopHelper } from "./daemon";
import { enableSystemProxy, disableSystemProxy } from "./sysproxy";
import { getInstalledApps } from "./apps";

const UPDATE_BASE = "https://82.97.246.65/download";

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

let installerPath = "";

async function fetchYml(): Promise<{ version: string; filename: string } | null> {
  const ymlFile = process.platform === "darwin" ? "latest-mac.yml" : "latest.yml";
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 10000);
  try {
    const res = await fetch(`${UPDATE_BASE}/${ymlFile}`, { signal: controller.signal });
    clearTimeout(timeout);
    const text = await res.text();
    const ver = text.match(/^version:\s*(.+)$/m);
    const p = text.match(/^path:\s*(.+)$/m);
    if (!ver || !p) return null;
    return { version: ver[1].trim(), filename: p[1].trim() };
  } catch {
    clearTimeout(timeout);
    return null;
  }
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
    try {
      const info = await fetchYml();
      if (!info) return;

      const dest = path.join(app.getPath("temp"), info.filename);
      const file = fs.createWriteStream(dest);

      https.get(`${UPDATE_BASE}/${info.filename}`, { rejectUnauthorized: false }, (res) => {
        const total = parseInt(res.headers["content-length"] || "0", 10);
        let downloaded = 0;

        res.on("data", (chunk: Buffer) => {
          downloaded += chunk.length;
          file.write(chunk);
          if (total > 0) {
            mainWindow?.webContents.send("update-progress", Math.round((downloaded / total) * 100));
          }
        });

        res.on("end", () => {
          file.end(() => {
            installerPath = dest;
            mainWindow?.webContents.send("update-downloaded");
          });
        });

        res.on("error", () => file.close());
      }).on("error", () => file.close());
    } catch {}
  });

  ipcMain.on("install-update", () => {
    if (installerPath) {
      shell.openPath(installerPath);
      setTimeout(() => app.quit(), 1000);
    }
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
  setupIpc();
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
