# Browser Extension Popup → Per-Tab Control Panel

**Date:** 2026-04-08
**Status:** Approved, ready for plan
**Related:**
- [`2026-04-08-browser-extension-design.md`](./2026-04-08-browser-extension-design.md) — оригинальный extension design
- `backlog/consolidate-sites-sync-to-daemon.md` — следующий шаг по очистке dual sync

## Problem

После пейринга расширения popup статически показывает «✓ Paired». Юзер не может из popup'а управлять проксированием активной вкладки — добавить новый сайт в каталог или временно выключить уже добавленный. Сейчас единственный способ что-то сделать — дождаться bottom-right панели от content-script'а или открывать desktop client. Это ломает естественный flow «нажал на иконку расширения → быстро переключил».

## Goals

- Превратить popup в **per-tab control panel** для активной вкладки.
- Переиспользовать существующий per-user opt-out механизм через серверный sync — без введения параллельного локального store.
- После любого toggle вкладка автоматически перезагружается, чтобы effect был немедленным.
- Минимальный UI: один контрол на одну вкладку, никаких списков, табов и настроек.
- **Дополнительно:** забрать формирование PAC из renderer'а в daemon, чтобы избавиться от race condition между mutations через extension и mutations через desktop client UI.

## Non-Goals

- Полный refactor sites-layer с переездом на daemon-driven model. См. `backlog/consolidate-sites-sync-to-daemon.md` — это отдельная инициатива.
- Локальный JSON-store exclusions — отвергнуто, потому что дублирует существующий serverside per-user mechanism.
- Серверная синхронизация exclusions между устройствами одного юзера — это уже работает через `/api/sync`.
- Удаление сайтов из общего каталога через popup.
- Bulk enable/disable, поиск, навигация в каталог.
- Discovery (домен-сниффинг) триггерится из popup'а — это остаётся обязанностью content-script панели.
- SSE/WebSocket realtime notifications от daemon к renderer'у — пока хватит eventual consistency через 5-минутный background refresh.

## Background: что уже есть в проекте

Контекст для понимания почему дизайн именно такой:

### Существующий per-user enable/disable

В `client/src/renderer/sites/types.ts` уже есть `LocalSite { id, slug, label, domains, ips, enabled, updatedAt }`. Поле `enabled` — per-user opt-out флаг.

`client/src/renderer/sites/sync.ts:107` (`toggleSite`) сейчас:
1. Обновляет `localSites` в localStorage
2. Кладёт pending op в queue
3. На следующем `sync()` отправляет op `enable`/`disable` на `https://proxy.smurov.com/api/sync` через серверный device key
4. Сервер хранит per-user view `my_sites` — у каждого юзера свои `enabled` флаги

`daemon/internal/sites/manager.go` тоже синкается с тем же `/api/sync` — у него свой `SyncClient`, свой `Cache`, и `StartBackgroundRefresh(5*time.Minute)` в `daemon/cmd/main.go:48`. То есть **daemon уже умеет ходить на сервер каталога** и держит свой view of `my_sites`.

### Существующий PAC flow

`daemon/internal/api/pac.go:PacSites` — пассивное in-memory хранилище. Daemon **не** генерит список сайтов сам, он принимает их через `POST /pac/sites` от внешнего клиента и отдаёт через `GET /proxy.pac`.

`client/src/main/index.ts:330` — `ipcMain.on("pac-sites", ...)` принимает данные от renderer'а и пушит их в daemon.

`client/src/renderer/components/AppRules.tsx:269` — `useEffect` пересчитывает `enabledLocalSites` из `localSites.filter(s => s.enabled)`, разворачивает через `expandDomains()` (`client/src/renderer/sites/pac.ts`), и отправляет в main process через IPC.

То есть **PAC сейчас формируется в renderer'е**, daemon — пассивный хранитель. Если extension через daemon mutate'ит site, daemon про это знает, но renderer не знает (до своего следующего sync), и при следующем re-render renderer перепишет PAC своим устаревшим view. Это race condition.

### Существующий extension API

`daemon/internal/api/sites.go` уже имеет:
- `GET /sites/match?host=...` — возвращает `{daemon_running, in_catalog, site_id, proxy_enabled}` (read из `sitesManager.Cache().Match()`)
- `POST /sites/add` — добавляет site через `sitesManager.AddSite()`, который синкается на сервер
- `POST /sites/discover` — добавляет alt-domains
- `POST /sites/test` — проверяет URL через тоннель

Все под `requireExtensionToken` middleware (Bearer auth). Из этого набора **уже есть всё** для add-flow. Не хватает только set-enabled и (опционально) remove.

## Architecture Overview

Меняется три слоя:

1. **Daemon (Go)** забирает ownership of PAC. Vendor `expandDomains` в Go, `Manager` получает `SetEnabled` / `RebuildPAC` методы. Все mutations (add/discover/set-enabled) после успешного server sync вызывают `RebuildPAC` который сам пушит в `pacSites` и закрывает SOCKS5 connections. Новый endpoint `POST /sites/set-enabled`.

2. **Desktop client renderer (TS/React)** — `toggleSite` / `addSite` теперь идут не в собственный pendingOps queue, а **через daemon HTTP API**. Daemon делает server sync + PAC rebuild атомарно. Renderer получает success response → обновляет `localSites` view. Если daemon down — показать error «Daemon not running». IPC `pac-sites` push удаляется из main process и из renderer'а (renderer больше не отвечает за PAC). `useEffect` в `AppRules.tsx` который раньше пушил pac-sites — удаляется.

3. **Browser extension (JS, MV3)** — popup переписывается из статичного `renderPaired()` в state-machine с 6 view'шками. Service worker получает 4 новых message handler'а. `daemon-client.js` получает `setEnabled` метод. Content-script расширяется: добавляется рендер для нового состояния «in catalog но disabled».

Renderer ↔ daemon ↔ server cache синхронизация:
- Mutations: renderer → daemon → server. Daemon обновляет свой cache, формирует PAC. Renderer на success обновляет свой localStorage view.
- Reads: renderer продолжает делать свой `sync()` к серверу для общего refresh (background, manual). Daemon тоже делает свой `Refresh` каждые 5 минут. Эти два потока могут расходиться **только** в окне между mutation и следующим refresh, и расхождение видно только в read path (UI и daemon cache temporary out of sync). PAC всегда корректный, потому что daemon владеет им единолично.

## Daemon Changes

### Vendor expandDomains

Создать `daemon/internal/sites/pac_expand.go`:

```go
package sites

import "strings"

// ExpandDomains takes a list of primary site domains and returns the
// flat list that goes into the PAC file. For each input domain it adds
// "www." and "*." variants because the PAC matches by suffix.
//
// Mirrors the previous client-side implementation in
// client/src/renderer/sites/pac.ts.
func ExpandDomains(domains []string) []string {
    seen := make(map[string]bool)
    out := make([]string, 0, len(domains)*3)
    add := func(s string) {
        if s == "" || seen[s] {
            return
        }
        seen[s] = true
        out = append(out, s)
    }
    for _, d := range domains {
        clean := strings.ToLower(strings.TrimSpace(d))
        if clean == "" {
            continue
        }
        add(clean)
        if !strings.HasPrefix(clean, "www.") {
            add("www." + clean)
        }
        add("*." + clean)
    }
    return out
}
```

`daemon/internal/sites/pac_expand_test.go` — проверка: пустой вход, кейсы с/без `www.`, дедупликация, обработка whitespace и uppercase, parity с TS реализацией (несколько fixture'ов).

### Manager methods

`daemon/internal/sites/manager.go` получает:

```go
// SetEnabled toggles per-user enabled flag for a site through server sync.
// On success the cache is replaced with the fresh my_sites snapshot.
func (m *Manager) SetEnabled(siteID int, enabled bool) error {
    op := "disable"
    if enabled {
        op = "enable"
    }
    resp, err := m.client.SyncOps([]map[string]interface{}{
        {
            "op":      op,
            "site_id": siteID,
            "at":      time.Now().Unix(),
        },
    })
    if err != nil {
        return err
    }
    if len(resp.OpResults) == 0 {
        return fmt.Errorf("no op_results in response")
    }
    if r := resp.OpResults[0]; r.Status != "ok" {
        return fmt.Errorf("server: %s", r.Message)
    }
    m.cache.Replace(resp.MySites)
    return nil
}

// RemoveSite removes a site through server sync. Symmetric to AddSite.
func (m *Manager) RemoveSite(siteID int) error {
    // ... аналогично SetEnabled, op = "remove"
}

// EnabledDomains returns the flat expanded domain list for all sites
// where Enabled == true. Used to feed pacSites.
func (m *Manager) EnabledDomains() []string {
    sites := m.cache.EnabledOnly()  // new helper or filter inline
    raw := make([]string, 0, len(sites))
    for _, s := range sites {
        for _, d := range s.Domains {
            raw = append(raw, d)
        }
    }
    return ExpandDomains(raw)
}
```

`daemon/internal/sites/cache.go` получает helper `EnabledOnly() []MySite` — фильтр текущего snapshot'а по `s.Enabled == true`.

### Daemon owns PAC

В `daemon/internal/api/api.go:Server` добавляется метод:

```go
// RebuildPAC refreshes pacSites from the sitesManager cache, preserving
// the current proxy_all flag (which is owned by the renderer's UI toggle
// and pushed via the existing /pac/sites endpoint).
func (s *Server) RebuildPAC() {
    if s.sitesManager == nil {
        return
    }
    proxyAll, _ := s.pacSites.Get()
    if proxyAll {
        s.pacSites.Set(true, nil)
    } else {
        domains := s.sitesManager.EnabledDomains()
        s.pacSites.Set(false, domains)
    }
    s.tunnel.CloseAllConns()
}
```

**Note about TUN engine:** TUN rules (`/tun/rules`) — это **per-app** список приложений, не per-site. Они формируются из `KNOWN_APPS` в `client/src/renderer/components/AppRules.tsx` и не зависят от sites enable/disable. Поэтому `RebuildPAC` не трогает TUN engine.

`RebuildPAC` вызывается:
- После `Manager.SetEnabled` (через `handleSitesSetEnabled`)
- После `Manager.RemoveSite` (через `handleSitesRemove`)
- После `Manager.AddSite` (через существующий `handleSitesAdd`)
- После `Manager.AddDomains` (через существующий `handleSitesDiscover`)
- В конце `Manager.Refresh()` background tick — чтобы daemon при старте знал actual enabled list даже если renderer offline. Но это требует чтобы `Manager` мог дёрнуть `Server.RebuildPAC()` — это циклическая зависимость. Решение: передать в `Manager` callback `OnCacheReplaced func()` через setter.

### New endpoints

`daemon/internal/api/api.go:Handler()` добавляются routes под `requireExtensionToken`:

```go
mux.Handle("POST /sites/set-enabled",  requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesSetEnabled)))
mux.Handle("POST /sites/remove",       requireExtensionToken(s.tokenStore, http.HandlerFunc(s.handleSitesRemove)))
mux.Handle("OPTIONS /sites/set-enabled", ...) // CORS preflight
mux.Handle("OPTIONS /sites/remove", ...)
```

`daemon/internal/api/sites.go`:

```go
func (s *Server) handleSitesSetEnabled(w http.ResponseWriter, r *http.Request) {
    if s.sitesManager == nil {
        http.Error(w, "daemon not ready", 503)
        return
    }
    var req struct {
        SiteID  int  `json:"site_id"`
        Enabled bool `json:"enabled"`
    }
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "invalid body", 400)
        return
    }
    if req.SiteID == 0 {
        http.Error(w, "missing site_id", 400)
        return
    }
    if err := s.sitesManager.SetEnabled(req.SiteID, req.Enabled); err != nil {
        http.Error(w, err.Error(), 502)
        return
    }
    s.RebuildPAC()
    writeJSON(w, 200, map[string]interface{}{
        "ok":       true,
        "my_sites": s.sitesManager.Cache().Snapshot(),
    })
}

func (s *Server) handleSitesRemove(w http.ResponseWriter, r *http.Request) {
    // ... аналогично
}
```

`handleSitesAdd` и `handleSitesDiscover` тоже добавляют `s.RebuildPAC()` в конце (сейчас они этого не делают, потому что PAC формирует renderer; после refactor'а это становится обязанностью daemon'а).

### Match endpoint

`handleSitesMatch` уже возвращает `proxy_enabled` — это и есть нужный нам флаг. **Никаких изменений не требуется.**

Состояния которые видит extension:
- `in_catalog: false` → not_in_catalog
- `in_catalog: true, proxy_enabled: true` → proxied
- `in_catalog: true, proxy_enabled: false` → catalog_disabled (юзер локально отключил)

### Wiring в daemon main

`daemon/cmd/main.go` остаётся почти как есть. Добавляется:

```go
// Wire RebuildPAC into background refresh so the cache→PAC sync happens
// automatically every 5 minutes (and on initial refresh).
sitesManager.SetOnCacheReplaced(func() {
    srv.RebuildPAC()
})
```

`SetOnCacheReplaced` — новый setter в `Manager`. Callback вызывается из `cache.Replace` consumers (`Refresh`, `AddSite`, `AddDomains`, `SetEnabled`, `RemoveSite`). Это делает PAC actually-fresh каждый раз когда daemon видит свежий cache.

## Desktop Client Renderer Changes

### sync.ts: mutations now go through daemon

`client/src/renderer/sites/sync.ts`:

- **`toggleSite(siteId, enabled)`** меняется: вместо локального state update + pendingOp queue делает HTTP POST в daemon `/sites/set-enabled` (через main process IPC, потому что renderer не имеет прямого доступа к daemon HTTP — добавить новый IPC `daemon-set-enabled`). На success → daemon уже обновил server и PAC, и в response возвращает fresh `my_sites` snapshot. Renderer заменяет `localSites` этим snapshot'ом (одна точка истины — то что вернул daemon). На fail → throw, UI показывает error.

- **`addSite(primaryDomain, label)`** аналогично — IPC `daemon-add-site`, daemon делает `Manager.AddSite` (уже имплементировано). Возвращает реальный `site_id` сразу, не temp negative id. pendingOps больше не нужны для этого пути.

- **`removeSite(siteId)`** аналогично — новый IPC `daemon-remove-site`.

- **`sync()`** — остаётся для refresh read path. По-прежнему ходит на `https://proxy.smurov.com/api/sync` напрямую с device key. Это нужно для (a) подтянуть изменения которые сделал юзер на другом устройстве, (b) первичного bootstrap, (c) periodic refresh. Можно позже переключить на pull-from-daemon чтобы устранить дублирование (это уже Option B territory).

- **pendingOps queue** — старая логика для ops выполненных offline. Сносим: после refactor'а mutations требуют online daemon, это OK trade-off (daemon обычно up когда клиент запущен — они вместе spawn'ятся). Очищаем `storage.ts` от save/load `pendingOps`, удаляем поле из `loadState`/`SyncRequest`, удаляем `toWireOp` маппер. `sync()` теперь чисто refresh от server'а, без отправки ops.

- **`bootstrapFromBundle()`** — без изменений. Это первичный seed когда у юзера нет ничего в localStorage. Хотя в Option B этот flow тоже переедет в daemon, в Option A он остаётся на renderer'е.

### main process: new IPC handlers, simplify pac-sites

`client/src/main/index.ts`:

- **Упростить** `ipcMain.on("pac-sites", ...)` (строки 330-338): теперь принимает только `{proxy_all: boolean}`, поле `sites` игнорируется (daemon формирует sites сам). Endpoint `/pac/sites` остаётся для passthrough флага. Renderer вызывает его при изменении `allSitesOn` или `browsersOn` toggle, но не передаёт список доменов.
- На стороне daemon `handlePacSitesUpdate` (`daemon/internal/api/api.go:277`) тоже упрощается: после `pacSites.Set(req.ProxyAll, nil)` сразу вызывается `s.RebuildPAC()` который заполнит domains из cache.
- **Добавить** новые IPC handlers для mutations:
  ```ts
  ipcMain.handle("daemon-set-enabled", async (_e, siteId: number, enabled: boolean) => {
      const r = await fetch("http://127.0.0.1:9090/sites/set-enabled", {
          method: "POST",
          headers: { "Content-Type": "application/json", Authorization: `Bearer ${getDaemonToken()}` },
          body: JSON.stringify({ site_id: siteId, enabled }),
      });
      if (!r.ok) throw new Error(`daemon ${r.status}`);
      return await r.json();
  });
  ipcMain.handle("daemon-add-site", async (_e, primaryDomain: string, label: string) => { ... });
  ipcMain.handle("daemon-remove-site", async (_e, siteId: number) => { ... });
  ```
- Все три используют `getDaemonToken()` (уже есть в `client/src/main/extension.ts`) — daemon защищает `/sites/*` через тот же Bearer middleware что и для extension.

### preload.ts

`client/src/main/preload.ts` экспортирует новые методы в renderer:

```ts
contextBridge.exposeInMainWorld("appInfo", {
    // ... existing
    daemonSetEnabled: (siteId: number, enabled: boolean) => ipcRenderer.invoke("daemon-set-enabled", siteId, enabled),
    daemonAddSite: (primaryDomain: string, label: string) => ipcRenderer.invoke("daemon-add-site", primaryDomain, label),
    daemonRemoveSite: (siteId: number) => ipcRenderer.invoke("daemon-remove-site", siteId),
});
```

### AppRules.tsx: stop expanding domains, mutations through daemon

`client/src/renderer/components/AppRules.tsx`:

- Удалить `siteDomains` memo (`AppRules.tsx:268-273`) — он больше не нужен, daemon формирует domain list сам.
- Удалить import `expandDomains` из `../sites/pac`.
- `applyPac` callback (`AppRules.tsx:357`) упрощается: больше не передаёт `sites` в `setPacSites` payload, только `proxy_all` flag.
- `useEffect` в `AppRules.tsx:373-376` остаётся — он по-прежнему вызывает `applyPac(browsersOn)` при изменении `browsersOn` или `allSitesOn`. Это просто становится тонким push'ом одного флага.
- `enabledSet` memo (`AppRules.tsx:294`) остаётся — это для рисования toggle'ов в SitesGrid, не для PAC.
- `liveSites` memo (`AppRules.tsx:277`) остаётся — это для "live" indicator'ов на сайтах.
- `toggleSiteById` теперь идёт через `await window.appInfo.daemonSetEnabled(id, enabled)`. На success → обновляется через replacement из `my_sites` snapshot в response. На error — toast «Daemon not running, try reconnecting», UI откатывается.

`client/src/renderer/sites/pac.ts` — удалить полностью. Никто больше не использует `expandDomains` на стороне renderer'а.

## Browser Extension Changes

### State machine

Popup рендерит один из 6 view'шек:

| Состояние | Условие | View |
|---|---|---|
| `not_paired` | Нет токена в `chrome.storage.local` | Текущая pair-форма (без изменений) |
| `daemon_down` | Любой fetch к 127.0.0.1:9090 fail'нул с network error | "Daemon not running" + Unpair мелко |
| `system_page` | `tab.url` не http(s) (chrome://, about:, file://, etc.) | "No site to control" + Unpair мелко |
| `not_in_catalog` | `match` вернул `in_catalog: false` | host крупно + кнопка **"Проксировать этот сайт"** |
| `proxied` | `match`: `in_catalog: true, proxy_enabled: true` | host + ✓ "Proxied" + кнопка **"Выключить проксирование"** |
| `catalog_disabled` | `match`: `in_catalog: true, proxy_enabled: false` | host + "Off" + кнопка **"Включить проксирование"** |

### Popup load flow

```js
async function loadActiveTabState() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.url) return { state: "system_page", tabId: tab?.id };
  let url;
  try { url = new URL(tab.url); } catch { return { state: "system_page", tabId: tab.id }; }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return { state: "system_page", tabId: tab.id };
  }
  const host = getDomain(url.hostname);
  const resp = await sendToSW({ type: "popup_get_state", host });
  return { ...resp, tabId: tab.id };
}
```

Pair-form (`not_paired`) рендерится до этого вызова — если токена нет, никаких запросов не делаем.

### New SW message types

`extension/service-worker.js` получает:

- **`popup_get_state`** body `{host}` → SW делает `daemonClient.match(host)`. Возвращает `{state, host, site_id?}`.
  - `r.error === "daemon_down"` → `{state: "daemon_down"}`
  - `!r.data.in_catalog` → `{state: "not_in_catalog", host}`
  - `r.data.proxy_enabled === false` → `{state: "catalog_disabled", site_id, host}`
  - default → `{state: "proxied", site_id, host}`
- **`popup_add_site`** body `{host, tabId}` → `daemonClient.add(host, host)`. Возвращает `{ok, site_id?, error?}`.
- **`popup_set_enabled`** body `{site_id, enabled, tabId}` → новый метод `daemonClient.setEnabled(siteId, enabled)`. Возвращает `{ok, error?}`.

Существующие handlers (`set_token`, `clear_token`, `get_state`, `add_current_site`, `add_current_site_and_reload`, `dismiss_block`) остаются без изменений.

### New daemon-client method

`extension/lib/daemon-client.js`:

```js
setEnabled: (siteId, enabled) => call("POST", "/sites/set-enabled", { site_id: siteId, enabled }),
```

### Auto-reload pattern

Все три action-кнопки в popup'е используют один flow:

```js
async function handleAction(actionType, payload, tabId) {
  setButtonBusy();
  const resp = await sendToSW({ type: actionType, ...payload, tabId });
  if (resp.ok) {
    chrome.tabs.reload(tabId);
    window.close();
  } else {
    showError(resp.error || "Action failed");
    setButtonIdle();
  }
}
```

reload + close попапа делается из popup'а, не из SW.

### Layout

```
┌─────────────────────────────────┐
│  youtube.com                    │  ← host крупно
│  ✓ Proxied                      │  ← статусная строка
│                                 │
│  [ Выключить проксирование ]    │  ← главная кнопка
│                                 │
│  ─────────────────────────      │
│            Unpair               │  ← мелким текстом, центр
└─────────────────────────────────┘
```

Размер popup'а ~280px шириной (как сейчас). Контейнер `#root` рендерится одним switch'ем по `state`.

### Content-script

`extension/content-script.js` сейчас рендерит панель в правом нижнем углу для `proxied` / `add` / `down` / `blocked` / `discovering`. Добавляется новый state `catalog_disabled` — панелька показывает «off» вид с короткой кнопкой «Включить» (тот же `popup_set_enabled` flow).

### manifest version bump

`extension/manifest.json` → `0.2.0` (новая фича).

## Data Flow Examples

### Сценарий A: проксированный сайт → выключить через popup

1. Юзер на `https://www.youtube.com/...`, кликает иконку extension'а.
2. Popup: `chrome.tabs.query` → `tab.id=42, tab.url`. Извлекает host → `youtube.com`.
3. Popup → SW: `{type: "popup_get_state", host: "youtube.com"}`.
4. SW → daemon: `GET /sites/match?host=youtube.com`.
5. Daemon: `Cache.Match` → `{id:47, enabled:true}`. Response: `{in_catalog:true, site_id:47, proxy_enabled:true}`.
6. SW → popup: `{state: "proxied", host: "youtube.com", site_id: 47}`.
7. Popup рендерит: `youtube.com` + `✓ Proxied` + кнопка «Выключить проксирование».
8. Клик → popup → SW: `{type: "popup_set_enabled", site_id: 47, enabled: false, tabId: 42}`.
9. SW → daemon: `POST /sites/set-enabled {"site_id":47,"enabled":false}`.
10. Daemon: `sitesManager.SetEnabled(47, false)` → `client.SyncOps([{op:"disable",site_id:47}])` → server caches user's new state → response с fresh `my_sites` → `cache.Replace`. Затем `srv.RebuildPAC()` → `sitesManager.EnabledDomains()` (без site 47) → `pacSites.Set(false, ...)` → `tunnel.CloseAllConns()`. Response 200 `{"ok":true}`.
11. SW → popup: `{ok: true}`.
12. Popup: `chrome.tabs.reload(42)` → `window.close()`.
13. Браузер reload вкладки → запрос к `youtube.com` идёт через свежий PAC → direct connection.
14. Content-script на новой странице вызывает `get_state` → SW → match теперь возвращает `proxy_enabled:false` → панелька показывает «off» вид.

### Сценарий B: симметричный enable

То же самое через `popup_set_enabled` с `enabled: true`.

### Сценарий C: не в каталоге → добавить через popup

1. Popup → SW: `popup_get_state` → `{state: "not_in_catalog", host}`.
2. Popup рендерит host + кнопку «Проксировать».
3. Клик → SW: `popup_add_site` → `daemonClient.add(host, host)` → daemon `Manager.AddSite` (уже имплементировано) + `RebuildPAC` → response с `site_id`.
4. Popup → reload → close.
5. После reload юзер на странице, content-script видит state=proxied и стартует discovery (existing behavior).

### Сценарий D: toggle через desktop client UI (не через popup)

1. Юзер открывает desktop client → AppRules → кликает toggle на site `youtube.com`.
2. Renderer: `toggleSite(47, false)` → `await window.appInfo.daemonSetEnabled(47, false)`.
3. Main process IPC → POST `127.0.0.1:9090/sites/set-enabled` с Bearer.
4. Daemon: тот же flow что в сценарии A шаг 10.
5. Renderer: на success → обновляет `localSites` локально → re-render UI → toggle visually OFF.
6. Браузеры подхватывают новый PAC при следующем connection (благодаря `tunnel.CloseAllConns`).

**Никакого race condition** — daemon единственный mutator PAC.

## Error Handling

| Кейс | Поведение |
|---|---|
| Daemon упал между match и set-enabled из popup'а | `popup_set_enabled` → SW → fetch fails → response `{ok:false, error:"daemon_down"}` → popup показывает inline error «Daemon not running», кнопка возвращается в idle, reload не происходит, popup не закрывается. |
| Daemon упал при toggle через desktop client UI | IPC `daemon-set-enabled` throws → renderer catch → toast «Daemon not running, try reconnecting» → toggle в UI откатывается. |
| Server каталога недоступен (но daemon up) | `Manager.SetEnabled` возвращает error от `client.SyncOps` → handler возвращает 502 → SW/IPC вернут `{ok:false, error:"server_unreachable"}` → UI показывает error. |
| `SetEnabled` для несуществующего site_id | Server вернёт `op_results[0].status !== "ok"` → `Manager.SetEnabled` возвращает error → 502. |
| 401 от daemon на любой `/sites/*` | Текущий behavior `daemon-client.js`: `clearToken()` + `error: "unauthorized"`. Popup при следующем открытии увидит отсутствие токена → state `not_paired` → pair-форма. (Это уже работает.) |
| Активная вкладка системная (`chrome://`, `about:`) | Popup определяет это до запроса в SW → state `system_page`. |
| Юзер не paired'ил extension | Popup'у нет токена в storage → state `not_paired`. |
| daemon уже умер когда renderer пытается push pac-sites | Не applicable — `pac-sites` IPC удалён. |

## Affected Files

### Daemon (Go)

- **NEW** `daemon/internal/sites/pac_expand.go` — `ExpandDomains` функция, port из `client/src/renderer/sites/pac.ts`
- **NEW** `daemon/internal/sites/pac_expand_test.go` — unit-тесты + parity fixtures
- `daemon/internal/sites/manager.go` — методы `SetEnabled`, `RemoveSite`, `EnabledDomains`, `SetOnCacheReplaced`. Wire callback в `Refresh`, `AddSite`, `AddDomains`, `SetEnabled`, `RemoveSite`.
- `daemon/internal/sites/cache.go` — helper `EnabledOnly()` или эквивалент filter
- `daemon/internal/api/api.go` — метод `RebuildPAC()` на `Server`. Routes `/sites/set-enabled` и `/sites/remove` под `requireExtensionToken`. Упростить существующий `handlePacSitesUpdate`: после `pacSites.Set(req.ProxyAll, nil)` вызывает `s.RebuildPAC()`.
- `daemon/internal/api/sites.go` — handlers `handleSitesSetEnabled`, `handleSitesRemove`. В `handleSitesAdd` и `handleSitesDiscover` добавить `s.RebuildPAC()` после успешной mutation.
- `daemon/internal/api/sites_test.go` — extend tests для новых endpoint'ов
- `daemon/internal/api/auth_token_test.go` — добавить покрытие новых routes
- `daemon/cmd/main.go` — wire `SetOnCacheReplaced(srv.RebuildPAC)`

### Desktop Client (TS)

- `client/src/main/index.ts` — упростить `ipcMain.on("pac-sites", ...)`: payload теперь только `{proxy_all}`. Добавить три новых handler'а: `daemon-set-enabled`, `daemon-add-site`, `daemon-remove-site`. Все используют `getDaemonToken()` для Bearer.
- `client/src/main/preload.ts` — экспортировать `daemonSetEnabled`, `daemonAddSite`, `daemonRemoveSite`.
- `client/src/renderer/sites/sync.ts` — переписать `toggleSite`, `addSite`, `removeSite` на вызов через `window.appInfo.daemon*`. Удалить pendingOps queue (или оставить пустой stub если есть consumers). `sync()` остаётся как был для read refresh.
- `client/src/renderer/sites/pac.ts` — удалить (больше нет consumers)
- `client/src/renderer/components/AppRules.tsx` — удалить `siteDomains` memo и import `expandDomains`. Упростить `applyPac` (только `proxy_all` flag). Toggle handlers идут через новый sync.ts API.
- Тесты в `client/src/renderer/sites/*.test.ts` — обновить под новый flow

### Extension (JS)

- `extension/lib/daemon-client.js` — добавить `setEnabled` метод
- `extension/service-worker.js` — handlers `popup_get_state`, `popup_add_site`, `popup_set_enabled`
- `extension/popup/popup.js` — переписать `renderPaired()` в state machine `renderControlPanel(state)`, добавить `loadActiveTabState()`, action handlers с reload+close
- `extension/popup/popup.css` — стили для нового layout
- `extension/content-script.js` — добавить рендер `catalog_disabled` state в bottom-right панельке
- `extension/manifest.json` — version bump до `0.2.0`

### Tests

- Unit (Go): `ExpandDomains` (включая parity fixtures с TS реализацией для регрессий)
- Unit (Go): `Manager.SetEnabled` / `RemoveSite` / `EnabledDomains` / `SetOnCacheReplaced` (mock SyncClient)
- Unit (Go): `Server.RebuildPAC` (smoke test что pacSites обновляется и CloseAllConns вызывается)
- Integration (Go): новые endpoint'ы под auth middleware (401 без токена, 200 с токеном, 502 при server error, body validation)
- Manual (Electron): toggle сайта в AppRules → проверить что browsers подхватили новый PAC, daemon log показывает `RebuildPAC` calls
- Manual (browser): pair → site proxied → disable из popup → reload → site direct → enable → reload → site proxied; также проверить что content-script панель синхронизирована
- Manual: cross-source consistency — toggle через popup, проверить что desktop client UI показывает новое состояние при следующем `sync()` (либо мгновенно если успели)

## Open Questions

Нет.

## Acceptance Criteria

- На активной вкладке с проксируемым сайтом popup показывает host, статус «Proxied» и кнопку «Выключить проксирование». Клик → reload → запрос идёт direct.
- На активной вкладке с локально выключенным сайтом popup показывает host, статус «Off» и кнопку «Включить проксирование». Клик → reload → запрос идёт через proxy.
- На активной вкладке с сайтом не из каталога popup показывает host и кнопку «Проксировать». Клик → site добавляется → reload → запрос идёт через proxy.
- Toggle в desktop client UI и toggle в popup'е используют один и тот же путь и **немедленно** обновляют PAC (без 5-минутной задержки).
- Никаких race conditions: mutation через любой из источников (popup, UI клиент) не перезаписывается следом push'ом от другого источника.
- При daemon down любой mutation возвращает понятную ошибку, не silent fail.
- Pair-форма работает как раньше (regression-safe).
- TUN AppRules продолжает работать корректно (regression-safe — это критичный flow).
- `make test` всех существующих и новых пакетов проходит.
- Manual cross-platform проверка: macOS + Windows.

## Risk Assessment

**Высокий риск регрессии в:** TUN AppRules. Это main контрольная панель, юзер видит её каждый день. Любое изменение в `toggleSiteById` flow трогает её. Mitigation: тщательное manual testing на TUN режиме до и после refactor'а. E2E проверка что toggle сайта реально меняет network behavior.

**Средний риск:** конфликт renderer'ского `sync()` (read-only background refresh) и daemon'ского mutation flow. Renderer может на follow-up sync получить устаревший snapshot если timing неудачный. Mitigation: `sync()` теперь чисто refresh от server'а (server == authoritative), и renderer всегда подтягивает freshest snapshot. Consistency window — секунды.

**Низкий риск:** popup и extension changes. Изолированные, не трогают critical paths.
