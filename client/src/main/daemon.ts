import { ChildProcess, spawn } from "child_process";
import path from "path";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;

const MAX_LOG_LINES = 500;
const logLines: string[] = [];

function addLog(source: string, data: string) {
  const ts = new Date().toLocaleTimeString("en-GB", { hour12: false });
  for (const line of data.trim().split("\n")) {
    if (line) {
      logLines.push(`${ts} [${source}] ${line}`);
      if (logLines.length > MAX_LOG_LINES) logLines.shift();
    }
  }
}

export function getLogs(): string[] {
  return [...logLines];
}

export function clearLogs(): void {
  logLines.length = 0;
}

function getDaemonPath(): string {
  const resourcesPath = app.isPackaged
    ? path.join(process.resourcesPath, "resources")
    : path.join(__dirname, "../../resources");

  const platform = process.platform;
  const arch = process.arch;

  if (platform === "win32") {
    return path.join(resourcesPath, "daemon-windows.exe");
  }
  return path.join(resourcesPath, `daemon-${platform}-${arch}`);
}

export function startDaemon(): void {
  if (daemonProcess) return;

  const daemonPath = getDaemonPath();
  daemonProcess = spawn(daemonPath, ["-api", "127.0.0.1:9090", "-listen", "127.0.0.1:1080"], {
    stdio: "pipe",
  });

  daemonProcess.on("error", (err) => {
    addLog("daemon", `failed to start: ${err.message}`);
    daemonProcess = null;
  });

  daemonProcess.stdout?.on("data", (data: Buffer) => {
    addLog("daemon", data.toString());
  });

  daemonProcess.stderr?.on("data", (data: Buffer) => {
    addLog("daemon", data.toString());
  });

  daemonProcess.on("exit", (code) => {
    addLog("daemon", `exited with code ${code}`);
    daemonProcess = null;
  });
}

export function stopDaemon(): void {
  if (daemonProcess) {
    daemonProcess.kill();
    daemonProcess = null;
  }
}

let helperProcess: ChildProcess | null = null;

function getHelperPath(): string {
  const resourcesPath = app.isPackaged
    ? path.join(process.resourcesPath, "resources")
    : path.join(__dirname, "../../resources");

  const platform = process.platform;
  const arch = process.arch;

  if (platform === "win32") {
    return path.join(resourcesPath, "helper-windows.exe");
  }
  return path.join(resourcesPath, `helper-${platform}-${arch}`);
}

export function startHelper(): void {
  if (helperProcess) return;

  // On macOS, helper is managed by launchd (installed via PKG postinstall)
  if (process.platform === "darwin" && app.isPackaged) return;

  const helperPath = getHelperPath();
  try {
    helperProcess = spawn(helperPath, [], {
      stdio: "pipe",
      cwd: path.dirname(helperPath),
    });

    helperProcess.on("error", (err) => {
      addLog("helper", `failed to start: ${err.message}`);
      helperProcess = null;
    });

    helperProcess.stdout?.on("data", (data: Buffer) => {
      addLog("helper", data.toString());
    });

    helperProcess.stderr?.on("data", (data: Buffer) => {
      addLog("helper", data.toString());
    });

    helperProcess.on("exit", (code) => {
      addLog("helper", `exited with code ${code}`);
      helperProcess = null;
    });
  } catch {
    addLog("helper", "spawn failed");
    helperProcess = null;
  }
}

export function stopHelper(): void {
  if (helperProcess) {
    helperProcess.kill();
    helperProcess = null;
  }
}
