# Smurov Proxy — Browser Extension

Companion to the Smurov Proxy desktop client. Provides per-tab proxy controls,
automatic domain discovery, and ISP-block detection.

**Requires the Smurov Proxy desktop client** running on the same machine.

## Install (development)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Toggle on "Developer mode" (top right).
3. Click "Load unpacked" and select this `extension/` folder.
4. Click the extension icon → paste the token shown in the desktop client's
   "Browser Extension" tab.

The extension will be published to the Chrome Web Store when feature-complete
and tested.

## Architecture

See [`docs/superpowers/specs/2026-04-08-browser-extension-design.md`](../docs/superpowers/specs/2026-04-08-browser-extension-design.md).
