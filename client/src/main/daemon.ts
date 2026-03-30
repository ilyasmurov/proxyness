import { ChildProcess, spawn } from "child_process";
import path from "path";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;

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

  daemonProcess.stdout?.on("data", (data: Buffer) => {
    console.log(`[daemon] ${data.toString().trim()}`);
  });

  daemonProcess.stderr?.on("data", (data: Buffer) => {
    console.error(`[daemon] ${data.toString().trim()}`);
  });

  daemonProcess.on("exit", (code) => {
    console.log(`[daemon] exited with code ${code}`);
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
      console.error(`[helper] failed to start: ${err.message}`);
      helperProcess = null;
    });

    helperProcess.stdout?.on("data", (data: Buffer) => {
      console.log(`[helper] ${data.toString().trim()}`);
    });

    helperProcess.stderr?.on("data", (data: Buffer) => {
      console.error(`[helper] ${data.toString().trim()}`);
    });

    helperProcess.on("exit", (code) => {
      console.log(`[helper] exited with code ${code}`);
      helperProcess = null;
    });
  } catch {
    console.error("[helper] spawn failed");
    helperProcess = null;
  }
}

export function stopHelper(): void {
  if (helperProcess) {
    helperProcess.kill();
    helperProcess = null;
  }
}
