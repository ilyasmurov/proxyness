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

function getDomain(host) {
  const parts = host.split(".");
  if (parts.length <= 2) return host;
  const secondLevel = new Set(["co", "com", "org", "net", "ac", "gov", "edu"]);
  if (parts.length >= 3 && secondLevel.has(parts[parts.length - 2])) {
    return parts.slice(-3).join(".");
  }
  return parts.slice(-2).join(".");
}

async function loadActiveTabState() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab || !tab.url) return { state: "system_page", tabId: tab?.id };
  let url;
  try { url = new URL(tab.url); } catch { return { state: "system_page", tabId: tab.id }; }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return { state: "system_page", tabId: tab.id };
  }
  const host = getDomain(url.hostname);
  const resp = await sendToSW({ type: "popup_get_state", host });
  return { ...resp, tabId: tab.id };
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

function renderSystemPage() {
  root.innerHTML = `
    <div class="title">No site to control</div>
    <div class="subtitle">Switch to a regular web page.</div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function renderControlPanel(state) {
  // state: { state, host, site_id?, tabId }
  let statusLine = "";
  let buttonText = "";
  let buttonHandler = null;

  if (state.state === "not_in_catalog") {
    statusLine = "Not proxied";
    buttonText = "Проксировать этот сайт";
    buttonHandler = async () => {
      const resp = await sendToSW({ type: "popup_add_site", host: state.host, tabId: state.tabId });
      if (resp?.ok) {
        chrome.tabs.reload(state.tabId);
        window.close();
      } else {
        showError(resp?.error || "Failed to add");
      }
    };
  } else if (state.state === "proxied") {
    statusLine = "✓ Proxied";
    buttonText = "Выключить проксирование";
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
        showError(resp?.error || "Failed to disable");
      }
    };
  } else if (state.state === "catalog_disabled") {
    statusLine = "Off (locally disabled)";
    buttonText = "Включить проксирование";
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
      }
    };
  }

  root.innerHTML = `
    <div class="host">${escapeHtml(state.host || "")}</div>
    <div class="status">${statusLine}</div>
    <button id="action" class="action">${buttonText}</button>
    <div id="msg" class="error"></div>
    <div class="footer"><a href="#" id="unpair" class="muted">Unpair</a></div>
  `;
  document.getElementById("action").addEventListener("click", buttonHandler);
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

function showError(text) {
  const msg = document.getElementById("msg");
  if (msg) msg.textContent = text;
}

function escapeHtml(s) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
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
  const tabState = await loadActiveTabState();
  if (tabState.state === "daemon_down") {
    renderDaemonDown();
    return;
  }
  if (tabState.state === "system_page") {
    renderSystemPage();
    return;
  }
  renderControlPanel(tabState);
}

render();
