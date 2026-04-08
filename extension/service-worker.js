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
    tabState.set(tabId, { state: "proxied", host, siteId: data.site_id });
  } else {
    tabState.set(tabId, { state: "add", host, siteId: data.site_id });
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
  await daemonClient.discover(s.siteId, domains);
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
    disc.queue.add(host);
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

  if (msg.type === "add_current_site") {
    handleAddCurrentSite(sender.tab);
    return false;
  }

  // Handlers for "add_current_site_and_reload",
  // "dismiss_block" arrive in Task 18.
});

async function handleAddCurrentSite(tab) {
  if (!tab || !tab.url) return;
  let host;
  try {
    host = getDomain(new URL(tab.url).hostname);
  } catch {
    return;
  }

  tabState.set(tab.id, { state: "discovering", host });
  pushStateToTab(tab.id);

  const r = await daemonClient.add(host, host);
  if (!r.ok) {
    tabState.set(tab.id, { state: "add", host });
    pushStateToTab(tab.id);
    return;
  }
  const siteId = r.data.site_id;
  tabState.set(tab.id, { state: "discovering", host, siteId });
  pushStateToTab(tab.id);
  startDiscovery(tab.id, host, siteId);
}

console.log("[smurov-proxy] service worker loaded");
