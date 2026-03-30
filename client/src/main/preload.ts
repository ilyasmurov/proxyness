import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("updater", {
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

contextBridge.exposeInMainWorld("sysproxy", {
  enable: () => ipcRenderer.send("enable-proxy"),
  disable: () => ipcRenderer.send("disable-proxy"),
});

contextBridge.exposeInMainWorld("appInfo", {
  getVersion: () => ipcRenderer.invoke("get-version"),
  getLogs: () => ipcRenderer.invoke("get-logs"),
  clearLogs: () => ipcRenderer.invoke("clear-logs"),
  closeWindow: () => ipcRenderer.send("window-close"),
  setTrayStatus: (connected: boolean) => ipcRenderer.send("tray-status", connected),
  onTrayConnect: (cb: () => void) => ipcRenderer.on("tray-connect", () => cb()),
  onTrayDisconnect: (cb: () => void) => ipcRenderer.on("tray-disconnect", () => cb()),
});

contextBridge.exposeInMainWorld("tunProxy", {
  start: (server: string, key: string) => ipcRenderer.invoke("tun-start", server, key),
  stop: () => ipcRenderer.invoke("tun-stop"),
  getStatus: () => ipcRenderer.invoke("tun-status"),
  getRules: () => ipcRenderer.invoke("tun-rules-get"),
  setRules: (rules: any) => ipcRenderer.send("tun-rules-set", rules),
  getInstalledApps: () => ipcRenderer.invoke("get-installed-apps"),
});
