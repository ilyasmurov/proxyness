// Content script: injected into every page. Creates a Shadow DOM root,
// renders a small floating panel, listens for state updates from the
// service worker. Panel is draggable and can be hidden from either the
// popup or its own close button — both preferences persist via
// chrome.storage.local.

(function () {
  if (window.__proxynessInjected) return;
  window.__proxynessInjected = true;

  // Create the host element and shadow root.
  const host = document.createElement("div");
  host.style.cssText = "position:fixed;bottom:16px;right:16px;z-index:2147483647;width:0;height:0;";
  document.documentElement.appendChild(host);
  const shadow = host.attachShadow({ mode: "open" });

  shadow.innerHTML = `
    <style>
      @import url('https://fonts.googleapis.com/css2?family=Figtree:wght@400;500;600;700&display=swap');
      :host { all: initial; }
      .fp {
        position: fixed; bottom: 16px; right: 16px;
        font-family: 'Figtree', system-ui, sans-serif;
        font-size: 12px; color: oklch(0.93 0.006 250);
        background: oklch(0.12 0.014 250);
        border: 1px solid oklch(0.24 0.013 250);
        border-radius: 8px;
        box-shadow: 0 6px 24px oklch(0 0 0 / 0.45);
        width: max-content; max-width: 300px;
        display: flex;
        overflow: hidden;
        opacity: 0;
        transition: opacity 0.2s;
        cursor: grab;
        user-select: none;
      }
      @keyframes fp-enter { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: translateY(0); } }
      .fp.visible { opacity: 1; animation: fp-enter 0.25s ease-out; }
      .fp.dragging { cursor: grabbing; transition: none; }
      .fp-accent {
        width: 3px; flex-shrink: 0;
        transition: background 0.2s;
      }
      .fp-accent.gn { background: oklch(0.72 0.15 150); }
      .fp-accent.gr { background: oklch(0.42 0.01 250); }
      .fp-accent.am { background: oklch(0.78 0.155 75); }
      .fp-accent.rd { background: oklch(0.62 0.19 25); }
      .fp-content {
        padding: 9px 12px; flex: 1; min-width: 0;
      }
      .fp-row {
        display: flex; align-items: center; gap: 7px;
      }
      .fp-ghost {
        width: 14px; height: 14px;
        flex-shrink: 0; opacity: 0.4;
      }
      .fp-label {
        font-weight: 500; font-size: 12px;
        flex: 1; min-width: 0;
        overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
      }
      .fp-label .host { font-weight: 600; }
      .fp-label .st { color: oklch(0.60 0.012 250); margin-left: 3px; }
      .fp-close {
        width: 16px; height: 16px;
        display: flex; align-items: center; justify-content: center;
        border-radius: 3px; border: none;
        background: transparent; color: oklch(0.42 0.01 250);
        font-size: 13px; cursor: pointer;
        flex-shrink: 0; line-height: 1;
        padding: 0;
      }
      .fp-close:hover { color: oklch(0.60 0.012 250); background: oklch(0.19 0.018 250); }
      .fp-hint {
        font-size: 10px; color: oklch(0.60 0.012 250);
        margin-top: 4px; padding-left: 21px; line-height: 1.4;
      }
      .fp-count {
        font-size: 10px; font-weight: 600;
        color: oklch(0.78 0.155 75);
        margin-top: 2px; padding-left: 21px;
      }
      .fp-actions {
        display: flex; gap: 5px;
        margin-top: 6px; padding-left: 21px;
      }
      .fp-btn {
        padding: 4px 9px; border-radius: 4px; border: none;
        font-family: 'Figtree', system-ui, sans-serif;
        font-size: 10px; font-weight: 600; cursor: pointer;
      }
      .fp-btn.primary { background: oklch(0.78 0.155 75); color: oklch(0.18 0.03 75); }
      .fp-btn.primary:hover { background: oklch(0.82 0.14 75); }
      .fp-btn.ghost { background: oklch(0.19 0.018 250); color: oklch(0.60 0.012 250); }
      .fp-btn.ghost:hover { background: oklch(0.23 0.016 250); color: oklch(0.93 0.006 250); }
    </style>
    <div class="fp" id="panel">
      <div class="fp-accent" id="accent"></div>
      <div class="fp-content">
        <div class="fp-row">
          <svg class="fp-ghost" viewBox="0 0 100 100" fill="none"><path d="M50 10 C25 10, 10 30, 10 55 L10 90 L25 75 L40 90 L50 80 L60 90 L75 75 L90 90 L90 55 C90 30, 75 10, 50 10Z" fill="currentColor"/></svg>
          <div class="fp-label" id="label">…</div>
          <button class="fp-close" id="close" title="Hide panel">&times;</button>
        </div>
        <div id="hint" class="fp-hint" style="display:none;"></div>
        <div id="count" class="fp-count" style="display:none;"></div>
        <div id="actions" class="fp-actions" style="display:none;"></div>
      </div>
    </div>
  `;

  const panel = shadow.getElementById("panel");
  const accentEl = shadow.getElementById("accent");
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
    accentEl.className = "fp-accent";
    actionsEl.innerHTML = "";
    actionsEl.style.display = "none";
    hintEl.style.display = "none";
    hintEl.textContent = "";
    countEl.style.display = "none";
    countEl.textContent = "";

    switch (s.state) {
      case "down":
        accentEl.className = "fp-accent rd";
        labelEl.innerHTML = '<span class="st">App not running</span>';
        hintEl.textContent = "Start the Proxyness desktop app";
        hintEl.style.display = "block";
        panel.classList.add("visible");
        break;

      case "proxied":
        accentEl.className = "fp-accent gn";
        labelEl.innerHTML = `<span class="host">${s.host}</span> <span class="st">proxied</span>`;
        panel.classList.add("visible");
        break;

      case "discovering":
        accentEl.className = "fp-accent am";
        labelEl.innerHTML = `<span class="host">${s.host}</span> <span class="st">scanning</span>`;
        hintEl.textContent = "Browse around — I'll pick up CDN hosts";
        hintEl.style.display = "block";
        if (typeof s.discoveredCount === "number" && s.discoveredCount > 0) {
          const n = s.discoveredCount;
          countEl.textContent = `+${n} domain${n === 1 ? "" : "s"} added`;
          countEl.style.display = "block";
          const reloadBtn = document.createElement("button");
          reloadBtn.className = "fp-btn primary";
          reloadBtn.textContent = "Reload";
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
        finishBtn.className = "fp-btn ghost";
        finishBtn.textContent = "Finish";
        finishBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "finish_discovery" });
        });
        actionsEl.appendChild(finishBtn);
        actionsEl.style.display = "flex";
        panel.classList.add("visible");
        break;

      case "add": {
        accentEl.className = "fp-accent gr";
        labelEl.innerHTML = `<span class="host">${s.host}</span> <span class="st">not proxied</span>`;
        const addBtn = document.createElement("button");
        addBtn.className = "fp-btn primary";
        addBtn.textContent = "Add to proxy";
        addBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "add_current_site" });
        });
        actionsEl.appendChild(addBtn);
        actionsEl.style.display = "flex";
        panel.classList.add("visible");
        break;
      }

      case "catalog_disabled": {
        accentEl.className = "fp-accent gr";
        labelEl.innerHTML = `<span class="host">${s.host}</span> <span class="st">disabled</span>`;
        const enableBtn = document.createElement("button");
        enableBtn.className = "fp-btn primary";
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
        panel.classList.add("visible");
        break;
      }

      case "blocked": {
        accentEl.className = "fp-accent rd";
        labelEl.innerHTML = `<span class="host">${s.host}</span> <span class="st">blocked</span>`;
        const fixBtn = document.createElement("button");
        fixBtn.className = "fp-btn primary";
        fixBtn.textContent = "Add to proxy";
        fixBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "add_current_site_and_reload" });
        });
        actionsEl.appendChild(fixBtn);
        const dismissBtn = document.createElement("button");
        dismissBtn.className = "fp-btn ghost";
        dismissBtn.textContent = "Dismiss";
        dismissBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          chrome.runtime.sendMessage({ type: "dismiss_block", host: s.host });
          panel.classList.remove("visible");
        });
        actionsEl.appendChild(dismissBtn);
        actionsEl.style.display = "flex";
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
