# Extension Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rewrite the browser extension's popup and floating panel CSS/HTML to match the approved design (`assets/design-extension-final.html`), with entrance animations.

**Architecture:** Pure CSS + vanilla JS rewrite. No new dependencies. Popup uses Google Fonts loaded via `<link>` in popup.html. Content-script shadow DOM bundles fonts via `@import`. Both use the shared OKLCH palette from the desktop client. Animations via CSS `@keyframes`.

**Tech Stack:** Vanilla JS, CSS (OKLCH), Chrome Extension MV3

**Approved mockup:** `assets/design-extension-final.html`

---

### File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `extension/popup/popup.html` | Modify | Add Google Fonts `<link>` |
| `extension/popup/popup.css` | Rewrite | Full popup styling matching approved design |
| `extension/popup/popup.js` | Modify | Update all render functions to emit new HTML structure |
| `extension/content-script.js` | Modify | Update shadow DOM styles + render() for new floating panel |

---

### Task 1: Popup HTML — add Google Fonts

**Files:**
- Modify: `extension/popup/popup.html`

- [ ] **Step 1: Add font preconnect + stylesheet links**

In `popup.html`, add before the existing `<link rel="stylesheet">`:

```html
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Barlow+Semi+Condensed:wght@500;600;700&family=Figtree:wght@400;500;600;700&family=Barlow:wght@400;500&display=swap" rel="stylesheet">
```

- [ ] **Step 2: Verify popup.html loads**

Open `chrome://extensions`, reload the extension, click the popup. It should still render (old styles still apply, fonts now available).

- [ ] **Step 3: Commit**

```bash
git add extension/popup/popup.html
git commit -m "feat(extension): add Google Fonts to popup"
```

---

### Task 2: Popup CSS — full rewrite

**Files:**
- Rewrite: `extension/popup/popup.css`

- [ ] **Step 1: Write the new popup.css**

Replace entire file with the approved design CSS. All tokens from the mockup, every selector. Key elements:

**Palette variables** on `body`:
```
--bg0: oklch(0.12 0.014 250)
--bg1: oklch(0.155 0.016 250)
--bg2: oklch(0.19 0.018 250)
--bg3: oklch(0.23 0.016 250)
--b1: oklch(0.24 0.013 250)
--t1: oklch(0.93 0.006 250)
--t2: oklch(0.60 0.012 250)
--t3: oklch(0.42 0.01 250)
--am: oklch(0.78 0.155 75)
--amb: oklch(0.19 0.035 75)
--gn: oklch(0.72 0.15 150)
--gnb: oklch(0.17 0.03 150)
--rd: oklch(0.62 0.19 25)
--rdb: oklch(0.17 0.03 25)
```

**Components** (copy exact styles from `assets/design-extension-final.html`):
- `.p` — popup container (348px, bg0, rounded 12px, border, shadow)
- `.p-head` — header bar (bg1, flex, gap 10px, border-bottom)
- `.p-head-logo`, `.p-head-name`, `.p-head-dot` (.on/.off/.err)
- `.p-body` — content area (padding 16px)
- `.p-domain` — Barlow Semi Condensed, 20px, 600 weight
- `.p-tag` — status tag with variants: `.proxied`, `.not-proxied`, `.disabled`, `.discovering`
- `.p-btn` — full-width buttons with `.primary` (amber) and `.secondary` (bg2)
- `.p-input` — text input (Barlow monospace)
- `.p-foot` — D-style footer (flex, gap 2px, padding 6px 10px)
- `.p-foot-btn` — icon+label buttons (transparent bg, hover: bg2), `.danger` variant
- `.p-error`, `.p-pair-title`, `.p-pair-sub` — pairing/error views
- `.p-down-icon`, `.p-down-title`, `.p-down-sub` — daemon-down view
- `.p-disc-hint`, `.p-disc-count`, `.p-disc-btns` — discovery extras

**Animations:**
- `@keyframes pn-fade-in` — opacity 0→1, translateY(4px)→0, 0.2s ease-out
- `@keyframes pn-slide-up` — opacity 0→1, translateY(8px)→0, 0.3s ease-out
- `.p` gets `animation: pn-fade-in 0.2s ease-out`
- `.p-body` gets `animation: pn-slide-up 0.25s ease-out 0.05s both`
- `.p-foot` gets `animation: pn-fade-in 0.2s ease-out 0.1s both`
- `.p-btn` gets `transition: background 0.12s, transform 0.1s` and `:active { transform: scale(0.98) }`

- [ ] **Step 2: Verify visually**

Reload extension, click popup. Compare against `assets/design-extension-final.html`. Old JS still renders old class names → some things will look broken. That's expected — Task 3 fixes it.

- [ ] **Step 3: Commit**

```bash
git add extension/popup/popup.css
git commit -m "feat(extension): rewrite popup CSS to approved design"
```

---

### Task 3: Popup JS — update render functions

**Files:**
- Modify: `extension/popup/popup.js`

- [ ] **Step 1: Update `renderPairing()`**

Replace the innerHTML with the approved structure:
- `.p` container wrapping everything
- `.p-head` with ghost SVG (closed eye), "Proxyness", `.p-head-dot.off`
- `.p-body` with `.p-pair-title`, `.p-pair-sub`, `.p-input`, `.p-btn.primary`
- `.p-error` for error state (hidden by default)

- [ ] **Step 2: Update `renderDaemonDown()`**

Replace innerHTML:
- `.p` container
- `.p-head` with ghost SVG (closed eye), "Proxyness", `.p-head-dot.err`
- `.p-body` centered with `.p-down-icon` (SVG exclamation in red circle), `.p-down-title`, `.p-down-sub`
- `.p-foot` with spacer + `.p-foot-btn.danger` (X icon + "Unpair")

- [ ] **Step 3: Update `renderManualEntry()`**

Replace innerHTML:
- `.p` container
- `.p-head` with ghost SVG (open eye), "Proxyness", `.p-head-dot.on`
- `.p-body` with `.p-pair-title` ("Add a site"), `.p-pair-sub`, `.p-input`, `.p-btn.primary`
- `.p-foot` with Hide panel + spacer + Unpair (both `.p-foot-btn` with SVG icons + labels)

- [ ] **Step 4: Update `renderControlPanel()`**

Replace innerHTML for all three states (not_in_catalog / proxied / catalog_disabled):
- `.p` container
- `.p-head` with ghost SVG (open eye), "Proxyness", `.p-head-dot.on`
- `.p-body` with `.p-domain`, `.p-tag` (correct variant class), `.p-btn` (primary or secondary)
- For discovering state: add `.p-disc-hint`, `.p-disc-count`, `.p-disc-btns`
- `.p-foot` with Add site + Hide panel + spacer + Unpair (all `.p-foot-btn` with SVG icons)
- Footer for discovery mode: only Hide panel + spacer + Unpair (no Add site)

SVG icons to inline:
- Ghost open eye: `<svg viewBox="0 0 100 100"><path d="M50 10 C25 10..."/><ellipse cx="50" cy="48" rx="16" ry="14" fill="oklch(0.68 0.12 75)"/><ellipse cx="50" cy="48" rx="8" ry="7" fill="oklch(0.25 0.02 75)"/></svg>`
- Ghost closed eye: same path, single `<ellipse cx="50" cy="48" rx="16" ry="12" fill="oklch(0.25 0.014 250)"/>`
- Plus: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M8 3v10M3 8h10"/></svg>`
- Eye-slash: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M1 8c2.5-4 5-5 7-5s4.5 1 7 5c-2.5 4-5 5-7 5s-4.5-1-7-5z"/><line x1="2" y1="14" x2="14" y2="2"/></svg>`
- X: `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"/></svg>`

- [ ] **Step 5: Verify all popup states**

Reload extension. Test each state:
1. Unpair → verify pairing screen renders
2. Pair → verify control panel renders
3. Navigate to proxied/non-proxied/disabled sites
4. Check manual entry on new tab

- [ ] **Step 6: Commit**

```bash
git add extension/popup/popup.js
git commit -m "feat(extension): update popup render functions for new design"
```

---

### Task 4: Content-script floating panel — redesign

**Files:**
- Modify: `extension/content-script.js`

- [ ] **Step 1: Replace shadow DOM styles**

Replace the entire `<style>` block inside `shadow.innerHTML` with the approved floating panel CSS from the mockup:

Key changes vs current:
- `.panel` → `.fp` (outer container with `display: flex`)
- Add `.fp-accent` (3px colored left stripe: `.gn`/`.gr`/`.am`/`.rd`)
- `.fp-content` (padding 9px 12px, flex: 1)
- `.fp-row` (flex, gap 7px)
- `.fp-ghost` (14px ghost SVG, opacity 0.4)
- `.fp-label` with `.host` (bold) + `.st` (muted)
- `.fp-close` (16px, rounded 3px)
- `.fp-hint`, `.fp-count`, `.fp-actions` (padding-left 21px to align past ghost)
- `.fp-btn.primary` (amber bg, dark text), `.fp-btn.ghost` (bg2, muted text)

**Add `@import` for fonts** at the top of the style block:
```css
@import url('https://fonts.googleapis.com/css2?family=Figtree:wght@400;500;600;700&display=swap');
```

**Animations:**
- `@keyframes fp-enter` — opacity 0→1, translateY(6px)→0, 0.25s ease-out
- `.fp` gets `animation: fp-enter 0.25s ease-out`

- [ ] **Step 2: Update initial shadow DOM HTML structure**

Replace the panel markup inside `shadow.innerHTML`:
```html
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
```

- [ ] **Step 3: Update `render()` function**

Update the `render(s)` switch cases to use new class names:

- **All cases:** Set `accent.className` to `fp-accent gn/gr/am/rd` based on state. Use `panel.classList.add/remove("visible")` for show/hide.
- **"proxied":** accent `gn`, label = `<span class="host">${host}</span> <span class="st">proxied</span>`, collapsed (no actions)
- **"add":** accent `gr`, label = `<span class="host">${host}</span> <span class="st">not proxied</span>`, one action button (fp-btn primary "Add to proxy")
- **"catalog_disabled":** accent `gr`, label with "disabled", action button "Enable"
- **"discovering":** accent `am`, label with "scanning", hint text, count if > 0, reload + finish buttons
- **"blocked":** accent `rd`, label with "blocked", two action buttons
- **"down":** accent `rd`, label = `<span class="st">App not running</span>`, hint text

- [ ] **Step 4: Update element references**

After changing the shadow DOM structure, update `getElementById` calls:
- `panel` → still `panel`
- `icon` → removed (replaced by `accent`)
- Add: `const accentEl = shadow.getElementById("accent")`
- `label` → still `label`
- `actions` → still `actions`
- `hint` → still `hint`
- `count` → still `count`
- `close` → still `close`

Remove references to `iconEl` and old `.collapsed` class toggles.

- [ ] **Step 5: Update dragging**

The dragging code stays the same but references `panel` which is now `.fp` instead of `.panel`. Ensure:
- `panel.style.left/top/right/bottom` still work (they do — it's the same element)
- `panel.classList.add/remove("dragging")` → add `.fp.dragging { cursor: grabbing; transition: none; }` to styles

- [ ] **Step 6: Verify all floating panel states**

Reload extension. Visit:
1. A proxied site → green accent, collapsed pill
2. A non-proxied site → grey accent, "Add to proxy" button
3. Trigger discovery → amber accent, scanning state
4. Stop daemon → red accent, "App not running"

- [ ] **Step 7: Commit**

```bash
git add extension/content-script.js
git commit -m "feat(extension): redesign floating panel with accent stripe and animations"
```

---

### Task 5: Final polish — animation timing + squash commit

**Files:**
- Modify: `extension/popup/popup.css`
- Modify: `extension/content-script.js`

- [ ] **Step 1: Add popup button micro-interactions**

In `popup.css`, add:
- `.p-btn:active { transform: scale(0.98); }` — subtle press feedback
- `.p-foot-btn` transition on svg opacity
- `.p-tag .dot` — add `box-shadow` pulse for `.proxied` and `.discovering` variants

- [ ] **Step 2: Add floating panel state transition**

In `content-script.js` styles, add:
- `.fp { transition: opacity 0.2s; }` for show/hide
- `.fp-accent { transition: background 0.2s; }` for state color changes
- Re-trigger entrance animation on state change by toggling a class

- [ ] **Step 3: Full visual QA**

Test every state in both popup and floating panel. Compare against `assets/design-extension-final.html`.

- [ ] **Step 4: Commit**

```bash
git add extension/
git commit -m "feat(extension): add micro-interactions and polish animations"
```
