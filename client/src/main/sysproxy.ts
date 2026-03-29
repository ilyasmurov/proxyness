import { execSync } from "child_process";

const PROXY_HOST = "127.0.0.1";
const PROXY_PORT = "1080";

function getNetworkServices(): string[] {
  try {
    const output = execSync("networksetup -listallnetworkservices", {
      encoding: "utf-8",
    });
    return output
      .split("\n")
      .slice(1) // skip header "An asterisk (*) denotes..."
      .map((s) => s.replace(/^\*/, "").trim())
      .filter(Boolean);
  } catch {
    return ["Wi-Fi"];
  }
}

function macEnable() {
  for (const svc of getNetworkServices()) {
    try {
      execSync(
        `networksetup -setsocksfirewallproxy "${svc}" ${PROXY_HOST} ${PROXY_PORT}`,
      );
      execSync(`networksetup -setsocksfirewallproxystate "${svc}" on`);
    } catch {
      // skip services that don't support proxy (e.g. Bluetooth)
    }
  }
}

function macDisable() {
  for (const svc of getNetworkServices()) {
    try {
      execSync(`networksetup -setsocksfirewallproxystate "${svc}" off`);
    } catch {
      // ignore
    }
  }
}

function winEnable() {
  try {
    execSync(
      `reg add "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings" /v ProxyEnable /t REG_DWORD /d 1 /f`,
    );
    execSync(
      `reg add "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings" /v ProxyServer /t REG_SZ /d "socks=${PROXY_HOST}:${PROXY_PORT}" /f`,
    );
  } catch {
    // ignore
  }
}

function winDisable() {
  try {
    execSync(
      `reg add "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings" /v ProxyEnable /t REG_DWORD /d 0 /f`,
    );
  } catch {
    // ignore
  }
}

export function enableSystemProxy() {
  if (process.platform === "darwin") {
    macEnable();
  } else if (process.platform === "win32") {
    winEnable();
  }
}

export function disableSystemProxy() {
  if (process.platform === "darwin") {
    macDisable();
  } else if (process.platform === "win32") {
    winDisable();
  }
}
