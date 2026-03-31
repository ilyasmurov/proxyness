import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("updaterApi", {
  checkVersion: () => ipcRenderer.invoke("check-update-version"),
  downloadUpdate: () => ipcRenderer.send("download-update"),
  installUpdate: () => ipcRenderer.send("install-update"),
  onUpdateProgress: (cb: (percent: number) => void) =>
    ipcRenderer.on("update-progress", (_e, percent) => cb(percent)),
  onUpdateDownloaded: (cb: () => void) =>
    ipcRenderer.on("update-downloaded", () => cb()),
  onUpdateError: (cb: () => void) =>
    ipcRenderer.on("update-error", () => cb()),
});
