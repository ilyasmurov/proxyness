import fs from "fs";
import os from "os";
import path from "path";

function tokenPath(): string {
  if (process.platform === "win32") {
    return path.join(process.env.APPDATA || os.homedir(), "SmurovProxy", "daemon-token");
  }
  return path.join(os.homedir(), ".config", "smurov-proxy", "daemon-token");
}

export function getDaemonToken(): string {
  try {
    return fs.readFileSync(tokenPath(), "utf-8").trim();
  } catch {
    return "";
  }
}
