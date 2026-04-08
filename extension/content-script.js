// Content script: injected into every page. Creates a Shadow DOM root,
// renders a small floating panel, listens for state updates from the
// service worker.

(function () {
  if (window.__smurovProxyInjected) return;
  window.__smurovProxyInjected = true;

  // Create the host element and shadow root.
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;bottom:16px;right:16px;z-index:2147483647;width:0;height:0;";
  document.documentElement.appendChild(host);
  const shadow = host.attachShadow({ mode: "open" });

  // Inject the panel HTML + styles.
  shadow.innerHTML = `
    <style>
      :host { all: initial; }
      .panel {
        position: fixed; bottom: 16px; right: 16px;
        background: #0b0f1a; color: #e8eaf0;
        border: 1px solid #2a3042; border-radius: 8px;
        padding: 10px 14px;
        font-family: -apple-system, system-ui, sans-serif;
        font-size: 13px; line-height: 1.4;
        box-shadow: 0 4px 16px rgba(0,0,0,0.4);
        max-width: 280px;
        opacity: 0;
        transition: opacity 0.2s;
      }
      .panel.visible { opacity: 1; }
      .panel.collapsed { padding: 8px 12px; }
      .row { display: flex; align-items: center; gap: 8px; }
      .icon { width: 14px; height: 14px; border-radius: 50%; flex-shrink: 0; }
      .icon.green { background: #22c55e; }
      .icon.gray  { background: #6b7280; }
      .icon.red   { background: #ef4444; }
      .icon.yellow { background: #eab308; }
      .label { font-weight: 500; }
      .actions { display: flex; gap: 6px; margin-top: 8px; }
      button {
        background: #3b82f6; color: #fff; border: none;
        padding: 6px 10px; border-radius: 4px; cursor: pointer;
        font-size: 12px; font-weight: 500;
      }
      button.dismiss { background: #374151; }
      .hint { color: #9ca3af; font-size: 11px; margin-top: 4px; }
    </style>
    <div class="panel collapsed" id="panel">
      <div class="row">
        <div class="icon gray" id="icon"></div>
        <div class="label" id="label">…</div>
      </div>
      <div id="actions" class="actions" style="display:none;"></div>
      <div id="hint" class="hint" style="display:none;"></div>
    </div>
  `;

  const panel = shadow.getElementById("panel");
  const iconEl = shadow.getElementById("icon");
  const labelEl = shadow.getElementById("label");
  const actionsEl = shadow.getElementById("actions");
  const hintEl = shadow.getElementById("hint");

  // Render a state object: { state, host, ... }
  function render(s) {
    panel.classList.add("visible");
    actionsEl.innerHTML = "";
    actionsEl.style.display = "none";
    hintEl.style.display = "none";
    panel.classList.add("collapsed");

    switch (s.state) {
      case "down":
        iconEl.className = "icon red";
        labelEl.textContent = "Daemon not running";
        hintEl.textContent = "Open the Smurov Proxy desktop app";
        hintEl.style.display = "block";
        panel.classList.remove("collapsed");
        break;
      case "proxied":
        iconEl.className = "icon green";
        labelEl.textContent = `✓ ${s.host} proxied`;
        break;
      case "discovering":
        iconEl.className = "icon yellow";
        labelEl.textContent = `${s.host} · discovering…`;
        break;
      case "add":
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} not in proxy`;
        const addBtn = document.createElement("button");
        addBtn.textContent = "Add to proxy";
        addBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "add_current_site" });
        });
        actionsEl.appendChild(addBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
      case "catalog_disabled":
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} off`;
        const enableBtn = document.createElement("button");
        enableBtn.textContent = "Enable";
        enableBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({
            type: "popup_set_enabled",
            site_id: s.siteId,
            enabled: true,
          }, (resp) => {
            if (resp?.ok) location.reload();
          });
        });
        actionsEl.appendChild(enableBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
      case "blocked":
        iconEl.className = "icon red";
        labelEl.textContent = `${s.host} blocked`;
        const fixBtn = document.createElement("button");
        fixBtn.textContent = "Add to proxy";
        fixBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "add_current_site_and_reload" });
        });
        actionsEl.appendChild(fixBtn);
        const dismissBtn = document.createElement("button");
        dismissBtn.className = "dismiss";
        dismissBtn.textContent = "Dismiss";
        dismissBtn.addEventListener("click", () => {
          chrome.runtime.sendMessage({ type: "dismiss_block", host: s.host });
          panel.classList.remove("visible");
        });
        actionsEl.appendChild(dismissBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        break;
      default:
        panel.classList.remove("visible");
    }
  }

  // Initial state query.
  chrome.runtime.sendMessage({ type: "get_state" }, (state) => {
    if (state) render(state);
  });

  // Listen for push updates from service worker.
  chrome.runtime.onMessage.addListener((msg) => {
    if (msg.type === "state_update") {
      render(msg.state);
    }
  });
})();
