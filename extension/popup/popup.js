const root = document.getElementById("root");

async function getStoredToken() {
  const r = await chrome.storage.local.get("daemon_token");
  return r.daemon_token || null;
}

async function tryPing(token) {
  return new Promise((resolve) => {
    chrome.runtime.sendMessage({ type: "set_token", token }, (resp) => resolve(resp?.ok === true));
  });
}

async function clearAndRender() {
  await new Promise((resolve) => chrome.runtime.sendMessage({ type: "clear_token" }, resolve));
  render();
}

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
    const ok = await tryPing(token);
    if (ok) {
      render();
    } else {
      msg.textContent = "Pairing failed. Is the daemon running? Token correct?";
      msg.className = "error";
    }
  });
}

function renderPaired() {
  root.innerHTML = `
    <div class="title">✓ Paired</div>
    <div class="row"><span class="label">Status:</span><span class="value">Connected to local daemon</span></div>
    <div class="subtitle" style="margin-top: 12px;">
      The extension is monitoring your tabs and will offer to add new sites
      to the proxy when needed.
    </div>
    <button id="unpair" class="danger">Unpair</button>
  `;
  document.getElementById("unpair").addEventListener("click", clearAndRender);
}

async function render() {
  const token = await getStoredToken();
  if (!token) {
    renderPairing();
    return;
  }
  // Verify token still works.
  const ok = await tryPing(token);
  if (ok) {
    renderPaired();
  } else {
    renderPairing("Saved token is invalid. Please re-pair.");
  }
}

render();
