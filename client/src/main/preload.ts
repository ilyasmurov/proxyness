import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("updater", {
  onUpdateAvailable: (cb: (version: string) => void) =>
    ipcRenderer.on("update-available", (_e, version) => cb(version)),
  onUpdateDownloaded: (cb: () => void) =>
    ipcRenderer.on("update-downloaded", () => cb()),
  onUpdateProgress: (cb: (percent: number) => void) =>
    ipcRenderer.on("update-progress", (_e, percent) => cb(percent)),
  downloadUpdate: () => ipcRenderer.send("download-update"),
  installUpdate: () => ipcRenderer.send("install-update"),
});
