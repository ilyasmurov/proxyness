# Consolidate sites sync to daemon (single source of truth)

**What:** Сейчас sites layer в проекте имеет **два независимых sync client'а** к серверному `/api/sync`:

1. **Renderer-side** — `client/src/renderer/sites/sync.ts` (260+ строк): держит `localSites` в localStorage, своя очередь pendingOps, свой bootstrap from bundle, своя legacy migration. Используется в `AppRules.tsx` (TUN per-site toggles), в SitesGrid, в browser PAC.
2. **Daemon-side** — `daemon/internal/sites/manager.go` + `client.go`: держит свой `Cache`, свой `SyncClient`, background refresh каждые 5 минут, методы `Refresh`, `AddSite`, `AddDomains`. Используется в `/sites/match`, `/sites/add`, `/sites/discover` endpoint'ах для browser extension.

Оба sync'аются с одним и тем же серверным state, но независимо друг от друга. Они eventually consistent через server, но между sync'ами могут расходиться. Это вызывает racy behavior когда mutation идёт через один sync (например extension → daemon), а другой (renderer) ещё не успел узнать.

**Why:**

- **Race conditions при mutation через extension:** popup жмёт «выключить сайт» → daemon отправляет op на сервер → daemon обновляет свой PAC. Через несколько секунд renderer делает свой sync, видит изменение, и пушит свой `/pac/sites` который может **переписать** свежий daemon-state на ещё-более-свежий-но-другой (а в худшем случае — старый, если renderer не успел сделать pull). Сейчас в Option A для popup-фичи мы это решаем тем что **renderer перестаёт пушить pac-sites вообще**, но renderer всё ещё держит свой `localSites` для UI и он расходится с daemon cache.
- **Дублирование sync logic:** pendingOps queue, bootstrap from bundle, legacy migration, retry — всё это есть в двух местах с разной семантикой. Менять что-то в server sync API требует править оба.
- **Offline behavior разный:** renderer first-class работает offline (localStorage). Daemon при offline просто не может Refresh. Не критично, но создаёт впечатление двух разных систем.
- **Технический долг растёт:** каждая новая фича по sites должна решать «куда это идёт — в renderer-sync или daemon-sync». Сейчас выбор делается ad-hoc.

**How (sketch — детали при реальном планировании):**

1. **Daemon становится единственный sync-client.** Все операции (`add`, `remove`, `enable`, `disable`, `add_domain`) идут через daemon HTTP API → daemon синкается с сервером → daemon обновляет свой cache + persist на диск.
2. **Daemon персистит cache на диск** — например `~/.config/proxyness/sites-cache.json`. Это даёт renderer'у offline view: при старте daemon грузит cache с диска, и сразу отвечает на API запросы даже до первого server sync.
3. **Daemon API расширяется:**
   - `GET /sites` — список всех sites (с enabled flag)
   - `POST /sites/add` (уже есть)
   - `POST /sites/remove` body `{site_id}`
   - `POST /sites/set-enabled` body `{site_id, enabled}`
   - `POST /sites/add-domain` body `{site_id, domain}` (или унифицировать с discover)
   - `GET /sites/events` SSE для realtime push updates в renderer (вместо polling)
4. **Renderer переписывается с localStorage на daemon-driven:**
   - `useSites()` hook → теперь читает sites через `fetch('http://127.0.0.1:9090/sites')` + SSE на `/sites/events`
   - `sync.ts` снести (или превратить в тонкий wrapper над daemon API)
   - `storage.ts` снести (localStorage больше не используется для sites)
   - `pac.ts` снести (daemon сам формирует PAC)
   - `bootstrapFromBundle` → перенести в daemon (он грузит seed.json при первом старте если cache пуст)
   - `runLegacyMigrationIfNeeded` → перенести в daemon или вырезать (зависит от того сколько ещё юзеров с legacy state)
5. **AppRules.tsx и SitesGrid** — переключить на новый `useSites` API. Memoized derivations (`enabledSet`, `enabledLocalSites`) остаются, просто базируются на data из daemon, а не из localStorage.
6. **Pending ops при offline daemon** — renderer теперь зависит от daemon. Если daemon down при попытке toggle, нужно либо: (a) показать ошибку UX с retry, (b) держать optimistic queue в renderer'е и retry'ить когда daemon up, (c) акцептить что без daemon UI заблокирован. Думаю (a) — простейший.
7. **Migration story:** при первом запуске новой версии renderer обнаруживает legacy `localSites` в localStorage → отправляет всё в daemon через bulk-import endpoint → удаляет из localStorage. Один-раз операция.

**Cost:** Большой. Грубая оценка: 1000-2000 строк кода (туда и сюда), плюс пересмотр существующих unit/integration тестов. Минимум неделя focused work, скорее две. Высокий риск регрессий в TUN AppRules (network может сломаться), нужны E2E проверки на каждом ОС.

**Blockers:**

- Желательно иметь стабильный test pipeline — сейчас на каждый push в main идёт deploy, что создаёт давление на скорость. Refactor такого размера лучше делать в feature branch с собственными CI runs.
- Имеет смысл сначала закрыть Option A (popup-фича) и пожить с ней пару недель, чтобы понять реальные пользовательские паттерны и какие edge cases вылезут — это уточнит scope refactor'а.

**Откуда взялось:** Появилось при self-review дизайна для popup-фичи (см. `docs/superpowers/specs/2026-04-08-popup-control-panel-design.md`). Я предложил Option B — полный refactor — чтобы решить race condition правильно, но мы сознательно выбрали Option A (минимальный fix) и вынесли B сюда как отдельную инициативу.
