# Per-App TLS Toggle — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow disabling TLS wrapping per-app in TUN mode so apps like Telegram/Discord connect through the proxy via raw TCP + HMAC instead of TLS, reducing latency from double encryption.

**Architecture:** Server switches from `tls.Listen` to `net.Listen` on port 443. A pre-TLS mux peeks the first byte: `0x16` (TLS ClientHello) wraps in TLS and routes to existing mux; `0x01` (our protocol) goes directly to proxy handler without TLS. Daemon checks a per-app `noTLSApps` set in rules to decide TLS vs raw. Client UI adds a TLS toggle per app in "Selected apps" mode.

**Tech Stack:** Go (server/daemon), React + TypeScript (client), Electron IPC

---

### Task 1: Server — Pre-TLS Mux

**Files:**
- Modify: `server/internal/mux/mux.go`
- Modify: `server/cmd/main.go`

- [ ] **Step 1: Add pre-TLS routing to mux.go**

Add a `PreTLSMux` that wraps the existing `ListenerMux`. It accepts raw TCP connections, peeks the first byte, and either wraps in TLS or passes raw to the proxy handler.

In `server/internal/mux/mux.go`, add after the existing `ListenerMux` struct (after line 95):

```go
// PreTLSMux sits in front of ListenerMux. It peeks the first byte of each
// raw TCP connection to decide whether to perform a TLS handshake.
//   0x16 = TLS ClientHello → wrap in TLS → route via ListenerMux (proxy + HTTP admin)
//   0x01 = raw proxy protocol → send directly to proxy handler (no TLS)
type PreTLSMux struct {
	ln           net.Listener
	tlsCfg       *tls.Config
	proxyHandler func(net.Conn)
	httpHandler  http.Handler
}

func NewPreTLSMux(ln net.Listener, tlsCfg *tls.Config, proxyHandler func(net.Conn), httpHandler http.Handler) *PreTLSMux {
	return &PreTLSMux{ln: ln, tlsCfg: tlsCfg, proxyHandler: proxyHandler, httpHandler: httpHandler}
}

func (m *PreTLSMux) Serve() error {
	httpConns := make(chan net.Conn, 64)
	httpLn := &chanListener{ch: httpConns, addr: m.ln.Addr()}
	go http.Serve(httpLn, m.httpHandler)

	for {
		conn, err := m.ln.Accept()
		if err != nil {
			close(httpConns)
			return err
		}
		go m.route(conn, httpConns)
	}
}

func (m *PreTLSMux) route(conn net.Conn, httpConns chan net.Conn) {
	pc := NewPeekConn(conn)
	b, err := pc.PeekByte()
	if err != nil {
		conn.Close()
		return
	}

	if b == 0x16 {
		// TLS ClientHello — handshake, then route by next byte (proxy vs HTTP)
		tlsConn := tls.Server(pc, m.tlsCfg)
		if err := tlsConn.Handshake(); err != nil {
			tlsConn.Close()
			return
		}
		inner := NewPeekConn(tlsConn)
		ib, err := inner.PeekByte()
		if err != nil {
			tlsConn.Close()
			return
		}
		if IsProxyProtocol(ib) {
			m.proxyHandler(inner)
		} else {
			httpConns <- inner
		}
	} else if IsProxyProtocol(b) {
		// Raw proxy protocol — no TLS
		m.proxyHandler(pc)
	} else {
		// Unknown — close
		conn.Close()
	}
}

func (m *PreTLSMux) Close() error { return m.ln.Close() }
```

Add `"crypto/tls"` to the import block at the top of `mux.go`.

- [ ] **Step 2: Update server main.go to use PreTLSMux**

In `server/cmd/main.go`, replace lines 64-77:

```go
// Old:
//   ln, err := tls.Listen("tcp", *addr, tlsCfg)
//   ...
//   m := mux.NewListenerMux(ln, ...)
//   m.Serve()

// New:
ln, err := net.Listen("tcp", *addr)
if err != nil {
	log.Fatalf("listen: %v", err)
}
log.Printf("server listening on %s", *addr)

adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass, "/data/downloads")
proxyHandler := &proxy.Handler{DB: database, Tracker: tracker}

m := mux.NewPreTLSMux(ln, tlsCfg,
	func(conn net.Conn) { proxyHandler.Handle(conn) },
	adminHandler,
)
m.Serve()
```

Remove `"crypto/tls"` from the `tls.Listen` usage (it's still needed for `tls.LoadX509KeyPair` and `tls.Config`). Remove unused import of `mux` if `NewListenerMux` is no longer called — actually keep the `mux` import since we use `mux.NewPreTLSMux`.

- [ ] **Step 3: Build and verify server compiles**

Run:
```bash
cd server && go build ./cmd/
```
Expected: builds without errors.

- [ ] **Step 4: Commit**

```bash
git add server/internal/mux/mux.go server/cmd/main.go
git commit -m "feat: pre-TLS mux — auto-detect TLS vs raw proxy on port 443"
```

---

### Task 2: Daemon — noTLSApps in Rules

**Files:**
- Modify: `daemon/internal/tun/rules.go`
- Modify: `daemon/internal/tun/rules_test.go`

- [ ] **Step 1: Write failing tests for ShouldUseTLS**

In `daemon/internal/tun/rules_test.go`, add:

```go
func TestShouldUseTLS_Default(t *testing.T) {
	r := NewRules()
	if !r.ShouldUseTLS("/Applications/Telegram.app") {
		t.Error("default should be TLS on")
	}
}

func TestShouldUseTLS_NoTLS(t *testing.T) {
	r := NewRules()
	r.SetNoTLSApps([]string{"/Applications/Telegram.app"})
	if r.ShouldUseTLS("/Applications/Telegram.app") {
		t.Error("telegram should be no-TLS")
	}
	if !r.ShouldUseTLS("/Applications/Discord.app") {
		t.Error("discord should still be TLS")
	}
}

func TestRulesJSON_NoTLS(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyOnly)
	r.SetApps([]string{"/Applications/Telegram.app"})
	r.SetNoTLSApps([]string{"/Applications/Telegram.app"})

	data := r.ToJSON()
	r2 := NewRules()
	if err := r2.FromJSON(data); err != nil {
		t.Fatalf("from json: %v", err)
	}
	if r2.ShouldUseTLS("/Applications/Telegram.app") {
		t.Error("no-tls should survive JSON round-trip")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
cd daemon && go test ./internal/tun/ -run "TestShouldUseTLS|TestRulesJSON_NoTLS" -v
```
Expected: FAIL — `ShouldUseTLS` and `SetNoTLSApps` do not exist.

- [ ] **Step 3: Implement noTLSApps in rules.go**

Add `noTLSApps` field to `Rules` struct and `rulesJSON`. In `daemon/internal/tun/rules.go`:

Update the `Rules` struct (line 16):
```go
type Rules struct {
	mu        sync.RWMutex
	mode      Mode
	apps      map[string]bool
	noTLSApps map[string]bool
}
```

Update `rulesJSON` (line 22):
```go
type rulesJSON struct {
	Mode      Mode     `json:"mode"`
	Apps      []string `json:"apps"`
	NoTLSApps []string `json:"no_tls_apps,omitempty"`
}
```

Update `NewRules()` (line 27):
```go
func NewRules() *Rules {
	return &Rules{
		mode:      ModeProxyAllExcept,
		apps:      make(map[string]bool),
		noTLSApps: make(map[string]bool),
	}
}
```

Add new methods after `GetApps()` (after line 61):
```go
func (r *Rules) SetNoTLSApps(apps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noTLSApps = make(map[string]bool, len(apps))
	for _, a := range apps {
		r.noTLSApps[strings.ToLower(a)] = true
	}
}

func (r *Rules) ShouldUseTLS(appPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lower := strings.ToLower(appPath)
	for app := range r.noTLSApps {
		if lower == app || strings.HasPrefix(lower, app+"/") || strings.HasPrefix(lower, app+"\\") {
			return false
		}
	}
	return true
}
```

Update `ToJSON()` (line 96):
```go
func (r *Rules) ToJSON() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]string, 0, len(r.apps))
	for a := range r.apps {
		apps = append(apps, a)
	}
	noTLSApps := make([]string, 0, len(r.noTLSApps))
	for a := range r.noTLSApps {
		noTLSApps = append(noTLSApps, a)
	}
	data, _ := json.Marshal(rulesJSON{
		Mode:      r.mode,
		Apps:      apps,
		NoTLSApps: noTLSApps,
	})
	return data
}
```

Update `FromJSON()` (line 110):
```go
func (r *Rules) FromJSON(data []byte) error {
	var rj rulesJSON
	if err := json.Unmarshal(data, &rj); err != nil {
		return err
	}
	r.SetMode(rj.Mode)
	r.SetApps(rj.Apps)
	r.SetNoTLSApps(rj.NoTLSApps)
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd daemon && go test ./internal/tun/ -v
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/rules.go daemon/internal/tun/rules_test.go
git commit -m "feat: add noTLSApps to TUN rules with ShouldUseTLS method"
```

---

### Task 3: Daemon — Raw TCP Proxy in Engine

**Files:**
- Modify: `daemon/internal/tun/engine.go`

- [ ] **Step 1: Pass appPath to proxyTCP and proxyUDP**

Currently `proxyTCP` and `proxyUDP` don't receive `appPath`. Update their signatures and call sites.

In `engine.go`, update `handleTCP` (line 460):
```go
// Old:
e.proxyTCP(conn, dstAddr, dstPort)
// New:
e.proxyTCP(conn, dstAddr, dstPort, appPath)
```

Update `handleUDP` (line 542):
```go
// Old:
e.proxyUDP(conn, dstAddr, dstPort)
// New:
e.proxyUDP(conn, dstAddr, dstPort, appPath)
```

- [ ] **Step 2: Add raw TCP path to proxyTCP**

Replace `proxyTCP` (lines 465-495) with:

```go
func (e *Engine) proxyTCP(local net.Conn, dstAddr string, dstPort uint16, appPath string) {
	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		log.Printf("[tun] protected dial failed: %v", err)
		return
	}

	var server net.Conn
	if e.rules.ShouldUseTLS(appPath) {
		tlsConn := tls.Client(rawConn, &tls.Config{
			InsecureSkipVerify: true,
		})
		if err := tlsConn.Handshake(); err != nil {
			log.Printf("[tun] tls handshake failed: %v", err)
			rawConn.Close()
			return
		}
		server = tlsConn
	} else {
		server = rawConn
		log.Printf("[tun] TCP %s:%d — raw (no TLS)", dstAddr, dstPort)
	}
	defer server.Close()

	if err := proto.WriteAuth(server, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(server)
	if err != nil || !ok {
		return
	}

	if err := proto.WriteMsgType(server, proto.MsgTypeTCP); err != nil {
		return
	}
	if err := proto.WriteConnect(server, dstAddr, dstPort); err != nil {
		return
	}
	ok, err = proto.ReadResult(server)
	if err != nil || !ok {
		return
	}

	proto.CountingRelay(local, server, func(in, out int64) {
		e.meter.Add(in, out)
	})
}
```

- [ ] **Step 3: Add raw TCP path to proxyUDP**

Replace `proxyUDP` (lines 549-594) with:

```go
func (e *Engine) proxyUDP(local net.Conn, dstAddr string, dstPort uint16, appPath string) {
	defer local.Close()

	rawConn, err := protectedDial("tcp", e.serverAddr)
	if err != nil {
		return
	}

	var server net.Conn
	if e.rules.ShouldUseTLS(appPath) {
		tlsConn := tls.Client(rawConn, &tls.Config{
			InsecureSkipVerify: true,
		})
		if err := tlsConn.Handshake(); err != nil {
			rawConn.Close()
			return
		}
		server = tlsConn
	} else {
		server = rawConn
	}
	defer server.Close()

	if err := proto.WriteAuth(server, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(server)
	if err != nil || !ok {
		return
	}

	if err := proto.WriteMsgType(server, proto.MsgTypeUDP); err != nil {
		return
	}
	if err := proto.WriteConnect(server, dstAddr, dstPort); err != nil {
		return
	}

	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			if err := proto.WriteUDPFrame(server, dstAddr, dstPort, buf[:n]); err != nil {
				return
			}
			e.meter.Add(0, int64(n))
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, _, payload, err := proto.ReadUDPFrame(server)
			if err != nil {
				return
			}
			e.meter.Add(int64(len(payload)), 0)
			if _, err := local.Write(payload); err != nil {
				return
			}
		}
	}()

	<-done
}
```

- [ ] **Step 4: Build and verify daemon compiles**

Run:
```bash
cd daemon && go build ./cmd/
```
Expected: builds without errors.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/engine.go
git commit -m "feat: daemon supports raw TCP proxy when TLS disabled per app"
```

---

### Task 4: Client UI — TLS Toggle Per App

**Files:**
- Modify: `client/src/renderer/components/AppRules.tsx`

- [ ] **Step 1: Add noTLS state and persistence**

In `AppRules.tsx`, add storage key constant (after line 81):

```typescript
const STORAGE_KEY_NO_TLS = "smurov-proxy-no-tls";
```

Add loader/saver functions (after `saveEnabledSites`, line 108):

```typescript
function loadNoTLS(): Set<string> {
  const saved = localStorage.getItem(STORAGE_KEY_NO_TLS);
  if (saved) {
    try { return new Set(JSON.parse(saved)); } catch {}
  }
  return new Set();
}

function saveNoTLS(noTLS: Set<string>) {
  localStorage.setItem(STORAGE_KEY_NO_TLS, JSON.stringify([...noTLS]));
}
```

- [ ] **Step 2: Add noTLS state to component**

Inside `AppRules` component, after the `browsersOn` state (line 129), add:

```typescript
const [noTLS, setNoTLS] = useState<Set<string>>(loadNoTLS);
```

- [ ] **Step 3: Update TypeScript types for rules**

Update the `Window` interface `tunProxy` types (lines 9-10):

```typescript
getRules: () => Promise<{ mode: string; apps: string[]; no_tls_apps?: string[] }>;
setRules: (rules: { mode: string; apps: string[]; no_tls_apps?: string[] }) => void;
```

- [ ] **Step 4: Update applyRules to include no_tls_apps**

Replace `applyRules` (lines 198-213):

```typescript
const applyRules = useCallback((m: Mode, enabledIds: Set<string>, resolvedApps: ResolvedApp[], bOn: boolean, eSites: Set<string>, noTLSIds: Set<string>) => {
  if (m === "all") {
    window.tunProxy?.setRules({ mode: "proxy_all_except", apps: [] });
    window.sysproxy?.setPacSites({ proxy_all: true, sites: [] });
    window.sysproxy?.enable();
  } else {
    const paths: string[] = [];
    const noTLSPaths: string[] = [];
    for (const r of resolvedApps) {
      if (enabledIds.has(r.app.id)) {
        paths.push(...r.paths);
        if (noTLSIds.has(r.app.id)) {
          noTLSPaths.push(...r.paths);
        }
      }
    }
    window.tunProxy?.setRules({ mode: "proxy_only", apps: paths, no_tls_apps: noTLSPaths });
    applyPac(bOn, eSites);
  }
}, [applyPac]);
```

- [ ] **Step 5: Update all applyRules call sites**

Every call to `applyRules` needs the `noTLS` parameter. Update each:

`handleModeChange` (line 217):
```typescript
const handleModeChange = (m: Mode) => {
  setMode(m);
  applyRules(m, enabled, resolved, browsersOn, enabledSites, noTLS);
};
```

`toggleApp` (line 225):
```typescript
applyRules(mode, next, resolved, browsersOn, enabledSites, noTLS);
```

- [ ] **Step 6: Add toggleNoTLS handler**

After `toggleApp` (after line 228):

```typescript
const toggleNoTLS = (appId: string) => {
  setNoTLS((prev) => {
    const next = new Set(prev);
    if (next.has(appId)) next.delete(appId);
    else next.add(appId);
    saveNoTLS(next);
    applyRules(mode, enabled, resolved, browsersOn, enabledSites, next);
    return next;
  });
};
```

- [ ] **Step 7: Restore noTLS state from daemon rules**

In the `useEffect` that loads rules (inside the `getRules().then` callback, around line 153), after setting `enabled`, add restoration of `noTLS`:

```typescript
window.tunProxy?.getRules().then((rules) => {
  if (rules.mode === "proxy_all_except") {
    setMode("all");
  } else if (rules.mode === "proxy_only") {
    if (rules.apps?.length > 0) {
      const savedPaths = new Set(rules.apps.map((a) => a.toLowerCase()));
      const enabledIds = new Set<string>();
      for (const app of KNOWN_APPS) {
        for (const sp of savedPaths) {
          if (app.keywords.some((kw) => sp.includes(kw))) {
            enabledIds.add(app.id);
            break;
          }
        }
      }
      setEnabled(enabledIds);
    }
    // Restore noTLS from daemon rules
    if (rules.no_tls_apps?.length) {
      const noTLSPaths = new Set(rules.no_tls_apps.map((a) => a.toLowerCase()));
      const noTLSIds = new Set<string>();
      for (const app of KNOWN_APPS) {
        for (const sp of noTLSPaths) {
          if (app.keywords.some((kw) => sp.includes(kw))) {
            noTLSIds.add(app.id);
            break;
          }
        }
      }
      setNoTLS(noTLSIds);
      saveNoTLS(noTLSIds);
    }
  }
});
```

- [ ] **Step 8: Update AppToggle to show TLS toggle**

Replace the `AppToggle` component (lines 449-484):

```tsx
function AppToggle({ app, isOn, noTLS, onToggle, onToggleTLS }: {
  app: KnownApp;
  isOn: boolean;
  noTLS: boolean;
  onToggle: (id: string) => void;
  onToggleTLS: (id: string) => void;
}) {
  return (
    <div
      style={{
        display: "flex", alignItems: "center", gap: 10,
        padding: "6px 8px", borderRadius: 6,
        background: isOn ? "rgba(59,130,246,0.08)" : "transparent",
      }}
    >
      <div
        onClick={() => onToggle(app.id)}
        style={{ display: "flex", alignItems: "center", gap: 10, flex: 1, cursor: "pointer" }}
      >
        <div style={{
          width: 28, height: 28, borderRadius: 6,
          background: isOn ? app.color : "#333",
          display: "flex", alignItems: "center", justifyContent: "center",
          fontSize: 12, fontWeight: 700, color: isOn ? "#fff" : "#666",
          flexShrink: 0,
        }}>
          {app.letter}
        </div>
        <div style={{ flex: 1 }}>
          <div style={{ fontSize: 13, color: isOn ? "#eee" : "#666" }}>{app.name}</div>
          {isOn && noTLS && (
            <div style={{ fontSize: 10, color: "#f59e0b" }}>without TLS</div>
          )}
        </div>
      </div>
      {isOn && (
        <div
          onClick={() => onToggleTLS(app.id)}
          title={noTLS ? "TLS off — raw connection" : "TLS on — encrypted"}
          style={{
            fontSize: 10, padding: "2px 6px", borderRadius: 4, cursor: "pointer",
            background: noTLS ? "rgba(245,158,11,0.15)" : "rgba(34,197,94,0.15)",
            color: noTLS ? "#f59e0b" : "#22c55e",
            border: `1px solid ${noTLS ? "#f59e0b33" : "#22c55e33"}`,
            whiteSpace: "nowrap",
          }}
        >
          TLS {noTLS ? "OFF" : "ON"}
        </div>
      )}
      <div
        onClick={() => onToggle(app.id)}
        style={{
          width: 36, height: 20, borderRadius: 10,
          background: isOn ? "#3b82f6" : "#333",
          position: "relative", transition: "background 0.2s", cursor: "pointer",
        }}
      >
        <div style={{
          width: 16, height: 16, borderRadius: 8, background: "#fff",
          position: "absolute", top: 2, left: isOn ? 18 : 2,
          transition: "left 0.2s",
        }} />
      </div>
    </div>
  );
}
```

- [ ] **Step 9: Update AppToggle usage in render**

Replace the app toggles render (line 440-442):

```tsx
{resolved.map(({ app }) => (
  <AppToggle
    key={app.id}
    app={app}
    isOn={enabled.has(app.id)}
    noTLS={noTLS.has(app.id)}
    onToggle={toggleApp}
    onToggleTLS={toggleNoTLS}
  />
))}
```

- [ ] **Step 10: Build and verify client compiles**

Run:
```bash
cd client && npm run build
```
Expected: builds without errors.

- [ ] **Step 11: Commit**

```bash
git add client/src/renderer/components/AppRules.tsx
git commit -m "feat: TLS toggle per app in selected apps UI"
```

---

### Task 5: Deploy and Test

- [ ] **Step 1: Build server**

```bash
make build-server
```

- [ ] **Step 2: Build daemon**

```bash
make build-daemon
```

- [ ] **Step 3: Build client**

```bash
make build-client
```

- [ ] **Step 4: Run all Go tests**

```bash
make test
```
Expected: all tests pass.

- [ ] **Step 5: Manual test**

1. Deploy server to VPS
2. Launch client, connect in TUN mode
3. Switch to "Selected apps", enable Telegram with TLS ON — verify Telegram works
4. Toggle TLS OFF for Telegram — verify Telegram still works, check server logs for raw (non-TLS) connections
5. Verify admin panel still accessible via HTTPS

- [ ] **Step 6: Commit version bump and tag**

```bash
# Bump version in package.json, then:
git add -A
git commit -m "feat: per-app TLS toggle — raw TCP proxy for reduced latency"
```
