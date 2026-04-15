import { ChildProcess, spawn, execFileSync } from "child_process";
import path from "path";
import fs from "node:fs";
import { createConnection } from "node:net";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;
let daemonShouldRun = false;
let daemonRestartTimer: ReturnType<typeof setTimeout> | null = null;

const MAX_LOG_LINES = 500;
const logLines: string[] = [];

// Disk logging: one rolling file in Electron's per-app logs directory
// (macOS: ~/Library/Logs/Proxyness/daemon.log, Windows: %APPDATA%\Proxyness\logs\daemon.log,
// Linux: ~/.config/Proxyness/logs/daemon.log). Rotated when it crosses
// 20 MB, keeping the previous 3 versions. Survives client restarts.
const LOG_FILE_MAX_BYTES = 20 * 1024 * 1024;
const LOG_FILE_KEEP = 3;
let logStream: fs.WriteStream | null = null;
let logStreamBytes = 0;

function getLogFilePath(): string {
  return path.join(app.getPath("logs"), "daemon.log");
}

function rotateLogsOnDisk(base: string): void {
  for (let i = LOG_FILE_KEEP - 1; i >= 1; i--) {
    const src = `${base}.${i}`;
    const dst = `${base}.${i + 1}`;
    if (fs.existsSync(src)) {
      try { fs.renameSync(src, dst); } catch { /* ignore */ }
    }
  }
  if (fs.existsSync(base)) {
    try { fs.renameSync(base, `${base}.1`); } catch { /* ignore */ }
  }
}

function ensureLogStream(): fs.WriteStream | null {
  if (logStream) return logStream;
  try {
    const dir = app.getPath("logs");
    fs.mkdirSync(dir, { recursive: true });
    const base = getLogFilePath();
    if (fs.existsSync(base)) {
      const stat = fs.statSync(base);
      if (stat.size > LOG_FILE_MAX_BYTES) {
        rotateLogsOnDisk(base);
        logStreamBytes = 0;
      } else {
        logStreamBytes = stat.size;
      }
    } else {
      logStreamBytes = 0;
    }
    logStream = fs.createWriteStream(base, { flags: "a" });
    return logStream;
  } catch {
    return null;
  }
}

function writeToDisk(entry: string): void {
  const s = ensureLogStream();
  if (!s) return;
  const line = entry + "\n";
  s.write(line);
  logStreamBytes += line.length;
  if (logStreamBytes > LOG_FILE_MAX_BYTES) {
    // Mid-run rotation: close, shuffle, reopen. Any write buffered in the
    // old stream still flushes to the now-.1 file — not lost, just
    // lands in the previous rotation slot.
    try { s.end(); } catch { /* ignore */ }
    logStream = null;
    logStreamBytes = 0;
    rotateLogsOnDisk(getLogFilePath());
  }
}

function addLog(source: string, data: string) {
  const now = new Date();
  const ts = now.toLocaleTimeString("en-GB", { hour12: false });
  const isoTs = now.toISOString();
  for (const line of data.trim().split("\n")) {
    if (line) {
      logLines.push(`${ts} [${source}] ${line}`);
      if (logLines.length > MAX_LOG_LINES) logLines.shift();
      writeToDisk(`${isoTs} [${source}] ${line}`);
    }
  }
}

export function getLogs(): string[] {
  return [...logLines];
}

export function getLogFile(): string {
  return getLogFilePath();
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
let helperStatus: { ok: boolean; error: string } = { ok: true, error: "" };

export async function getHelperStatus(): Promise<{ ok: boolean; error: string }> {
  if (process.platform === "darwin" && app.isPackaged) {
    return { ok: true, error: "" };
  }
  if (!helperStatus.ok && await probeHelperSocket()) {
    helperStatus = { ok: true, error: "" };
  }
  return { ...helperStatus };
}

// Wait for the helper to settle after spawn. If it exits quickly (e.g.
// socket bind failure), the exit handler fires within milliseconds —
// 500ms is plenty to catch that without slowing boot on the happy path.
// If our spawned helper dies but the socket is reachable (another instance
// running via sudo or launchd), report ok.
export async function waitForHelperReady(timeoutMs: number = 500): Promise<{ ok: boolean; error: string }> {
  if (process.platform === "darwin" && app.isPackaged) {
    return { ok: true, error: "" };
  }
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (!helperProcess) {
      if (!helperStatus.ok && await probeHelperSocket()) {
        addLog("helper", "spawned helper exited but another instance is running — ok");
        helperStatus = { ok: true, error: "" };
      }
      return helperStatus;
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  return helperProcess ? { ok: true, error: "" } : helperStatus;
}

const HELPER_SOCKET = "/var/run/proxyness-helper.sock";

function probeHelperSocket(): Promise<boolean> {
  if (process.platform !== "darwin") return Promise.resolve(false);
  return new Promise((resolve) => {
    const c = createConnection(HELPER_SOCKET, () => {
      c.destroy();
      resolve(true);
    });
    c.on("error", () => resolve(false));
    c.setTimeout(300, () => { c.destroy(); resolve(false); });
  });
}

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

  helperStatus = { ok: true, error: "" };
  let helperOutput = "";

  const helperPath = getHelperPath();

  // Dev mode on macOS: helper needs root for TUN device creation, route
  // management, and binding to /var/run. Use osascript to prompt for admin
  // password — runs the helper as root in the background.
  if (process.platform === "darwin" && !app.isPackaged) {
    const logFile = "/tmp/proxyness-helper.log";
    const script = `do shell script "` +
      `'${helperPath}' > '${logFile}' 2>&1 & echo $!" ` +
      `with administrator privileges`;
    try {
      const proc = spawn("osascript", ["-e", script], { stdio: "pipe" });
      let pidOutput = "";
      proc.stdout?.on("data", (d: Buffer) => { pidOutput += d.toString(); });
      proc.on("exit", (code) => {
        if (code !== 0) {
          addLog("helper", "admin password dialog cancelled or failed");
          helperStatus = { ok: false, error: "Admin password required" };
          return;
        }
        const pid = parseInt(pidOutput.trim(), 10);
        addLog("helper", `started with sudo (pid ${pid}), logs → ${logFile}`);
        // Poll the log file briefly to catch early failures
        setTimeout(() => {
          try {
            const log = require("fs").readFileSync(logFile, "utf-8");
            if (log) addLog("helper", log.trim());
            if (log.includes("bind:") || log.includes("listen:")) {
              const m = log.match(/listen:.*?(bind:.*)/);
              helperStatus = { ok: false, error: m ? m[1] : "bind failed" };
            }
          } catch {}
        }, 500);
      });
    } catch {
      addLog("helper", "osascript spawn failed");
      helperStatus = { ok: false, error: "Failed to request admin privileges" };
    }
    return;
  }

  try {
    helperProcess = spawn(helperPath, [], {
      stdio: "pipe",
      cwd: path.dirname(helperPath),
    });

    helperProcess.on("error", (err) => {
      addLog("helper", `failed to start: ${err.message}`);
      helperStatus = { ok: false, error: err.message };
      helperProcess = null;
    });

    helperProcess.stdout?.on("data", (data: Buffer) => {
      const text = data.toString();
      addLog("helper", text);
      helperOutput += text;
    });

    helperProcess.stderr?.on("data", (data: Buffer) => {
      const text = data.toString();
      addLog("helper", text);
      helperOutput += text;
    });

    helperProcess.on("exit", (code) => {
      addLog("helper", `exited with code ${code}`);
      if (code !== 0) {
        const bindErr = helperOutput.match(/listen:.*?(bind:.*)/);
        helperStatus = {
          ok: false,
          error: bindErr ? bindErr[1] : `Helper exited with code ${code}`,
        };
      }
      helperProcess = null;
    });
  } catch {
    addLog("helper", "spawn failed");
    helperStatus = { ok: false, error: "Helper binary not found" };
    helperProcess = null;
  }
}

export function stopHelper(): void {
  if (helperProcess) {
    helperProcess.kill("SIGKILL");
    helperProcess = null;
  }
  // Dev mode: helper runs as root via osascript — kill by finding the process
  if (process.platform === "darwin" && !app.isPackaged) {
    try {
      const pids = execFileSync("/usr/sbin/lsof", ["-ti", "unix:" + HELPER_SOCKET], {
        encoding: "utf-8",
      }).trim();
      for (const pid of pids.split("\n").filter(Boolean)) {
        process.kill(parseInt(pid, 10), "SIGKILL");
        addLog("helper", `killed sudo helper pid=${pid}`);
      }
    } catch {
      // No process found — fine
    }
  }
}
