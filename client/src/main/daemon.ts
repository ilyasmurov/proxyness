import { ChildProcess, spawn } from "child_process";
import path from "path";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;
let daemonShouldRun = false;
let daemonRestartTimer: ReturnType<typeof setTimeout> | null = null;

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
  daemonShouldRun = true;

  const daemonPath = getDaemonPath();
  daemonProcess = spawn(daemonPath, ["-api", "127.0.0.1:9090", "-listen", "127.0.0.1:1080"], {
    stdio: "pipe",
  });

  daemonProcess.on("error", (err) => {
    addLog("daemon", `failed to start: ${err.message}`);
    daemonProcess = null;
    scheduleRestart();
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
    scheduleRestart();
  });
}

// waitForDaemonReady polls the daemon's /health endpoint until it
// starts responding (or the deadline hits). Needed because spawn()
// returns immediately but the Go process needs a beat to bind its HTTP
// listener — if the renderer lets the user click Connect before that
// moment, the first /connect fetch hits ECONNREFUSED and tunConnect's
// in-hook retry (800ms) may not cover the full startup window, leaving
// the user with SOCKS5 dead while TUN (which runs after an extra beat)
// ends up fine. Main process awaits this before createWindow so the UI
// only appears once the daemon is actually usable.
export async function waitForDaemonReady(timeoutMs: number = 5000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      // Node 18+ has global fetch. AbortController keeps one poll from
      // stalling the boot path if the daemon is unresponsive but TCP is
      // accepted — rare but possible during startup.
      const ctrl = new AbortController();
      const to = setTimeout(() => ctrl.abort(), 300);
      const res = await fetch("http://127.0.0.1:9090/health", { signal: ctrl.signal });
      clearTimeout(to);
      if (res.ok) {
        addLog("daemon", "ready (health check passed)");
        return true;
      }
    } catch {
      // Connection refused / aborted / dns — keep polling.
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  addLog("daemon", `not ready after ${timeoutMs}ms, continuing anyway`);
  return false;
}

function scheduleRestart(): void {
  if (!daemonShouldRun || daemonRestartTimer) return;
  addLog("daemon", "restarting in 2s...");
  daemonRestartTimer = setTimeout(() => {
    daemonRestartTimer = null;
    if (daemonShouldRun) startDaemon();
  }, 2000);
}

export function stopDaemon(): void {
  daemonShouldRun = false;
  if (daemonRestartTimer) {
    clearTimeout(daemonRestartTimer);
    daemonRestartTimer = null;
  }
  if (daemonProcess) {
    daemonProcess.kill("SIGKILL");
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
    helperProcess.kill("SIGKILL");
    helperProcess = null;
  }
}
