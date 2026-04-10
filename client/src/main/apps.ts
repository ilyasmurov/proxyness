import fs from "fs";
import path from "path";
import os from "os";
import { execSync } from "child_process";

export interface InstalledApp {
  name: string;
  path: string;
}

let cachedApps: InstalledApp[] | null = null;

export function getInstalledApps(): InstalledApp[] {
  if (cachedApps) return cachedApps;
  if (process.platform === "darwin") {
    cachedApps = getMacApps();
  } else if (process.platform === "win32") {
    cachedApps = getWindowsApps();
  } else {
    cachedApps = [];
  }
  return cachedApps;
}

// Well-known CLI apps installed outside /Applications
const MAC_CLI_APPS: { name: string; dir: string }[] = [
  { name: "claude", dir: path.join(os.homedir(), ".local/share/claude") },
];

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

  // Add known CLI apps
  for (const cli of MAC_CLI_APPS) {
    try {
      if (fs.existsSync(cli.dir)) {
        apps.push({ name: cli.name, path: cli.dir });
      }
    } catch {}
  }

  return apps.sort((a, b) => a.name.localeCompare(b.name));
}

// Directories that are data/cache, not real apps
const SKIP_DIRS = new Set([
  "google", "microsoft", "packages", "temp", "comms", "connecteddevicesplatform",
  "d3dscache", "deliveryoptimization", "gamingservices", "lxss", "publisher.mapdata",
  "squirreltmp", "windowsapps", "fontcache", "placehodertiledatalayer",
]);

function getWindowsApps(): InstalledApp[] {
  const apps: InstalledApp[] = [];
  const seen = new Set<string>();

  // 1. Registry: most reliable, finds apps regardless of install location
  const regKeys = [
    "HKLM\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall",
    "HKCU\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall",
    "HKLM\\SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall",
  ];
  for (const key of regKeys) {
    try {
      const out = execSync(`reg query "${key}" /s`, { encoding: "utf-8", timeout: 5000 });
      let name = "";
      let loc = "";
      for (const line of out.split("\n")) {
        const trimmed = line.trim();
        if (trimmed.startsWith("HKEY_")) {
          if (name && loc && !seen.has(name.toLowerCase())) {
            seen.add(name.toLowerCase());
            apps.push({ name, path: loc });
          }
          name = "";
          loc = "";
        }
        const match = trimmed.match(/^\s*(\w+)\s+REG_SZ\s+(.+)$/);
        if (match) {
          if (match[1] === "DisplayName") name = match[2].trim();
          if (match[1] === "InstallLocation") loc = match[2].trim().replace(/\\$/, "");
        }
      }
      if (name && loc && !seen.has(name.toLowerCase())) {
        seen.add(name.toLowerCase());
        apps.push({ name, path: loc });
      }
    } catch {}
  }

  // 2. Directory scan: fallback for apps without registry entries
  const localAppData = path.join(os.homedir(), "AppData", "Local");
  const roamingAppData = path.join(os.homedir(), "AppData", "Roaming");

  const dirs = [
    "C:\\Program Files",
    "C:\\Program Files (x86)",
    path.join(localAppData, "Programs"),
    localAppData,
    roamingAppData,
  ];

  for (const dir of dirs) {
    try {
      for (const entry of fs.readdirSync(dir)) {
        if (entry.startsWith(".") || SKIP_DIRS.has(entry.toLowerCase())) continue;
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
