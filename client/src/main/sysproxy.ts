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

function pacUrl() {
  // Unique URL each time forces macOS and browsers to re-fetch the PAC file
  return `http://${PROXY_HOST}:9090/proxy.pac?t=${Date.now()}`;
}

function macEnable() {
  const url = pacUrl();
  for (const svc of getNetworkServices()) {
    try {
      execSync(`networksetup -setautoproxyurl "${svc}" "${url}"`);
      execSync(`networksetup -setautoproxystate "${svc}" on`);
      // Disable plain SOCKS in case it was set before
      execSync(`networksetup -setsocksfirewallproxystate "${svc}" off`);
    } catch {
      // skip services that don't support proxy (e.g. Bluetooth)
    }
  }
}

function macDisable() {
  for (const svc of getNetworkServices()) {
    try {
      execSync(`networksetup -setautoproxystate "${svc}" off`);
    } catch {
      // ignore
    }
  }
}

const WIN_REFRESH_PROXY = `
Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;
public class WinInet {
    [DllImport("wininet.dll", SetLastError=true)]
    public static extern bool InternetSetOption(int h, int o, int b, int l);
}
'@
[WinInet]::InternetSetOption(0, 39, 0, 0)
[WinInet]::InternetSetOption(0, 37, 0, 0)
`;

function winRefreshProxy() {
  try {
    execSync(`powershell -NoProfile -Command "${WIN_REFRESH_PROXY}"`, { windowsHide: true });
  } catch {
    // ignore
  }
}

function winEnable() {
  try {
    const url = pacUrl();
    const regBase = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings`;
    execSync(`reg add "${regBase}" /v AutoConfigURL /t REG_SZ /d "${url}" /f`);
    execSync(`reg add "${regBase}" /v ProxyEnable /t REG_DWORD /d 0 /f`);
    winRefreshProxy();
  } catch {
    // ignore
  }
}

function winDisable() {
  try {
    const regBase = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings`;
    execSync(`reg delete "${regBase}" /v AutoConfigURL /f`);
    execSync(`reg add "${regBase}" /v ProxyEnable /t REG_DWORD /d 0 /f`);
    winRefreshProxy();
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
