import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("updater", {
  onUpdateAvailable: (cb: (version: string) => void) =>
    ipcRenderer.on("update-available", (_e, version) => cb(version)),
  onUpdateDownloaded: (cb: () => void) =>
    ipcRenderer.on("update-downloaded", () => cb()),
  onUpdateProgress: (cb: (percent: number) => void) =>
    ipcRenderer.on("update-progress", (_e, percent) => cb(percent)),
  onUpdateNotAvailable: (cb: () => void) =>
    ipcRenderer.on("update-not-available", () => cb()),
  downloadUpdate: () => ipcRenderer.send("download-update"),
  installUpdate: () => ipcRenderer.send("install-update"),
  checkForUpdates: () => ipcRenderer.send("check-for-updates"),
});

contextBridge.exposeInMainWorld("sysproxy", {
  enable: () => ipcRenderer.send("enable-proxy"),
  disable: () => ipcRenderer.send("disable-proxy"),
});

contextBridge.exposeInMainWorld("appInfo", {
  getVersion: () => ipcRenderer.invoke("get-version"),
});
