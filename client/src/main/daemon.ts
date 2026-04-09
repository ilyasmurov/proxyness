import { ChildProcess, spawn, execFileSync } from "child_process";
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

// killStaleDaemon hunts down a leftover daemon from a previous install
// that's still holding the API port. This happens on macOS in particular
// when the user runs an auto-updater install (new .app replaces the old
// one on disk) but never actually quits and relaunches the app — the old
// daemon, a child of the old-version Electron main process, keeps running
// for days at a time with out-of-date code. Next fresh launch would spawn
// its own daemon that immediately fails to bind 9090, crashes, and
// scheduleRestart cycles forever with the stale process still winning.
//
// Approach: check if something is already answering /health on 127.0.0.1:9090.
// If yes, look up the PID holding the port (lsof on macOS/Linux, netstat on
// Windows) and kill it. We only do this when we're about to spawn our own
// daemon, so we're not stepping on anything useful — anything binding 9090
// in the user's session is either our daemon or a misbehaving squatter.
function killStaleDaemon(): void {
  // First probe: is something actually listening?
  let hasStale = false;
  try {
    execFileSync("curl", ["-sf", "--max-time", "0.3", "http://127.0.0.1:9090/health"], {
      stdio: "pipe",
    });
    hasStale = true;
  } catch {
    return; // nothing there, clean slate
  }
  if (!hasStale) return;

  addLog("daemon", "detected stale daemon on :9090, killing");

  const pids: string[] = [];
  try {
    if (process.platform === "win32") {
      // netstat -ano | findstr :9090 — parse for the LISTENING PID
      const out = execFileSync("cmd", ["/c", "netstat -ano | findstr :9090"], {
        encoding: "utf-8",
      });
      for (const line of out.split("\n")) {
        const m = line.match(/LISTENING\s+(\d+)/i);
        if (m) pids.push(m[1]);
      }
    } else {
      // lsof -ti :9090 prints just the PIDs, one per line
      const out = execFileSync("/usr/sbin/lsof", ["-ti", ":9090"], {
        encoding: "utf-8",
      });
      for (const line of out.split("\n")) {
        const pid = line.trim();
        if (pid) pids.push(pid);
      }
    }
  } catch (err: any) {
    addLog("daemon", `pid lookup failed: ${err?.message || err}`);
    return;
  }

  for (const pid of pids) {
    try {
      if (process.platform === "win32") {
        execFileSync("taskkill", ["/F", "/PID", pid], { stdio: "pipe" });
      } else {
        process.kill(parseInt(pid, 10), "SIGKILL");
      }
      addLog("daemon", `killed stale daemon pid=${pid}`);
    } catch (err: any) {
      addLog("daemon", `kill pid=${pid} failed: ${err?.message || err}`);
    }
  }

  // Wait up to 1s for the socket to free. Each curl --max-time 0.2 call
  // acts as both a probe AND the pacing (~5Hz loop). spawn immediately
  // after the kill would race against the kernel releasing the port.
  const deadline = Date.now() + 1000;
  while (Date.now() < deadline) {
    try {
      execFileSync("curl", ["-sf", "--max-time", "0.2", "http://127.0.0.1:9090/health"], {
        stdio: "pipe",
      });
      // still answering — next iteration will re-probe after curl's own delay
    } catch {
      return; // port is free
    }
  }
}

export function startDaemon(): void {
  if (daemonProcess) return;
  daemonShouldRun = true;

  killStaleDaemon();

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
