// Daemon client: token-aware fetch wrapper for the local Smurov daemon API.
// All extension → daemon HTTP traffic flows through this module.

const DAEMON_BASE = "http://127.0.0.1:9090";

let cachedToken = null;

async function getToken() {
  if (cachedToken) return cachedToken;
  const stored = await chrome.storage.local.get("daemon_token");
  cachedToken = stored.daemon_token || null;
  return cachedToken;
}

export async function setToken(token) {
  cachedToken = token;
  await chrome.storage.local.set({ daemon_token: token });
}

export async function clearToken() {
  cachedToken = null;
  await chrome.storage.local.remove("daemon_token");
}

async function call(method, path, body) {
  const token = await getToken();
  if (!token) {
    return { ok: false, error: "no_token" };
  }

  let resp;
  try {
    resp = await fetch(DAEMON_BASE + path, {
      method,
      headers: {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
    });
  } catch (e) {
    return { ok: false, error: "daemon_down" };
  }

  if (resp.status === 401) {
    await clearToken();
    return { ok: false, error: "unauthorized" };
  }
  if (!resp.ok) {
    const text = await resp.text();
    return { ok: false, error: `http_${resp.status}`, message: text };
  }
  const data = await resp.json();
  return { ok: true, data };
}

export const daemonClient = {
  match: (host) => call("GET", `/sites/match?host=${encodeURIComponent(host)}`),
  add: (primaryDomain, label) => call("POST", "/sites/add", { primary_domain: primaryDomain, label }),
  discover: (siteId, domains) => call("POST", "/sites/discover", { site_id: siteId, domains }),
  test: (url) => call("POST", "/sites/test", { url }),
  setEnabled: (siteId, enabled) => call("POST", "/sites/set-enabled", { site_id: siteId, enabled }),
  ping: async () => {
    // Used for daemon-up detection without needing a real query.
    const r = await call("GET", "/sites/match?host=ping.local");
    return r.ok || r.error === "unauthorized";
  },
};
