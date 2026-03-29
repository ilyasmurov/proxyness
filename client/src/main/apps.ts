import fs from "fs";
import path from "path";
import os from "os";

export interface InstalledApp {
  name: string;
  path: string;
}

export function getInstalledApps(): InstalledApp[] {
  if (process.platform === "darwin") {
    return getMacApps();
  }
  if (process.platform === "win32") {
    return getWindowsApps();
  }
  return [];
}

function getMacApps(): InstalledApp[] {
  const apps: InstalledApp[] = [];
  const dirs = ["/Applications", path.join(os.homedir(), "Applications")];

  for (const dir of dirs) {
    try {
      for (const entry of fs.readdirSync(dir)) {
        if (entry.endsWith(".app")) {
          apps.push({
            name: entry.replace(/\.app$/, ""),
            path: path.join(dir, entry),
          });
        }
      }
    } catch {}
  }

  return apps.sort((a, b) => a.name.localeCompare(b.name));
}

function getWindowsApps(): InstalledApp[] {
  const apps: InstalledApp[] = [];
  const seen = new Set<string>();

  const dirs = [
    "C:\\Program Files",
    "C:\\Program Files (x86)",
    path.join(os.homedir(), "AppData", "Local", "Programs"),
  ];

  for (const dir of dirs) {
    try {
      for (const entry of fs.readdirSync(dir)) {
        const fullPath = path.join(dir, entry);
        try {
          if (!fs.statSync(fullPath).isDirectory()) continue;
          const files = fs.readdirSync(fullPath);
          const hasExe = files.some(
            (f) => f.endsWith(".exe") && !f.toLowerCase().includes("uninstall")
          );
          if (hasExe && !seen.has(entry.toLowerCase())) {
            seen.add(entry.toLowerCase());
            apps.push({ name: entry, path: fullPath });
          }
        } catch {}
      }
    } catch {}
  }

  return apps.sort((a, b) => a.name.localeCompare(b.name));
}
