import { contextBridge, ipcRenderer } from "electron";

type LoaderPhase = "daemon" | "helper" | "ready" | "error";
interface LoaderStatus {
  phase: LoaderPhase;
  message: string;
}

contextBridge.exposeInMainWorld("loaderApi", {
  onStatus: (cb: (payload: LoaderStatus) => void) =>
    ipcRenderer.on("loader-status", (_e, payload: LoaderStatus) => cb(payload)),
  retry: () => ipcRenderer.send("loader-retry"),
  quit: () => ipcRenderer.send("loader-quit"),
});
