import { daemonClient, setToken, clearToken } from "./lib/daemon-client.js";

// Simple heuristic eTLD+1 extraction (not bulletproof but works for 99% of cases).
// Real public suffix list is bundled in a later version.
function getDomain(host) {
  const parts = host.split(".");
  if (parts.length <= 2) return host;
  // Common multi-level TLDs (co.uk, com.br, etc.) — incomplete but OK for now.
  const secondLevel = new Set(["co", "com", "org", "net", "ac", "gov", "edu"]);
  if (parts.length >= 3 && secondLevel.has(parts[parts.length - 2])) {
    return parts.slice(-3).join(".");
  }
  return parts.slice(-2).join(".");
}

// Per-tab state.
// Map<tabId, {host, state, siteId}>
const tabState = new Map();

// Map<tabId, { siteId, host, queue: Set<string>, flushTimer }>
const discoveryState = new Map();

const SUSPICIOUS_ERRORS = new Set([
  "net::ERR_CONNECTION_RESET",
  "net::ERR_CONNECTION_TIMED_OUT",
  "net::ERR_NAME_NOT_RESOLVED",
  "net::ERR_CONNECTION_CLOSED",
  "net::ERR_SSL_PROTOCOL_ERROR",
  "net::ERR_CONNECTION_REFUSED",
]);

// In-memory dismissals: { host: dismissedAtMs }
// Persisted to chrome.storage.local under "block_dismissals".
const blockDismissals = new Map();
const DISMISS_TTL_MS = 24 * 60 * 60 * 1000;

async function loadDismissals() {
  const stored = await chrome.storage.local.get("block_dismissals");
  const data = stored.block_dismissals || {};
  const now = Date.now();
  for (const [host, at] of Object.entries(data)) {
    if (now - at < DISMISS_TTL_MS) {
      blockDismissals.set(host, at);
    }
  }
}
loadDismissals();

async function persistDismissal(host) {
  blockDismissals.set(host, Date.now());
  const out = {};
  for (const [h, at] of blockDismissals) out[h] = at;
  await chrome.storage.local.set({ block_dismissals: out });
}

// ---------- helpers ----------

function pushStateToTab(tabId) {
  const state = tabState.get(tabId);
  if (!state) return;
  chrome.tabs.sendMessage(tabId, { type: "state_update", state }).catch(() => {});
}

async function refreshTabState(tabId, url) {
  if (!url) return;
  let urlObj;
  try { urlObj = new URL(url); } catch { return; }
  if (urlObj.protocol !== "http:" && urlObj.protocol !== "https:") {
    tabState.delete(tabId);
    return;
  }
  const fullHost = urlObj.hostname;
  const host = getDomain(fullHost);

  const r = await daemonClient.match(host);
  if (!r.ok) {
    tabState.set(tabId, { state: "down", host });
    pushStateToTab(tabId);
    return;
  }
  const data = r.data;
  if (!data.in_catalog) {
    tabState.set(tabId, { state: "add", host });
  } else if (data.proxy_enabled) {
    // Preserve the "discovering" panel while the discoveryState for
    // this tab is still active — the panel needs it to keep showing
    // the discovered counter and the manual reload button even after
    // a reload (auto or manual) has re-run refreshTabState.
    const disc = discoveryState.get(tabId);
    const prev = tabState.get(tabId);
    if (disc && disc.host === host) {
      tabState.set(tabId, {
        state: "discovering",
        host,
        siteId: data.site_id,
        discoveredCount: prev?.discoveredCount || 0,
      });
    } else {
      tabState.set(tabId, { state: "proxied", host, siteId: data.site_id });
    }
  } else {
    tabState.set(tabId, { state: "catalog_disabled", host, siteId: data.site_id });
  }
  pushStateToTab(tabId);
}

function startDiscovery(tabId, host, siteId) {
  // Clean up any prior discovery for this tab.
  stopDiscovery(tabId);
  const state = {
    siteId,
    host,
    queue: new Set(),
    flushTimer: null,
    // Budget for silent auto-reloads triggered by flushDiscovery.
    // Only 1 — it covers the common case where subresources fail on
    // the initial page load (subtitle CDN, video chunk host). After
    // that, the panel's manual "Перезагрузить" button takes over so
    // user-initiated activity doesn't get interrupted by surprise
    // reloads that would drop their scroll / video position.
    autoReloadsRemaining: 1,
  };
  discoveryState.set(tabId, state);
  state.flushTimer = setInterval(() => flushDiscovery(tabId), 5000);
}

function stopDiscovery(tabId) {
  const s = discoveryState.get(tabId);
  if (!s) return;
  if (s.flushTimer) clearInterval(s.flushTimer);
  flushDiscovery(tabId);  // final flush
  discoveryState.delete(tabId);
}

async function flushDiscovery(tabId) {
  const s = discoveryState.get(tabId);
  if (!s || s.queue.size === 0) return;
  const domains = Array.from(s.queue);
  s.queue.clear();
  const r = await daemonClient.discover(s.siteId, domains);
  if (!r?.ok || typeof r.data?.added !== "number" || r.data.added <= 0) return;

  // Bump the per-tab discovered counter so the panel can show it and
  // conditionally render the manual reload button.
  const state = tabState.get(tabId);
  if (state && state.state === "discovering") {
    state.discoveredCount = (state.discoveredCount || 0) + r.data.added;
    tabState.set(tabId, state);
    pushStateToTab(tabId);
  }

  // Silent auto-reload — budgeted to 1 per discovery session (see
  // startDiscovery). Covers the common "subresource failed on initial
  // load" case so the user doesn't have to click anything on a freshly
  // added site. After the budget is gone, the panel's manual reload
  // button takes over.
  if (s.autoReloadsRemaining > 0) {
    s.autoReloadsRemaining--;
    chrome.tabs.reload(tabId);
  }
}

// ---------- tab events ----------

chrome.tabs.onActivated.addListener(async (info) => {
  try {
    const tab = await chrome.tabs.get(info.tabId);
    refreshTabState(tab.id, tab.url);
  } catch {}
});

chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (change.url) {
    const disc = discoveryState.get(tabId);
    if (disc) {
      let newHost;
      try { newHost = getDomain(new URL(tab.url).hostname); } catch { newHost = ""; }
      if (newHost !== disc.host) {
        stopDiscovery(tabId);
        // Now that discovery is done, transition to plain "proxied" state.
        tabState.set(tabId, { state: "proxied", host: disc.host, siteId: disc.siteId });
      }
    }
    refreshTabState(tabId, tab.url);
  } else if (change.status === "complete") {
    refreshTabState(tabId, tab.url);
  }
});

chrome.tabs.onRemoved.addListener((tabId) => {
  stopDiscovery(tabId);
  tabState.delete(tabId);
});

chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    if (details.tabId < 0) return;  // background fetch
    const disc = discoveryState.get(details.tabId);
    if (!disc) return;
    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    if (urlObj.protocol !== "https:" && urlObj.protocol !== "http:") return;
    const host = urlObj.hostname;
    // Skip localhost / IP literals / our own daemon.
    if (host === "127.0.0.1" || host === "localhost") return;
    // Collapse to eTLD+1 so a single entry covers all sibling subdomains —
    // catching `a-api.anthropic.com` adds `anthropic.com`, which via PAC's
    // dnsDomainIs match also covers `api.anthropic.com`, `console.`, etc.
    // This cuts the discovered-domain list dramatically on SPAs that hit
    // dozens of service subdomains.
    disc.queue.add(getDomain(host));
  },
  { urls: ["<all_urls>"] }
);

chrome.webRequest.onErrorOccurred.addListener(
  (details) => {
    if (details.tabId < 0) return;
    if (details.type === "main_frame") return;  // handled by Flow 3 in Task 18
    if (!SUSPICIOUS_ERRORS.has(details.error)) return;

    const tab = tabState.get(details.tabId);
    if (!tab || tab.state !== "proxied") return;
    if (!tab.siteId) return;

    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    const failedHost = urlObj.hostname;
    const failedSld = getDomain(failedHost);
    if (failedSld === tab.host) return;  // same SLD = should already be covered

    // Strong signal: a request from a proxied page failed at a non-covered
    // host with a block-like error. Push it to discover IMMEDIATELY.
    daemonClient.discover(tab.siteId, [failedHost]);
  },
  { urls: ["<all_urls>"] }
);

chrome.webRequest.onErrorOccurred.addListener(
  async (details) => {
    if (details.tabId < 0) return;
    if (details.type !== "main_frame") return;
    if (!SUSPICIOUS_ERRORS.has(details.error)) return;

    let urlObj;
    try { urlObj = new URL(details.url); } catch { return; }
    const failedHost = getDomain(urlObj.hostname);
    if (blockDismissals.has(failedHost)) return;

    // Verify: ask daemon to test the URL through the tunnel.
    const r = await daemonClient.test(details.url);
    if (!r.ok || !r.data.likely_blocked) return;

    // Push the "blocked" state to the failed tab. The content script
    // will render the banner.
    tabState.set(details.tabId, { state: "blocked", host: failedHost });
    pushStateToTab(details.tabId);
  },
  { urls: ["<all_urls>"] }
);

// ---------- messages ----------

chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "set_token") {
    setToken(msg.token).then(async () => {
      const ok = await daemonClient.ping();
      sendResponse({ ok });
      // Refresh all tabs after pairing.
      chrome.tabs.query({}, (tabs) => tabs.forEach((t) => refreshTabState(t.id, t.url)));
    });
    return true;
  }

  if (msg.type === "clear_token") {
    clearToken().then(() => sendResponse({ ok: true }));
    return true;
  }

  if (msg.type === "get_state") {
    const tabId = sender.tab?.id;
    sendResponse(tabState.get(tabId) || { state: "idle" });
    return false;
  }

  if (msg.type === "ping_daemon") {
    daemonClient.ping().then((up) => sendResponse({ up }));
    return true;
  }

  if (msg.type === "popup_get_state") {
    daemonClient.match(msg.host).then((r) => {
      if (!r.ok) {
        sendResponse({ state: "daemon_down" });
        return;
      }
      const data = r.data;
      if (!data.in_catalog) {
        sendResponse({ state: "not_in_catalog", host: msg.host });
        return;
      }
      if (data.proxy_enabled === false) {
        sendResponse({ state: "catalog_disabled", host: msg.host, site_id: data.site_id });
        return;
      }
      sendResponse({ state: "proxied", host: msg.host, site_id: data.site_id });
    });
    return true;
  }

  if (msg.type === "popup_add_site") {
    daemonClient.add(msg.host, msg.host).then((r) => {
      if (!r.ok) {
        sendResponse({ ok: false, error: r.error });
        return;
      }
      const siteId = r.data.site_id;
      // If the popup told us which tab it was opened from, kick off
      // discovery for that tab. Mirrors handleAddCurrentSite's flow
      // for the in-page add banner: subsequent subresource requests
      // (video CDN, subtitle host, etc.) get queued and flushed to
      // /sites/discover so the user doesn't have to manually add each
      // auxiliary domain. Only applies when the tab is actually on the
      // site being added — the popup's "not_in_catalog" path enforces
      // this by using the active tab's host.
      if (typeof msg.tabId === "number") {
        tabState.set(msg.tabId, {
          state: "discovering",
          host: msg.host,
          siteId,
          discoveredCount: 0,
        });
        pushStateToTab(msg.tabId);
        startDiscovery(msg.tabId, msg.host, siteId);
      }
      sendResponse({ ok: true, site_id: siteId });
    });
    return true;
  }

  if (msg.type === "popup_set_enabled") {
    daemonClient.setEnabled(msg.site_id, msg.enabled).then((r) => {
      if (!r.ok) {
        sendResponse({ ok: false, error: r.error });
        return;
      }
      sendResponse({ ok: true });
    });
    return true;
  }

  if (msg.type === "add_current_site") {
    handleAddCurrentSite(sender.tab);
    return false;
  }

  if (msg.type === "add_current_site_and_reload") {
    handleAddSiteAndReload(sender.tab);
    return false;
  }

  if (msg.type === "dismiss_block") {
    persistDismissal(msg.host);
    return false;
  }

  if (msg.type === "finish_discovery") {
    // User clicked "Finish scanning" in the panel — stop the discovery
    // session for this tab and drop it into plain "proxied" state so the
    // panel hides its action buttons. Per-tab only; if the user reloads
    // or revisits the site, no new discovery session starts automatically
    // (startDiscovery only fires on initial add), so "больше не
    // сканировать" is satisfied without any extra persistence.
    const tabId = sender.tab?.id;
    if (typeof tabId === "number") {
      stopDiscovery(tabId);
      const st = tabState.get(tabId);
      if (st && st.state === "discovering") {
        tabState.set(tabId, { state: "proxied", host: st.host, siteId: st.siteId });
        pushStateToTab(tabId);
      }
    }
    return false;
  }
});

async function handleAddCurrentSite(tab) {
  if (!tab || !tab.url) return;
  let host;
  try {
    host = getDomain(new URL(tab.url).hostname);
  } catch {
    return;
  }

  tabState.set(tab.id, { state: "discovering", host, discoveredCount: 0 });
  pushStateToTab(tab.id);

  const r = await daemonClient.add(host, host);
  if (!r.ok) {
    tabState.set(tab.id, { state: "add", host });
    pushStateToTab(tab.id);
    return;
  }
  const siteId = r.data.site_id;
  tabState.set(tab.id, { state: "discovering", host, siteId, discoveredCount: 0 });
  pushStateToTab(tab.id);
  startDiscovery(tab.id, host, siteId);
}

async function handleAddSiteAndReload(tab) {
  if (!tab) return;
  const state = tabState.get(tab.id);
  if (!state || state.state !== "blocked") return;

  const r = await daemonClient.add(state.host, state.host);
  if (!r.ok) {
    console.warn("[proxyness] add failed:", r);
    return;
  }
  const siteId = r.data.site_id;

  // Brief pause to let daemon's PAC update.
  await new Promise((res) => setTimeout(res, 500));

  tabState.set(tab.id, { state: "proxied", host: state.host, siteId });
  pushStateToTab(tab.id);

  // Reload the tab so the request goes through the proxy this time.
  chrome.tabs.reload(tab.id);
}

// ---------- icon state ----------

const ICON_OFF = {
  16: "icons/icon-16.png",
  32: "icons/icon-32.png",
  48: "icons/icon-48.png",
  128: "icons/icon-128.png",
};
const ICON_ON = {
  16: "icons/icon-on-16.png",
  32: "icons/icon-on-32.png",
  48: "icons/icon-on-48.png",
  128: "icons/icon-on-128.png",
};

let lastIconConnected = false;

function setIconConnected(connected) {
  if (connected === lastIconConnected) return;
  lastIconConnected = connected;
  chrome.action.setIcon({ path: connected ? ICON_ON : ICON_OFF });
}

async function pollDaemonStatus() {
  try {
    const resp = await fetch("http://127.0.0.1:9090/status");
    if (resp.ok) {
      const data = await resp.json();
      setIconConnected(data.status === "connected");
    } else {
      setIconConnected(false);
    }
  } catch {
    setIconConnected(false);
  }
}

// Poll every 5 seconds while the service worker is alive.
setInterval(pollDaemonStatus, 5000);
// Alarm wakes the service worker after it goes idle (min 30s in Chrome).
chrome.alarms.create("poll-status", { periodInMinutes: 0.5 });
chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === "poll-status") pollDaemonStatus();
});
pollDaemonStatus();

console.log("[proxyness] service worker loaded");
