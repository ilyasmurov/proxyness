# Browser Extension for Site Management and Auto-Discovery Design

## Overview

Add a Chromium-compatible browser extension that **complements** the existing
Electron desktop client. The extension provides per-tab UX for the proxy state
of the current site, one-click "add this site to the proxy" actions, automatic
domain discovery via `chrome.webRequest`, and ISP-block detection with silent
verification through the tunnel.

The extension talks **exclusively to the local daemon** at `127.0.0.1:9090`,
never directly to `proxy.smurov.com`. The daemon already holds the device key
and the sync state, so the extension stays a thin UI layer. As a side effect,
installing the extension implicitly markets the desktop client: an extension-
only user hits a "daemon not running" state and is pointed to the client
download.

This is the next iteration after the sites-catalog-sync release (2026-04-08).
It uses the global `sites` / `site_domains` catalog established there and adds
a single new sync op (`add_domain`) to enrich existing entries.

## Why an Extension at All

The desktop client + daemon already do everything except one thing well:
**they cannot attribute network traffic to a particular tab inside a browser**.
The browser owns all sockets in a single process, so OS-level PID tracking
(which the TUN engine already uses for per-app routing) gives you "Chrome" but
not "the Habr tab inside Chrome".

A browser extension solves this trivially. `chrome.webRequest` events carry
`tabId`, `frameId`, and `initiator`. Discovery becomes per-tab native, with no
heuristics, no isolated browser spawning, and no PID tree filtering.

The extension also unlocks a UX that pure-desktop apps cannot: contextual
controls right where the user is browsing, surfacing block-induced errors
exactly when they hurt.

## Goals

- **Per-tab attribution** of network requests for accurate domain discovery.
- **Enrich the global catalog** so users adding a popular site benefit from
  prior users' discoveries — the second user to add `habr.com` should get all
  the CDN domains the first user accidentally collected.
- **Detect ISP blocks** on top-level navigation and offer one-click recovery,
  killing the "why doesn't this site work, oh I forgot to add it" cycle.
- **Drive desktop client installs** — the extension cannot proxy traffic on
  its own, so anyone who finds the extension first is funneled into the
  client download.

## Non-Goals

- **Replacing the desktop client.** The Electron client + `AppRules.tsx` site
  grid stays. The extension is a second UI on the same backend, not a
  rewrite.
- **Covering non-browser apps.** Telegram, Discord, Steam, games — still go
  through the desktop client's TUN engine. The extension only manages browser
  traffic.
- **Firefox / Safari for v1.** Firefox AMO can come later; Safari needs
  Xcode + Apple Developer account and is not worth it for this iteration.
- **Manual moderation** of discovered domains. The catalog is implicitly
  trusted — 7 known users, no spam risk yet. Admin moderation UI is deferred.
- **Browser-level proxy configuration** via `chrome.proxy` API. The daemon
  already configures the system PAC; per-extension proxy override would
  fight that.
- **Authenticating the extension to the server.** The extension never sees
  the device key — it talks to the daemon, the daemon talks to the server.
- **Per-user discovery preferences.** Always-on for newly-added sites; no
  toggle.
- **The "disable proxy for one specific subdomain on a proxied site"** use
  case. Out of scope for v1.

## Architecture

### Components

```
┌─────────────────────────────┐
│ Browser tab (any website)   │
│   ┌───────────────────────┐ │
│   │ Content script        │ │
│   │ - reads location      │ │
│   │ - injects Shadow DOM  │ │
│   │ - panel UI            │ │
│   └──────┬────────────────┘ │
└──────────┼──────────────────┘
           │ runtime.sendMessage
           ▼
┌─────────────────────────────┐
│ Service worker (background) │
│ - chrome.webRequest hooks   │
│ - chrome.webNavigation      │
│ - state cache per tab       │
│ - talks to daemon           │
└──────────┬──────────────────┘
           │ HTTP fetch
           ▼
┌─────────────────────────────┐
│ Daemon @ 127.0.0.1:9090     │
│ - new /sites/* endpoints    │
│ - holds device key          │
│ - syncs to server           │
└──────────┬──────────────────┘
           │ TLS / sync
           ▼
┌─────────────────────────────┐
│ Server @ proxy.smurov.com   │
│ - existing /api/sync        │
│ - new add_domain op         │
└─────────────────────────────┘
```

The extension never sees the device key, never makes direct calls to
`proxy.smurov.com`. All persistence and server I/O flows through the daemon.

### Daemon API Surface (new)

All new endpoints are added to `daemon/internal/api/` and mounted on the
existing `127.0.0.1:9090` HTTP server. The daemon adds CORS headers allowing
the extension's `chrome-extension://<id>` origin (the extension's stable ID
will be hardcoded after first publish to the Web Store).

All four endpoints require an `Authorization: Bearer <token>` header. The
token is established via the pairing flow described in
**Authentication & Pairing** below. Requests without a valid token return
`401 Unauthorized`.

| Method | Path | Purpose | Request | Response |
|--------|------|---------|---------|----------|
| `GET`  | `/sites/match?host=<eTLD+1>` | Is this host in the catalog? Is it currently proxy-enabled? | — | `{ in_catalog: bool, site_id: int?, proxy_enabled: bool, daemon_running: true }` |
| `POST` | `/sites/add` | Add a new site (or dedup to existing) | `{ primary_domain, label }` | `{ site_id, deduped: bool }` |
| `POST` | `/sites/discover` | Push discovered domains for a site | `{ site_id, domains: string[] }` | `{ added: int, deduped: int }` |
| `POST` | `/sites/test` | Verify a URL through the proxy (block detection) | `{ url }` | `{ likely_blocked: bool, status_code: int? }` |

**Scope note:** the existing daemon endpoints (`/tun/*`, `/pac/*`,
`/transport`, `/tunnel/active-hosts`) remain **unauthenticated** in v1.
Extending token auth to them would require lockstep updates to the desktop
client and is out of scope for this spec — the new `/sites/*` endpoints are
the only ones the extension calls, and they're the only ones that need the
new auth boundary.

These wrap calls to the existing `/api/sync` server endpoint:

- `/sites/add` enqueues an `add` op via the same sync module the desktop
  client already uses.
- `/sites/discover` enqueues batched `add_domain` ops (new server op type,
  see Server Changes below).
- `/sites/test` makes a `HEAD` request to the URL through the SOCKS5 tunnel
  to the production server. If the request succeeds while the direct attempt
  failed, the host is likely ISP-blocked.

The `/sites/test` endpoint is the only one that requires new daemon-side
machinery (an `http.Client` whose `Transport.DialContext` routes through the
existing tunnel rather than the protected dialer). Everything else is plumbing
on top of the existing sync module.

### Authentication & Pairing

The daemon's new `/sites/*` endpoints require a per-extension bearer token.
Without auth, any local process — including arbitrary JS loaded in any tab
of any browser via `fetch("http://127.0.0.1:9090/...")` — could read or
mutate the catalog. The threat is real because the very thing the extension
does (talk to localhost from JS) is also what an attacker can do.

#### Token generation

On first start (and whenever the token file is missing), the daemon generates
a 32-byte cryptographically-random token, hex-encodes it, and persists it:

- **Unix:** `~/.config/smurov-proxy/daemon-token` with mode `0600`
- **Windows:** `%APPDATA%\SmurovProxy\daemon-token` with the user's ACL only

The token has no expiry. The user can manually rotate it (delete the file,
restart the daemon) which invalidates all paired extensions.

#### Token check middleware

A new middleware in `daemon/internal/api/auth.go` is mounted **only on
`/sites/*`** routes. It:

1. Reads `Authorization: Bearer <token>` from the request.
2. Constant-time-compares against the loaded token.
3. On mismatch returns `401 Unauthorized` with no body.
4. On the very first call from a new origin, also records the
   `Origin` header so the desktop client can show "Paired with Chrome
   extension `<id>`" in its UI.

The middleware does NOT validate `Origin` against an allowlist — the token
itself is the gate.

#### Pairing flow

1. User installs the extension and clicks the toolbar icon for the first time.
2. Extension popup checks `chrome.storage.local` for a stored token. None
   found → renders the pairing screen: "Open Smurov Proxy desktop app →
   Browser Extension tab → copy the token here: `[_______________]`."
3. User opens the desktop client, navigates to a new **Browser Extension**
   tab. The tab shows the token with a Copy button, plus pairing status.
4. User pastes the token into the extension popup. Popup validates by
   calling `GET /sites/match?host=test.local` with the token. On 200, stores
   the token in `chrome.storage.local` (key: `daemon_token`), shows ✓ Paired.
5. From this point, every daemon API call from the service worker includes
   the token in the `Authorization` header.
6. The desktop client polls its own daemon for paired extension origins
   every few seconds and updates its UI to show "Paired ✓".

#### Re-pairing

If a request returns 401 (token rotated, or token file missing because the
user wiped state), the extension drops the cached token, re-shows the
pairing screen, and prompts the user to fetch a new token from the desktop
client. No data is lost — the pairing is purely about local API access.

#### Why not native messaging

The Manifest V3 native messaging API would let the extension fetch the
token automatically without manual paste. We chose paste-based pairing
because:

- Native messaging requires shipping a separate native host binary AND
  registering a manifest file at OS-specific paths (~/Library/...
  /Chrome/NativeMessagingHosts/com.smurov.proxy.json on Mac, similar on
  Win), which is extra build/install plumbing for both the desktop client
  installer AND the extension.
- The user only pairs once per browser. Friction is one-time and small.
- Manual pairing makes the trust relationship visible to the user, which
  matches the security-conscious posture this whole system is built on.

Native messaging stays in the back pocket if pairing UX feedback shows it's
worth the complexity later.

### Server Changes

Add a new sync op type `add_domain` to the existing `POST /api/sync` handler:

```json
{ "op": "add_domain", "site_id": 123, "domain": "habrcdn.io", "at": 1712512345 }
```

Server handler (`server/internal/db/sites.go::ApplyAddDomainOp`):

1. Validate the domain via the existing `domainRE`.
2. Verify the user is linked to `site_id` via `user_sites` (auth check — only
   users who have the site enabled can enrich it; prevents random spammers
   adding domains to sites they don't even use).
3. `INSERT OR IGNORE INTO site_domains (site_id, domain, is_primary) VALUES (?, ?, 0)`.
4. Return `{ status: "ok", deduped: <bool> }` per existing op_results format.

This is the only server change. No new tables, no schema migrations beyond
what already shipped in sites-catalog-sync.

### Extension Layout

```
extension/
├── manifest.json          # Manifest V3
├── service-worker.js      # background, webRequest hooks, daemon I/O
├── content-script.js      # injects panel into every page
├── panel/
│   ├── panel.html         # Shadow DOM template
│   ├── panel.css
│   └── panel.js           # state rendering + click handlers
├── popup/
│   ├── popup.html         # toolbar icon popup (settings, status)
│   └── popup.js
├── lib/
│   └── tldts.min.js       # public suffix list for eTLD+1 extraction
└── icons/                 # 16/32/48/128 px
```

### Manifest V3 Permissions

```json
{
  "manifest_version": 3,
  "permissions": ["webRequest", "webNavigation", "storage", "tabs"],
  "host_permissions": [
    "<all_urls>",
    "http://127.0.0.1:9090/*"
  ]
}
```

Notes on the permissions:

- **`webRequest` (read-only)** still works in V3. The blocking variant
  (`webRequestBlocking`) was removed for non-enterprise extensions in V3, but
  pure observation via `onBeforeRequest` / `onErrorOccurred` is still allowed.
- **`<all_urls>`** is required to observe webRequest events on every site and
  inject the content script on every page. Chrome shows a "this extension can
  read and change all your data on all websites" warning at install time. We
  mitigate this in the store description: the extension only reads URLs
  (network metadata, not page content) and forwards summaries to the local
  daemon — never to a remote server.
- **`storage`** is for `chrome.storage.local`: dismissed banners, panel
  preferences, the per-tab discovery state that must survive service worker
  restarts.
- **`tabs`** is for `tabs.onActivated` / `tabs.onUpdated` so the panel can
  refresh when the user switches tabs.

## UX Flows

All flows below assume the extension is **already paired** with the daemon
(see Authentication & Pairing). The pairing screen is shown the first time
the user clicks the extension icon and is dismissed forever after a
successful token paste.

### Flow 1 — Add a new site

1. User navigates to `https://habr.com`. The site is not in the catalog and
   the page may or may not load directly (Russia: it usually does, until it
   doesn't).
2. Content script reads `location.host`, sends a message to the service
   worker.
3. Service worker computes eTLD+1 (`habr.com`), calls
   `GET /sites/match?host=habr.com`. Daemon returns
   `{ in_catalog: false, daemon_running: true }`.
4. Panel renders inside the page's Shadow DOM root, collapsed to a small pill
   in the corner: "**+ Add habr.com to proxy**".
5. User clicks Add.
6. Service worker calls
   `POST /sites/add { primary_domain: "habr.com", label: "Habr" }`.
   Daemon enqueues an `add` op via the sync module, the existing flow runs,
   server returns `{ site_id: 47, deduped: false }`.
7. Panel state flips to: "✓ habr.com proxied · *discovering...*"
8. Service worker switches on the discovery hook for the current `tabId`,
   tagging it with `site_id=47`.
9. As the user scrolls, clicks links, navigates within `habr.com`, every new
   host seen by `chrome.webRequest.onBeforeRequest` (and not already in the
   service worker's cache of `site_domains` for this site) is queued.
10. Every 5 seconds the queue flushes:
    `POST /sites/discover { site_id: 47, domains: [...] }`.
11. When the user navigates away from `habr.com` (eTLD+1 changes) or closes
    the tab, discovery for that tab stops cleanly. Final flush is sent
    immediately.
12. Panel becomes idle: "✓ habr.com proxied".

### Flow 2 — Sub-resource fails on a known proxied site

1. User is on `https://habr.com` (proxied), browsing normally.
2. A request to `https://habrcdn.io/img/x.jpg` fails with
   `net::ERR_CONNECTION_RESET` — the CDN is blocked and not yet in
   `site_domains` for habr.com.
3. Service worker's `chrome.webRequest.onErrorOccurred` fires with
   `tabId, url, error, type='image'`.
4. Service worker checks: failed host's eTLD+1 (`habrcdn.io`) ≠ current
   site's primary (`habr.com`), error code is in the "looks-like-a-block"
   allowlist, user is currently on a proxied site → strong signal that
   `habrcdn.io` is part of habr.com.
5. **Immediate** flush (don't wait 5s):
   `POST /sites/discover { site_id: 47, domains: ["habrcdn.io"] }`.
6. Daemon syncs to server, server adds the row with INSERT OR IGNORE,
   client's local PAC is updated on next /pac/sites refresh.
7. No banner, no notification — the next reload or retry succeeds via proxy.
8. Optional polish: panel shows a tiny "fixed 1 broken resource" toast for
   2 seconds. Defer to v1.1 if it adds noise.

### Flow 3 — Top-level navigation blocked

1. User types `youtube.com` in the address bar, hits Enter.
2. Page fails to load with `net::ERR_CONNECTION_RESET` on the main_frame.
3. Service worker's `chrome.webRequest.onErrorOccurred` fires with
   `type='main_frame'`. The error code is in the suspicious allowlist
   (RESET, NAME_NOT_RESOLVED, TIMED_OUT, CONNECTION_CLOSED, SSL_PROTOCOL_ERROR).
4. Service worker kicks off **silent verification**:
   `POST /sites/test { url: "https://youtube.com" }`.
5. Daemon makes a HEAD request to `youtube.com` through the SOCKS5 tunnel
   to the production server. If response is 200/3xx → block confirmed.
   Daemon returns `{ likely_blocked: true, status_code: 200 }`.
6. Service worker messages the content script. Content script injects a
   full-width banner at the top of the page (Shadow DOM):
   "youtube.com appears blocked. **[Add to proxy]** *Don't ask again*".
7. User clicks Add → same flow as Flow 1, plus the tab is reloaded via
   `chrome.tabs.reload(tabId)` once the daemon confirms the new PAC is
   applied.
8. If verification through proxy ALSO fails → site is genuinely down or
   unreachable → no banner, regular browser error stays visible.
9. If user clicks "Don't ask again" → service worker writes
   `{ host: "youtube.com", dismissed_at: now }` to `chrome.storage.local`,
   won't re-prompt for 24h.

### Flow 4 — Daemon not running

1. Any of the above flows starts.
2. Service worker tries to fetch `127.0.0.1:9090/sites/match?host=...` →
   connection refused.
3. Service worker stores "daemon down" in cache, sends to content script.
4. Panel renders a single-purpose message: "Smurov Proxy daemon is not
   running. **[Open desktop client]** or **[Download]**".
5. The "Download" link goes to `https://proxy.smurov.com` with platform-
   specific PKG/exe. This is the implicit advertising loop.
6. Service worker retries the daemon every 30 seconds; once it comes up,
   the panel transitions back to its normal state.

## Panel State Machine

```
       ┌──────────┐
       │  IDLE    │ ◄────────────┐
       │ (hidden) │              │
       └─────┬────┘              │
             │ tab change        │
             ▼                   │
      ┌──────────────┐           │
      │ check daemon │           │
      └────┬─────────┘           │
           │                     │
   ┌───────┴───────┐             │
   │ down          │ up          │
   ▼               ▼             │
┌──────┐    ┌──────────────┐     │
│ DOWN │    │ in_catalog?  │     │
└──────┘    └─┬──────────┬─┘     │
              │ no       │ yes   │
              ▼          ▼       │
        ┌─────────┐ ┌─────────┐  │
        │  ADD?   │ │ PROXIED │  │
        └────┬────┘ └─────────┘  │
             │ click             │
             ▼                   │
        ┌─────────────┐          │
        │ DISCOVERING │──────────┘ tab close / navigate away
        └─────────────┘
```

The panel is **collapsed by default** to a small floating pill in the corner
of the page (positioned bottom-right via fixed positioning inside the Shadow
DOM root). It expands on hover, and **auto-expands** when the state is `ADD?`
or `BLOCKED` (Flow 3) — actionable states force themselves into view.

## Distribution

**v1: Chrome Web Store** ($5 one-time developer fee, ~1-2 day review). One
submission covers Chrome, Edge (via "Allow extensions from other stores"),
Brave, Opera, Vivaldi, and other Chromium derivatives. ~95% of target users.

**During development:** sideload via `chrome://extensions` → developer mode
→ "Load unpacked". The persistent "disable developer extensions" banner is
acceptable for the 7 known beta users. The extension folder ships in the
repo; users `git clone` and load it from there.

**Future iterations:** Firefox AMO (free, similar review process, separate
manifest tweaks for V3 differences). Safari is explicitly out of scope.

## Open Risks

1. **False positives in block detection.** Bad WiFi, real server outages,
   mobile network glitches all produce errors that look like blocks.
   Mitigation: silent verification through the proxy before showing the
   banner; dismissals remembered for 24h; rate-limit to 3 banners per
   browser session.

2. **`<all_urls>` permission warning at install.** Chrome shows a scary
   warning. Mitigation: store description explicitly says "reads URLs only,
   never page content; sends summaries only to your local Smurov Proxy
   daemon". Add a one-paragraph privacy explainer in the popup that opens
   the first time the extension is loaded.

3. **Content script overhead on every page.** Even with Shadow DOM, ~5KB of
   JS is injected into every tab. Mitigation: lazy-load the panel UI only
   when there's actionable state; keep the always-on content script under
   1KB (just bootstraps and waits for service worker messages).

4. **Token leak via filesystem.** The daemon's token file
   (`~/.config/smurov-proxy/daemon-token`) is the gate to the new
   `/sites/*` endpoints. Mode `0600` protects it from other Unix users on
   the same machine, but malware running as the same user can read it.
   That's the same threat surface as the device key file the desktop
   client already stores, so we accept it. Token rotation by deleting the
   file and restarting the daemon is a manual recovery path documented in
   the README.

5. **Catalog pollution.** A misbehaving extension copy could pump junk
   domains into popular sites' `site_domains` lists. Partial mitigation
   built in: the new `add_domain` server op rejects domains for sites the
   user is not linked to. Full moderation UI deferred.

6. **Service worker lifecycle.** Manifest V3 service workers can be killed
   by Chrome at any time (idle for 30s). State must persist via
   `chrome.storage`, not in-memory globals. Discovery state for active
   tabs survives service worker restarts via a `chrome.storage.session`
   record keyed by `tabId`.

7. **Public suffix list maintenance.** `tldts` ships with a static PSL
   snapshot. We bundle it; updates ship with extension updates. For our
   use case (eTLD+1 extraction for site matching) staleness is harmless.

## Out of Scope (v1)

- Admin UI for moderating user-submitted sites or discovered domains
- Firefox AMO submission
- Safari extension
- Mobile browsers
- Per-user discovery preferences (always-on for new sites in v1)
- Token auth for the existing `/tun/*`, `/pac/*`, `/transport`, and
  `/tunnel/active-hosts` daemon endpoints (only the new `/sites/*` are
  authenticated in v1)
- Native messaging instead of paste-pairing (deferred until pairing UX
  feedback shows it's worth the complexity)
- Disable proxy for one specific subdomain on a proxied site
- Browser bookmarklet fallback for users who can't install extensions
- Telemetry / usage analytics inside the extension
