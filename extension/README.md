# Smurov Proxy — Browser Extension

Companion to the Smurov Proxy desktop client. Provides per-tab proxy controls,
automatic domain discovery, and ISP-block detection.

**Requires:** the Smurov Proxy desktop client running on the same machine.

## Install (development)

1. Open `chrome://extensions` (or `edge://extensions`).
2. Toggle on "Developer mode" (top right).
3. Click "Load unpacked" and select this `extension/` folder.
4. Click the extension icon in the toolbar.
5. Open the Smurov Proxy desktop client → "Browser Extension" tab.
6. Copy the token shown there.
7. Paste it into the extension popup → click "Pair".

The extension is now connected to your local daemon and will start
working on every tab.

## What it does

- **In-page panel** (bottom-right corner of every page) shows the proxy state
  for the current site:
  - ✓ green: site is being proxied
  - gray: site is not in your catalog — click "Add to proxy" to add it
  - red: ISP block detected — click "Add to proxy" to fix
  - red ("daemon not running"): start the desktop client
- **Automatic discovery:** when you add a new site, the extension watches
  what your browser loads on that site for the duration of your visit and
  pushes any new domains to the catalog. Subsequent visits (and other
  users) get the enriched domain list automatically.
- **Block detection:** if a top-level navigation fails with a suspicious
  error code, the extension silently verifies via the proxy whether the
  site loads through the tunnel. If yes, it offers to add the site with
  one click.

## Privacy

The extension reads request URLs (network metadata, not page content) and
forwards summaries only to your local Smurov Proxy daemon at
`127.0.0.1:9090`. Nothing is sent directly to any remote server.

## Architecture

See [`docs/superpowers/specs/2026-04-08-browser-extension-design.md`](../docs/superpowers/specs/2026-04-08-browser-extension-design.md).

## Publishing

Future Chrome Web Store submission. For now, sideload only.
