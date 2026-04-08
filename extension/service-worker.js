import { daemonClient, setToken, clearToken } from "./lib/daemon-client.js";

// Per-tab state cache.
// Map<tabId, {host, state, siteId, discovering}>
const tabState = new Map();

// Listen for messages from popup (pairing) and content script (state queries).
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  if (msg.type === "set_token") {
    setToken(msg.token).then(async () => {
      const ok = await daemonClient.ping();
      sendResponse({ ok });
    });
    return true;  // async
  }

  if (msg.type === "clear_token") {
    clearToken().then(() => sendResponse({ ok: true }));
    return true;
  }

  if (msg.type === "get_state") {
    const tabId = sender.tab?.id;
    const state = tabState.get(tabId) || { state: "idle" };
    sendResponse(state);
    return false;
  }

  if (msg.type === "ping_daemon") {
    daemonClient.ping().then((up) => sendResponse({ up }));
    return true;
  }
});

// Refresh tab state on activation/navigation. Real implementation in Task 14.
chrome.tabs.onActivated.addListener(async (info) => {
  // Stub: full implementation in Task 14.
});

console.log("[smurov-proxy] service worker loaded");
