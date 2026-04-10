import { execSync, exec } from "child_process";

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
  const cmds: string[] = [];
  for (const svc of getNetworkServices()) {
    cmds.push(`networksetup -setautoproxyurl "${svc}" "${url}"`);
    cmds.push(`networksetup -setautoproxystate "${svc}" on`);
    cmds.push(`networksetup -setsocksfirewallproxystate "${svc}" off`);
  }
  if (cmds.length) exec(cmds.join(" ; "), () => {});
}

function macDisable() {
  const cmds: string[] = [];
  for (const svc of getNetworkServices()) {
    cmds.push(`networksetup -setautoproxystate "${svc}" off`);
  }
  if (cmds.length) exec(cmds.join(" ; "), () => {});
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
  exec(`powershell -NoProfile -Command "${WIN_REFRESH_PROXY}"`, { windowsHide: true }, () => {});
}

function winEnable() {
  const url = pacUrl();
  const regBase = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings`;
  const cmds = [
    `reg add "${regBase}" /v AutoConfigURL /t REG_SZ /d "${url}" /f`,
    `reg add "${regBase}" /v ProxyEnable /t REG_DWORD /d 0 /f`,
  ];
  exec(cmds.join(" && "), { windowsHide: true }, () => { winRefreshProxy(); });
}

function winDisable() {
  const regBase = `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings`;
  const cmds = [
    `reg delete "${regBase}" /v AutoConfigURL /f`,
    `reg add "${regBase}" /v ProxyEnable /t REG_DWORD /d 0 /f`,
  ];
  exec(cmds.join(" && "), { windowsHide: true }, () => { winRefreshProxy(); });
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
