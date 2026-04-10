import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("updater", {
  downloadUpdate: () => ipcRenderer.send("download-update"),
  installUpdate: () => ipcRenderer.send("install-update"),
  onUpdateProgress: (cb: (percent: number) => void) =>
    ipcRenderer.on("update-progress", (_e, percent) => cb(percent)),
  onUpdateDownloaded: (cb: () => void) =>
    ipcRenderer.on("update-downloaded", () => cb()),
  onUpdateError: (cb: () => void) =>
    ipcRenderer.on("update-error", () => cb()),
  // Config-driven: replaces GitHub-based version polling
  onConfigUpdated: (cb: (config: any) => void) =>
    ipcRenderer.on("config-updated", (_e, config) => cb(config)),
  getConfig: () => ipcRenderer.invoke("get-config"),
  storeKey: (key: string) => ipcRenderer.send("store-key", key),
});

contextBridge.exposeInMainWorld("sysproxy", {
  enable: () => ipcRenderer.send("enable-proxy"),
  disable: () => ipcRenderer.send("disable-proxy"),
  setPacSites: (data: { proxy_all: boolean }) => ipcRenderer.send("pac-sites", data),
});

contextBridge.exposeInMainWorld("appInfo", {
  getVersion: () => ipcRenderer.invoke("get-version"),
  getLogs: () => ipcRenderer.invoke("get-logs"),
  clearLogs: () => ipcRenderer.invoke("clear-logs"),
  closeWindow: () => ipcRenderer.send("window-close"),
  openLogs: () => ipcRenderer.send("open-logs"),
  openUpdate: () => ipcRenderer.send("open-update"),
  setTrayStatus: (connected: boolean) => ipcRenderer.send("tray-status", connected),
  showNotification: (title: string, body: string) =>
    ipcRenderer.send("show-notification", { title, body }),
  getSeedSites: () => ipcRenderer.invoke("get-seed-sites"),
  getDaemonToken: () => ipcRenderer.invoke("get-daemon-token"),
  daemonSetEnabled: (siteId: number, enabled: boolean) =>
    ipcRenderer.invoke("daemon-set-enabled", siteId, enabled),
  daemonAddSite: (primaryDomain: string, label: string) =>
    ipcRenderer.invoke("daemon-add-site", primaryDomain, label),
  daemonRemoveSite: (siteId: number) =>
    ipcRenderer.invoke("daemon-remove-site", siteId),
  daemonListSites: () =>
    ipcRenderer.invoke("daemon-list-sites"),
  daemonSearchSites: (q: string) =>
    ipcRenderer.invoke("daemon-search-sites", q),
  onTrayConnect: (cb: () => void) => ipcRenderer.on("tray-connect", () => cb()),
  onTrayDisconnect: (cb: () => void) => ipcRenderer.on("tray-disconnect", () => cb()),
  onSystemResumed: (cb: () => void) => {
    const handler = () => cb();
    ipcRenderer.on("system-resumed", handler);
    return () => ipcRenderer.removeListener("system-resumed", handler);
  },
  onSitesChanged: (cb: () => void) => {
    const handler = () => cb();
    ipcRenderer.on("daemon-sites-changed", handler);
    return () => ipcRenderer.removeListener("daemon-sites-changed", handler);
  },
});

contextBridge.exposeInMainWorld("tunProxy", {
  start: (server: string, key: string) => ipcRenderer.invoke("tun-start", server, key),
  stop: () => ipcRenderer.invoke("tun-stop"),
  getStatus: () => ipcRenderer.invoke("tun-status"),
  getRules: () => ipcRenderer.invoke("tun-rules-get"),
  setRules: (rules: any) => ipcRenderer.send("tun-rules-set", rules),
  getInstalledApps: () => ipcRenderer.invoke("get-installed-apps"),
});

contextBridge.exposeInMainWorld("transport", {
  getMode: () => ipcRenderer.invoke("transport-get"),
  setMode: (mode: string) => ipcRenderer.invoke("transport-set", mode),
});
