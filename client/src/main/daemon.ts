import { ChildProcess, spawn } from "child_process";
import path from "path";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;

function getDaemonPath(): string {
  const resourcesPath = app.isPackaged
    ? process.resourcesPath
    : path.join(__dirname, "../../resources");

  const platform = process.platform;
  const arch = process.arch;

  if (platform === "win32") {
    return path.join(resourcesPath, "resources", "daemon-windows.exe");
  }
  return path.join(resourcesPath, "resources", `daemon-${platform}-${arch}`);
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
