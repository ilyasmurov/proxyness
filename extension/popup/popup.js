const root = document.getElementById("root");

// ----- helpers -----

function sendToSW(msg) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage(msg, (resp) => resolve(resp));
  });
}

async function getStoredToken() {
  const r = await chrome.storage.local.get("daemon_token");
  return r.daemon_token || null;
}

// getPanelVisible reads the shared panel-visibility flag that content
// scripts watch via chrome.storage.onChanged. Default is true (visible)
// so a fresh install still shows the on-page panel.
async function getPanelVisible() {
  const r = await chrome.storage.local.get("panel_visible");
  return r.panel_visible !== false;
}

async function togglePanelVisible() {
  const current = await getPanelVisible();
  await chrome.storage.local.set({ panel_visible: !current });
}

function getDomain(host) {
  const parts = host.split(".");
  if (parts.length <= 2) return host;
  const secondLevel = new Set(["co", "com", "org", "net", "ac", "gov", "edu"]);
  if (parts.length >= 3 && secondLevel.has(parts[parts.length - 2])) {
    return parts.slice(-3).join(".");
  }
  return parts.slice(-2).join(".");
}

// normalizeHost takes raw user input ("https://kinovod.pro/foo", " WWW.HABR.COM ", etc.)
// and returns a clean lowercase domain ("kinovod.pro", "habr.com").
function normalizeHost(raw) {
  let host = String(raw || "").trim().toLowerCase();
  if (!host) return "";
  host = host.replace(/^https?:\/\//, "").replace(/\/.*$/, "").replace(/^www\./, "");
  return host;
}

// extractHostFromUrl tries to parse a URL and returns its registrable domain
// (e.g. "https://m.kinovod.pro/foo" -> "kinovod.pro"). Returns null on failure.
function extractHostFromUrl(rawUrl) {
  if (!rawUrl) return null;
  let url;
  try { url = new URL(rawUrl); } catch { return null; }
  if (url.protocol !== "http:" && url.protocol !== "https:") return null;
  return getDomain(url.hostname);
}

// Try multiple sources to find what host the user is trying to reach.
// Chrome error pages keep the original URL in tab.url for most cases, but
// some interstitials (cert errors, etc.) report tab.url as
// chrome-error://chromewebdata/. In those cases tab.pendingUrl or
// tab.title may still hold the failed hostname.
function detectHostForTab(tab) {
  const fromUrl = extractHostFromUrl(tab?.url);
  if (fromUrl) return fromUrl;
  const fromPending = extractHostFromUrl(tab?.pendingUrl);
  if (fromPending) return fromPending;
  // Last-ditch: tab.title sometimes contains a bare hostname like "kinovod.pro"
  // for connection-error pages. Only accept if it looks like a domain.
  const title = (tab?.title || "").trim();
  if (/^[a-z0-9-]+(\.[a-z0-9-]+)+$/i.test(title)) {
    return title.toLowerCase();
  }
  return null;
}

async function loadActiveTabState() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab) return { state: "manual_entry" };
  const tabId = tab.id;

  const host = detectHostForTab(tab);
  if (!host) {
    // No usable host (chrome:// page, new tab, unparseable error page).
    // Offer manual entry instead of a dead-end "system_page" view.
    return { state: "manual_entry", tabId };
  }

  const resp = await sendToSW({ type: "popup_get_state", host });
  return { ...resp, tabId };
}

// ----- views -----

function renderPairing(initialError) {
  root.innerHTML = `
    <div class="title">Pair with Smurov Proxy</div>
    <div class="subtitle">
      Open the Smurov Proxy desktop client → Browser Extension tab,
      copy the token, paste it below.
    </div>
    <input type="text" id="token" placeholder="abc123..." autofocus>
    <button id="pair">Pair</button>
    <div id="msg" class="${initialError ? 'error' : ''}">${initialError || ''}</div>
  `;
  document.getElementById("pair").addEventListener("click", async () => {
    const token = document.getElementById("token").value.trim();
    const msg = document.getElementById("msg");
    if (token.length !== 64) {
      msg.textContent = "Token should be 64 hex characters.";
      msg.className = "error";
      return;
    }
    msg.textContent = "Pairing…";
    msg.className = "";
    const ok = await tryPair(token);
    if (ok) {
      render();
    } else {
      msg.textContent = "Pairing failed. Is the daemon running? Token correct?";
      msg.className = "error";
    }
  });
}

async function tryPair(token) {
  const resp = await sendToSW({ type: "set_token", token });
  return resp?.ok === true;
}

function renderDaemonDown() {
  root.innerHTML = `
    <div class="title">Daemon not running</div>
    <div class="subtitle">Start the Smurov Proxy desktop client.</div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

// renderManualEntry handles the case where we couldn't detect a host on the
// active tab — chrome:// pages, new tab, chrome-error pages with no usable
// URL. Lets the user paste a domain manually and proxy it.
function renderManualEntry(state, prefill, opts = {}) {
  const tabId = state?.tabId;
  const panelToggleText = opts.panelVisible ? "Hide panel" : "Show panel";
  root.innerHTML = `
    <div class="title">Add a site to proxy</div>
    <div class="subtitle">Paste a domain to add it to your proxy list.</div>
    <input type="text" id="manual-host" placeholder="kinovod.pro" value="${escapeHtml(prefill || "")}" autofocus>
    <button id="manual-add" class="action">Proxy this site</button>
    <div id="msg" class="error"></div>
    <div class="footer">
      <a href="#" id="panel-toggle" class="muted">${panelToggleText}</a>
      &nbsp;·&nbsp;
      <a href="#" id="unpair" class="muted">Unpair</a>
    </div>
  `;
  const input = document.getElementById("manual-host");
  const submit = async () => {
    const host = normalizeHost(input.value);
    if (!host) {
      showError("Enter a domain (e.g. kinovod.pro)");
      return;
    }
    const resp = await sendToSW({ type: "popup_add_site", host });
    if (resp?.ok) {
      // If we have a tab id, reload it so the user lands on the now-proxied page.
      if (tabId != null) chrome.tabs.reload(tabId);
      window.close();
    } else {
      showError(resp?.error || "Failed to add");
    }
  };
  document.getElementById("manual-add").addEventListener("click", submit);
  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") submit();
  });
  document.getElementById("panel-toggle").addEventListener("click", async (e) => {
    e.preventDefault();
    await togglePanelVisible();
    render();
  });
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function renderControlPanel(state, opts = {}) {
  // state: { state, host, site_id?, tabId }
  let statusLine = "";
  let buttonText = "";
  let buttonHandler = null;
  const panelToggleText = opts.panelVisible ? "Hide panel" : "Show panel";

  if (state.state === "not_in_catalog") {
    statusLine = "Not proxied";
    buttonText = "Proxy this site";
    buttonHandler = async () => {
      // Give the desktop client's sites-cache poller (500ms) + macOS PAC
      // URL propagation a brief moment before we reload the tab. Without
      // this pause, Chrome reloads on the *old* PAC (still without the
      // just-added host) and the first page load fails, leaving the user
      // staring at a spinner until Chrome eventually re-reads the fresh
      // PAC on its own schedule.
      const actionBtn = document.getElementById("action");
      actionBtn.textContent = "Setting up proxy…";
      actionBtn.disabled = true;
      const resp = await sendToSW({ type: "popup_add_site", host: state.host, tabId: state.tabId });
      if (resp?.ok) {
        setTimeout(() => {
          chrome.tabs.reload(state.tabId);
          window.close();
        }, 1200);
      } else {
        actionBtn.textContent = buttonText;
        actionBtn.disabled = false;
        showError(resp?.error || "Failed to add");
        // Something's out of sync on the daemon side — refetch so the
        // popup can re-render with the state we actually have now.
        render();
      }
    };
  } else if (state.state === "proxied") {
    statusLine = "✓ Proxied";
    buttonText = "Disable proxying";
    buttonHandler = async () => {
      const resp = await sendToSW({
        type: "popup_set_enabled",
        site_id: state.site_id,
        enabled: false,
        tabId: state.tabId,
      });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        // The daemon rejected us — usually because the site was removed
        // or disabled behind our back (client UI delete, server revoke,
        // stale cached state). Refetch so the popup re-renders with
        // whatever the daemon thinks is the current truth; the user
        // sees the correct button without clicking again.
        showError(resp?.error || "Failed to disable");
        render();
      }
    };
  } else if (state.state === "catalog_disabled") {
    statusLine = "Off (locally disabled)";
    buttonText = "Enable proxying";
    buttonHandler = async () => {
      const resp = await sendToSW({
        type: "popup_set_enabled",
        site_id: state.site_id,
        enabled: true,
        tabId: state.tabId,
      });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        showError(resp?.error || "Failed to enable");
        render();
      }
    };
  }

  root.innerHTML = `
    <div class="host">${escapeHtml(state.host || "")}</div>
    <div class="status">${statusLine}</div>
    <button id="action" class="action">${buttonText}</button>
    <div id="msg" class="error"></div>
    <div class="footer">
      <a href="#" id="manual" class="muted">+ Add another site</a>
      &nbsp;·&nbsp;
      <a href="#" id="panel-toggle" class="muted">${panelToggleText}</a>
      &nbsp;·&nbsp;
      <a href="#" id="unpair" class="muted">Unpair</a>
    </div>
  `;
  document.getElementById("action").addEventListener("click", buttonHandler);
  document.getElementById("manual").addEventListener("click", (e) => {
    e.preventDefault();
    renderManualEntry({ tabId: state.tabId }, undefined, opts);
  });
  document.getElementById("panel-toggle").addEventListener("click", async (e) => {
    e.preventDefault();
    await togglePanelVisible();
    render();
  });
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function showError(text) {
  const msg = document.getElementById("msg");
  if (msg) msg.textContent = text;
}

function escapeHtml(s) {
  return String(s || "").replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

async function clearAndRender() {
  await sendToSW({ type: "clear_token" });
  render();
}

// ----- top-level dispatcher -----

async function render() {
  const token = await getStoredToken();
  if (!token) {
    renderPairing();
    return;
  }
  const panelVisible = await getPanelVisible();
  const tabState = await loadActiveTabState();
  if (tabState.state === "daemon_down") {
    renderDaemonDown();
    return;
  }
  if (tabState.state === "manual_entry") {
    renderManualEntry(tabState, undefined, { panelVisible });
    return;
  }
  renderControlPanel(tabState, { panelVisible });
}

render();
