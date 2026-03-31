import { contextBridge, ipcRenderer } from "electron";

contextBridge.exposeInMainWorld("logsApi", {
  getLogs: () => ipcRenderer.invoke("get-logs"),
  clearLogs: () => ipcRenderer.invoke("clear-logs"),
});
