// Content script: injected into every page. Creates a Shadow DOM root,
// renders a small floating panel, listens for state updates from the
// service worker. Panel is draggable and can be hidden from either the
// popup or its own close button — both preferences persist via
// chrome.storage.local.

(function () {
  if (window.__smurovProxyInjected) return;
  window.__smurovProxyInjected = true;

  // Create the host element and shadow root.
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;bottom:16px;right:16px;z-index:2147483647;width:0;height:0;";
  document.documentElement.appendChild(host);
  const shadow = host.attachShadow({ mode: "open" });

  shadow.innerHTML = `
    <style>
      :host { all: initial; }
      .panel {
        position: fixed; bottom: 16px; right: 16px;
        background: #0b0f1a; color: #e8eaf0;
        border: 1px solid #2a3042; border-radius: 8px;
        padding: 10px 28px 10px 14px;
        font-family: -apple-system, system-ui, sans-serif;
        font-size: 13px; line-height: 1.4;
        box-shadow: 0 4px 16px rgba(0,0,0,0.4);
        max-width: 300px;
        opacity: 0;
        transition: opacity 0.2s;
        cursor: grab;
        user-select: none;
      }
      .panel.visible { opacity: 1; }
      .panel.collapsed { padding: 8px 28px 8px 12px; }
      .panel.dragging { cursor: grabbing; transition: none; }
      .row { display: flex; align-items: center; gap: 8px; }
      .icon { width: 14px; height: 14px; border-radius: 50%; flex-shrink: 0; }
      .icon.green { background: #22c55e; }
      .icon.gray  { background: #6b7280; }
      .icon.red   { background: #ef4444; }
      .icon.yellow { background: #eab308; }
      .label { font-weight: 500; }
      .actions { display: flex; gap: 6px; margin-top: 8px; flex-wrap: wrap; }
      button {
        background: #3b82f6; color: #fff; border: none;
        padding: 6px 10px; border-radius: 4px; cursor: pointer;
        font-size: 12px; font-weight: 500;
        font-family: inherit;
      }
      button:hover { background: #2563eb; }
      button.dismiss { background: #374151; }
      button.dismiss:hover { background: #4b5563; }
      .hint { color: #9ca3af; font-size: 11px; margin-top: 6px; }
      .count { color: #eab308; font-size: 11px; margin-top: 4px; font-weight: 500; }
      .close {
        position: absolute;
        top: 4px; right: 4px;
        background: transparent;
        color: #6b7280;
        border: none;
        cursor: pointer;
        font-size: 16px;
        line-height: 1;
        padding: 2px 6px;
        font-weight: 400;
      }
      .close:hover { background: transparent; color: #e8eaf0; }
    </style>
    <div class="panel collapsed" id="panel">
      <button class="close" id="close" title="Hide panel">×</button>
      <div class="row">
        <div class="icon gray" id="icon"></div>
        <div class="label" id="label">…</div>
      </div>
      <div id="hint" class="hint" style="display:none;"></div>
      <div id="count" class="count" style="display:none;"></div>
      <div id="actions" class="actions" style="display:none;"></div>
    </div>
  `;

  const panel = shadow.getElementById("panel");
  const iconEl = shadow.getElementById("icon");
  const labelEl = shadow.getElementById("label");
  const actionsEl = shadow.getElementById("actions");
  const hintEl = shadow.getElementById("hint");
  const countEl = shadow.getElementById("count");
  const closeBtn = shadow.getElementById("close");

  // Panel prefs (loaded from storage, shared across tabs).
  let panelVisible = true;
  let panelPosition = null; // {x, y} in pixels, or null for default bottom-right
  let lastState = null;

  function applyPanelPosition() {
    if (panelPosition && typeof panelPosition.x === "number" && typeof panelPosition.y === "number") {
      panel.style.left = `${panelPosition.x}px`;
      panel.style.top = `${panelPosition.y}px`;
      panel.style.right = "auto";
      panel.style.bottom = "auto";
    } else {
      panel.style.left = "auto";
      panel.style.top = "auto";
      panel.style.right = "16px";
      panel.style.bottom = "16px";
    }
  }

  // Load prefs on startup.
  chrome.storage.local.get(["panel_visible", "panel_position"], (data) => {
    panelVisible = data.panel_visible !== false; // default true
    panelPosition = data.panel_position || null;
    applyPanelPosition();
    if (lastState) render(lastState);
  });

  // React to changes made from the popup or another tab's content script.
  chrome.storage.onChanged.addListener((changes, area) => {
    if (area !== "local") return;
    if (changes.panel_visible) {
      panelVisible = changes.panel_visible.newValue !== false;
      if (lastState) render(lastState);
    }
    if (changes.panel_position) {
      panelPosition = changes.panel_position.newValue || null;
      applyPanelPosition();
    }
  });

  // Close button hides the panel globally via shared storage. Popup
  // picks it up via onChanged and flips its "show panel" toggle.
  closeBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    panelVisible = false;
    panel.classList.remove("visible");
    chrome.storage.local.set({ panel_visible: false });
  });

  // --- Dragging ---
  //
  // Whole panel is a drag handle, except buttons. composedPath() is
  // needed because the click lands inside the shadow root — without it
  // we couldn't see which inner element was hit. Listeners for move/up
  // go on window so a fast drag that leaves the panel still tracks.
  let dragging = false;
  let dragStartX = 0, dragStartY = 0;
  let panelStartX = 0, panelStartY = 0;

  function onMouseDown(e) {
    if (e.button !== 0) return; // left click only
    const path = e.composedPath();
    const hitButton = path.some((el) => el.tagName === "BUTTON");
    if (hitButton) return;
    const rect = panel.getBoundingClientRect();
    dragging = true;
    dragStartX = e.clientX;
    dragStartY = e.clientY;
    panelStartX = rect.left;
    panelStartY = rect.top;
    panel.classList.add("dragging");
    window.addEventListener("mousemove", onMouseMove);
    window.addEventListener("mouseup", onMouseUp);
    e.preventDefault();
  }

  function onMouseMove(e) {
    if (!dragging) return;
    const dx = e.clientX - dragStartX;
    const dy = e.clientY - dragStartY;
    const pw = panel.offsetWidth;
    const ph = panel.offsetHeight;
    // Clamp inside the viewport so the panel can't be dragged off-screen.
    const x = Math.max(0, Math.min(window.innerWidth - pw, panelStartX + dx));
    const y = Math.max(0, Math.min(window.innerHeight - ph, panelStartY + dy));
    panelPosition = { x, y };
    applyPanelPosition();
  }

  function onMouseUp() {
    if (!dragging) return;
    dragging = false;
    panel.classList.remove("dragging");
    window.removeEventListener("mousemove", onMouseMove);
    window.removeEventListener("mouseup", onMouseUp);
    if (panelPosition) {
      chrome.storage.local.set({ panel_position: panelPosition });
    }
  }

  panel.addEventListener("mousedown", onMouseDown);

  // --- Rendering ---

  function render(s) {
    lastState = s;

    if (!panelVisible) {
      panel.classList.remove("visible");
      return;
    }

    // Reset dynamic bits before each render.
    actionsEl.innerHTML = "";
    actionsEl.style.display = "none";
    hintEl.style.display = "none";
    hintEl.textContent = "";
    countEl.style.display = "none";
    countEl.textContent = "";
    panel.classList.add("collapsed");

    switch (s.state) {
      case "down":
        iconEl.className = "icon red";
        labelEl.textContent = "Daemon not running";
        hintEl.textContent = "Open the Smurov Proxy desktop app";
        hintEl.style.display = "block";
        panel.classList.remove("collapsed");
        panel.classList.add("visible");
        break;

      case "proxied":
        iconEl.className = "icon green";
        labelEl.textContent = `✓ ${s.host} proxied`;
        panel.classList.add("visible");
        break;

      case "discovering":
        iconEl.className = "icon yellow";
        labelEl.textContent = `${s.host} · discovering domains`;
        hintEl.textContent = "Browse the site and start videos — I'll pick up the CDNs and dynamic hosts you need.";
        hintEl.style.display = "block";
        if (typeof s.discoveredCount === "number" && s.discoveredCount > 0) {
          const n = s.discoveredCount;
          countEl.textContent = `Added ${n} new domain${n === 1 ? "" : "s"}`;
          countEl.style.display = "block";
          const reloadBtn = document.createElement("button");
          reloadBtn.textContent = "Reload page";
          reloadBtn.addEventListener("click", (e) => {
            e.stopPropagation();
            location.reload();
          });
          actionsEl.appendChild(reloadBtn);
        }
        // "Finish scanning" is available throughout discovery, even before
        // any domains have been picked up — the user may want to dismiss
        // the panel on a site where discovery isn't going to find anything.
        const finishBtn = document.createElement("button");
        finishBtn.className = "dismiss";
        finishBtn.textContent = "Finish scanning";
        finishBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "finish_discovery" });
        });
        actionsEl.appendChild(finishBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        panel.classList.add("visible");
        break;

      case "add": {
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} not in proxy`;
        const addBtn = document.createElement("button");
        addBtn.textContent = "Add to proxy";
        addBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "add_current_site" });
        });
        actionsEl.appendChild(addBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        panel.classList.add("visible");
        break;
      }

      case "catalog_disabled": {
        iconEl.className = "icon gray";
        labelEl.textContent = `${s.host} off`;
        const enableBtn = document.createElement("button");
        enableBtn.textContent = "Enable";
        enableBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage(
            { type: "popup_set_enabled", site_id: s.siteId, enabled: true },
            (resp) => {
              if (resp?.ok) location.reload();
            },
          );
        });
        actionsEl.appendChild(enableBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        panel.classList.add("visible");
        break;
      }

      case "blocked": {
        iconEl.className = "icon red";
        labelEl.textContent = `${s.host} blocked`;
        const fixBtn = document.createElement("button");
        fixBtn.textContent = "Add to proxy";
        fixBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "add_current_site_and_reload" });
        });
        actionsEl.appendChild(fixBtn);
        const dismissBtn = document.createElement("button");
        dismissBtn.className = "dismiss";
        dismissBtn.textContent = "Dismiss";
        dismissBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "dismiss_block", host: s.host });
          panel.classList.remove("visible");
        });
        actionsEl.appendChild(dismissBtn);
        actionsEl.style.display = "flex";
        panel.classList.remove("collapsed");
        panel.classList.add("visible");
        break;
      }

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
