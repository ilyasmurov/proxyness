import fs from "fs";
import os from "os";
import path from "path";

function tokenPath(): string {
  if (process.platform === "win32") {
    return path.join(process.env.APPDATA || os.homedir(), "SmurovProxy", "daemon-token");
  }
  return path.join(os.homedir(), ".config", "smurov-proxy", "daemon-token");
}

let cachedToken: string | null = null;

// cachedDaemonToken returns the daemon bearer token, reading the file
// once and caching the result in memory. Subsequent calls are
// synchronous and don't touch the disk. Returns "" if the file doesn't
// exist or can't be read.
export function cachedDaemonToken(): string {
  if (cachedToken !== null) return cachedToken;
  try {
    cachedToken = fs.readFileSync(tokenPath(), "utf-8").trim();
  } catch {
    cachedToken = "";
  }
  return cachedToken;
}

// clearCachedDaemonToken forces the next cachedDaemonToken() call to
// re-read from disk. Used if the token file changes mid-session
// (shouldn't happen in practice — daemon GetOrCreate reuses existing).
export function clearCachedDaemonToken(): void {
  cachedToken = null;
}

// getDaemonToken is kept for backwards compatibility with the existing
// `get-daemon-token` IPC handler used by BrowserExtension.tsx to show
// the token to the user. Internally uses the cache.
export function getDaemonToken(): string {
  return cachedDaemonToken();
}
