// Allow self-signed cert on our VPS for auto-updater
process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";

import { app, BrowserWindow, Tray, Menu, nativeImage, ipcMain } from "electron";
import path from "path";
import { autoUpdater } from "electron-updater";
import { startDaemon, stopDaemon } from "./daemon";
import { enableSystemProxy, disableSystemProxy } from "./sysproxy";

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

  // Minimize to tray instead of closing
  mainWindow.on("close", (e) => {
    if (mainWindow && !(app as any).isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    }
  });
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

function setupAutoUpdater() {
  autoUpdater.autoDownload = false;
  autoUpdater.autoInstallOnAppQuit = true;

  autoUpdater.on("update-available", (info) => {
    mainWindow?.webContents.send("update-available", info.version);
  });

  autoUpdater.on("update-not-available", () => {
    mainWindow?.webContents.send("update-not-available");
  });

  autoUpdater.on("update-downloaded", () => {
    mainWindow?.webContents.send("update-downloaded");
  });

  autoUpdater.on("download-progress", (progress) => {
    mainWindow?.webContents.send("update-progress", Math.round(progress.percent));
  });

  ipcMain.on("download-update", () => {
    autoUpdater.downloadUpdate();
  });

  ipcMain.on("install-update", () => {
    autoUpdater.quitAndInstall();
  });

  ipcMain.on("check-for-updates", () => {
    autoUpdater.checkForUpdates().catch(() => {});
  });

  ipcMain.handle("get-version", () => app.getVersion());

  ipcMain.on("enable-proxy", () => {
    enableSystemProxy();
  });

  ipcMain.on("disable-proxy", () => {
    disableSystemProxy();
  });

  autoUpdater.checkForUpdates().catch(() => {});
}

app.whenReady().then(() => {
  startDaemon();
  createWindow();
  createTray();
  setupAutoUpdater();
});

app.on("before-quit", () => {
  disableSystemProxy();
  stopDaemon();
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("activate", () => {
  mainWindow?.show();
});
