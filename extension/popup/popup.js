const root = document.getElementById("root");

// ----- SVG constants -----

const SVG_GHOST_OPEN = `<svg class="p-head-logo" viewBox="0 0 100 100" fill="none"><path d="M50 10 C25 10, 10 30, 10 55 L10 90 L25 75 L40 90 L50 80 L60 90 L75 75 L90 90 L90 55 C90 30, 75 10, 50 10Z" fill="oklch(0.60 0.01 250)"/><ellipse cx="50" cy="48" rx="16" ry="14" fill="oklch(0.68 0.12 75)"/><ellipse cx="50" cy="48" rx="8" ry="7" fill="oklch(0.25 0.02 75)"/></svg>`;
const SVG_GHOST_CLOSED = `<svg class="p-head-logo" viewBox="0 0 100 100" fill="none"><path d="M50 10 C25 10, 10 30, 10 55 L10 90 L25 75 L40 90 L50 80 L60 90 L75 75 L90 90 L90 55 C90 30, 75 10, 50 10Z" fill="oklch(0.40 0.01 250)"/><ellipse cx="50" cy="48" rx="16" ry="12" fill="oklch(0.25 0.014 250)"/></svg>`;
const SVG_ICON_PLUS = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>`;
const SVG_ICON_EYE_SLASH = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M1 8c2.5-4 5-5 7-5s4.5 1 7 5c-2.5 4-5 5-7 5s-4.5-1-7-5z"/><line x1="2" y1="14" x2="14" y2="2"/></svg>`;
const SVG_ICON_EYE = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M1 8c2.5-4 5-5 7-5s4.5 1 7 5c-2.5 4-5 5-7 5s-4.5-1-7-5z"/><circle cx="8" cy="8" r="2"/></svg>`;
const SVG_ICON_X = `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg>`;
const SVG_DOWN_EXCL = `<svg viewBox="0 0 20 20" fill="none" stroke="oklch(0.62 0.19 25)" stroke-width="1.8" stroke-linecap="round"><circle cx="10" cy="10" r="7"/><path d="M10 6.5v4"/><circle cx="10" cy="14" r="0.5" fill="oklch(0.62 0.19 25)" stroke="none"/></svg>`;

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
    <div class="p">
      <div class="p-head">
        ${SVG_GHOST_CLOSED}
        <span class="p-head-name">Proxyness</span>
        <span class="p-head-dot off"></span>
      </div>
      <div class="p-body">
        <div class="p-pair-title">Connect to desktop app</div>
        <div class="p-pair-sub">Open the Proxyness app, go to Settings &rarr; Extension, and copy the pairing token.</div>
        <input class="p-input" type="text" id="token" placeholder="Paste token here..." autofocus>
        <button class="p-btn primary" id="pair">Pair</button>
        <div id="msg" class="p-error">${initialError ? escapeHtml(initialError) : ''}</div>
      </div>
    </div>
  `;
  document.getElementById("pair").addEventListener("click", async () => {
    const token = document.getElementById("token").value.trim();
    const msg = document.getElementById("msg");
    if (token.length !== 64) {
      msg.textContent = "Token should be 64 hex characters.";
      return;
    }
    msg.textContent = "Pairing\u2026";
    const ok = await tryPair(token);
    if (ok) {
      render();
    } else {
      msg.textContent = "Pairing failed. Is the desktop app running?";
    }
  });
}

async function tryPair(token) {
  const resp = await sendToSW({ type: "set_token", token });
  return resp?.ok === true;
}

function renderDaemonDown() {
  root.innerHTML = `
    <div class="p">
      <div class="p-head">
        ${SVG_GHOST_CLOSED}
        <span class="p-head-name">Proxyness</span>
        <span class="p-head-dot err"></span>
      </div>
      <div class="p-body" style="text-align:center; padding:24px 16px;">
        <div class="p-down-icon">${SVG_DOWN_EXCL}</div>
        <div class="p-down-title">App not running</div>
        <div class="p-down-sub">Start the Proxyness desktop app to use the extension.</div>
      </div>
      <div class="p-foot">
        <span class="spacer"></span>
        <a class="p-foot-btn danger" href="#" id="unpair">
          ${SVG_ICON_X}
          Unpair
        </a>
      </div>
    </div>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

// renderManualEntry handles the case where we couldn't detect a host on the
// active tab — chrome:// pages, new tab, chrome-error pages with no usable
// URL. Lets the user paste a domain manually and proxy it.
function renderManualEntry(state, prefill, opts = {}) {
  const tabId = state?.tabId;
  const panelToggleLabel = opts.panelVisible ? "Hide panel" : "Show panel";
  const panelToggleSvg = opts.panelVisible ? SVG_ICON_EYE_SLASH : SVG_ICON_EYE;
  root.innerHTML = `
    <div class="p">
      <div class="p-head">
        ${SVG_GHOST_OPEN}
        <span class="p-head-name">Proxyness</span>
        <span class="p-head-dot on"></span>
      </div>
      <div class="p-body">
        <div class="p-pair-title">Add a site</div>
        <div class="p-pair-sub">Enter a domain to route through proxy.</div>
        <input class="p-input" type="text" id="manual-host" placeholder="example.com" value="${escapeHtml(prefill || "")}" autofocus>
        <button class="p-btn primary" id="manual-add">Proxy this site</button>
        <div id="msg" class="p-error"></div>
      </div>
      <div class="p-foot">
        <a class="p-foot-btn" href="#" id="panel-toggle">
          ${panelToggleSvg}
          ${panelToggleLabel}
        </a>
        <span class="spacer"></span>
        <a class="p-foot-btn danger" href="#" id="unpair">
          ${SVG_ICON_X}
          Unpair
        </a>
      </div>
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
  let tagClass = "";
  let tagLabel = "";
  let buttonText = "";
  let buttonClass = "";
  let buttonHandler = null;
  const panelToggleLabel = opts.panelVisible ? "Hide panel" : "Show panel";
  const panelToggleSvg = opts.panelVisible ? SVG_ICON_EYE_SLASH : SVG_ICON_EYE;

  if (state.state === "not_in_catalog") {
    tagClass = "not-proxied";
    tagLabel = "Not proxied";
    buttonText = "Proxy this site";
    buttonClass = "primary";
    buttonHandler = async () => {
      // Give the desktop client's sites-cache poller (500ms) + macOS PAC
      // URL propagation a brief moment before we reload the tab. Without
      // this pause, Chrome reloads on the *old* PAC (still without the
      // just-added host) and the first page load fails, leaving the user
      // staring at a spinner until Chrome eventually re-reads the fresh
      // PAC on its own schedule.
      const actionBtn = document.getElementById("action");
      actionBtn.textContent = "Setting up proxy\u2026";
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
    tagClass = "proxied";
    tagLabel = "Proxied";
    buttonText = "Disable proxy";
    buttonClass = "secondary";
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
    tagClass = "disabled";
    tagLabel = "Disabled";
    buttonText = "Enable proxy";
    buttonClass = "primary";
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
    <div class="p">
      <div class="p-head">
        ${SVG_GHOST_OPEN}
        <span class="p-head-name">Proxyness</span>
        <span class="p-head-dot on"></span>
      </div>
      <div class="p-body">
        <div class="p-domain">${escapeHtml(state.host || "")}</div>
        <div class="p-tag ${tagClass}"><span class="dot"></span> ${tagLabel}</div>
        <button class="p-btn ${buttonClass}" id="action">${buttonText}</button>
        <div id="msg" class="p-error"></div>
      </div>
      <div class="p-foot">
        <a class="p-foot-btn" href="#" id="manual">
          ${SVG_ICON_PLUS}
          Add site
        </a>
        <a class="p-foot-btn" href="#" id="panel-toggle">
          ${panelToggleSvg}
          ${panelToggleLabel}
        </a>
        <span class="spacer"></span>
        <a class="p-foot-btn danger" href="#" id="unpair">
          ${SVG_ICON_X}
          Unpair
        </a>
      </div>
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
