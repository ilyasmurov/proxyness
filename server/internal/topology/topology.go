// Package topology builds the live traffic map shown on the admin
// "Topology" page (PRXNS-23): which exits and bridges the config service
// lists, whether the control plane can reach each of them, how many devices
// ride each path, and the health of the control plane's own parts.
//
// Probing runs in this process (the control plane's proxy binary): the
// browser cannot open UDP or talk to other hosts. It is lazy — the loop
// starts on the first Snapshot() call, repeats every Interval while calls
// keep coming, and stops after IdleStop without any. Exits run the same
// binary but nobody asks them, so they never probe.
package topology

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	Interval     = 15 * time.Second
	IdleStop     = 2 * time.Minute
	probeTimeout = 5 * time.Second
)

// State of a node, edge or check.
type State string

const (
	OK      State = "ok"
	Down    State = "down"
	Unknown State = "unknown"
)

// Entry is one row of the config service's servers list.
type Entry struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Addr  string `json:"addr"`
	Kind  string `json:"kind,omitempty"` // "exit" (default) | "bridge"
	Via   string `json:"via,omitempty"`  // bridge → exit id
}

// Source is everything the prober needs from the host process.
type Source struct {
	ConfigAddr string // e.g. http://172.17.0.1:8443
	AdminUser  string
	AdminPass  string
	// LocalDevices returns this host's device count (the primary's, on the
	// control plane) for the replica check.
	LocalDevices func() (int, error)
	// DBPing checks the local Postgres.
	DBPing func(ctx context.Context) error
	// DNSHost is the name clients poll for config (and the landing).
	DNSHost string
}

type Check struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	State     State     `json:"state"`
	LatencyMS int64     `json:"latency_ms"`
	Devices   int       `json:"devices"`
	CheckedAt time.Time `json:"checked_at"`
	Detail    string    `json:"detail,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type Node struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind"` // clients|bridge|exit|internet|control|source|egress
	Label        string  `json:"label"`
	Sub          string  `json:"sub,omitempty"`
	Addr         string  `json:"addr,omitempty"`
	Via          string  `json:"via,omitempty"`
	State        State   `json:"state"`
	LatencyMS    int64   `json:"latency_ms,omitempty"`
	Devices      int     `json:"devices"`
	TotalDevices int     `json:"total_devices,omitempty"`
	Error        string  `json:"error,omitempty"`
	Checks       []Check `json:"checks,omitempty"`
}

type Edge struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	State     State  `json:"state"`
	Label     string `json:"label"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Devices   int    `json:"devices"`
	Error     string `json:"error,omitempty"`
}

type Snapshot struct {
	CheckedAt time.Time `json:"checked_at"`
	IntervalS int       `json:"interval_s"`
	Overall   State     `json:"overall"`
	Nodes     []Node    `json:"nodes"`
	Edges     []Edge    `json:"edges"`
	Checks    []Check   `json:"checks"`
}

// activeConn mirrors the exit's /admin/api/stats/active row.
type activeConn struct {
	DeviceID int    `json:"device_id"`
	RemoteIP string `json:"remote_ip"`
}

// probeResult is what one admin-API probe of an exit or bridge yields.
type probeResult struct {
	ok        bool
	latencyMS int64
	err       string
	conns     []activeConn
	total     int // total_devices from /stats/overview (exits only)
}

// Prober owns the cached snapshot and the lazy loop.
type Prober struct {
	src     Source
	client  *http.Client
	now     func() time.Time
	probeFn func(ctx context.Context, addr string, withOverview bool) probeResult

	mu      sync.Mutex
	snap    *Snapshot
	lastReq time.Time
	running bool
}

func New(src Source) *Prober {
	p := &Prober{
		src: src,
		client: &http.Client{
			Timeout:   probeTimeout,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DisableKeepAlives: true},
		},
		now: time.Now,
	}
	p.probeFn = p.probeAdmin
	return p
}

// Snapshot returns the latest map, probing synchronously the first time so
// the page never paints an all-grey diagram, and keeps the loop alive.
func (p *Prober) Snapshot() *Snapshot {
	p.mu.Lock()
	p.lastReq = p.now()
	snap := p.snap
	start := !p.running
	if start {
		p.running = true
	}
	p.mu.Unlock()

	if snap == nil {
		snap = p.probeOnce()
		p.mu.Lock()
		p.snap = snap
		p.mu.Unlock()
	}
	if start {
		go p.loop()
	}
	return snap
}

func (p *Prober) loop() {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for range t.C {
		p.mu.Lock()
		idle := p.now().Sub(p.lastReq) > IdleStop
		if idle {
			p.running = false
		}
		p.mu.Unlock()
		if idle {
			return
		}
		snap := p.probeOnce()
		p.mu.Lock()
		p.snap = snap
		p.mu.Unlock()
	}
}

// ---------------------------------------------------------------- probes

func (p *Prober) basicGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(p.src.AdminUser, p.src.AdminPass)
	return p.client.Do(req)
}

// fetchEntries reads the servers list and the egress proxy URL from the
// config service.
func (p *Prober) fetchEntries(ctx context.Context) ([]Entry, string, int64, error) {
	start := p.now()
	resp, err := p.basicGet(ctx, strings.TrimRight(p.src.ConfigAddr, "/")+"/api/admin/services")
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	lat := p.now().Sub(start).Milliseconds()
	if resp.StatusCode != http.StatusOK {
		return nil, "", lat, fmt.Errorf("config service: HTTP %d", resp.StatusCode)
	}
	var cfg map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return nil, "", lat, fmt.Errorf("config service: %w", err)
	}
	var entries []Entry
	if raw := strings.TrimSpace(cfg["servers"]); raw != "" {
		if err := json.Unmarshal([]byte(raw), &entries); err != nil {
			return nil, "", lat, fmt.Errorf("servers list: %w", err)
		}
	}
	for i := range entries {
		if entries[i].Kind == "" {
			entries[i].Kind = "exit"
		}
	}
	return entries, strings.TrimSpace(cfg["egress_proxy"]), lat, nil
}

// probeAdmin hits an exit's admin API at addr (directly, or through a
// bridge's DNAT). Reachability + latency come from /stats/active; exits also
// get /stats/overview for the replica check.
func (p *Prober) probeAdmin(ctx context.Context, addr string, withOverview bool) probeResult {
	start := p.now()
	resp, err := p.basicGet(ctx, "https://"+addr+"/admin/api/stats/active")
	if err != nil {
		return probeResult{err: err.Error(), latencyMS: p.now().Sub(start).Milliseconds()}
	}
	defer resp.Body.Close()
	lat := p.now().Sub(start).Milliseconds()
	if resp.StatusCode != http.StatusOK {
		return probeResult{err: fmt.Sprintf("HTTP %d", resp.StatusCode), latencyMS: lat}
	}
	var conns []activeConn
	if err := json.NewDecoder(resp.Body).Decode(&conns); err != nil {
		return probeResult{err: "bad stats payload: " + err.Error(), latencyMS: lat}
	}
	res := probeResult{ok: true, latencyMS: lat, conns: conns}
	if withOverview {
		if r2, err := p.basicGet(ctx, "https://"+addr+"/admin/api/stats/overview"); err == nil {
			var ov struct {
				TotalDevices int `json:"total_devices"`
			}
			_ = json.NewDecoder(r2.Body).Decode(&ov)
			r2.Body.Close()
			res.total = ov.TotalDevices
		}
	}
	return res
}

func (p *Prober) probeEgress(ctx context.Context, proxyURL string) (int64, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return 0, err
	}
	c := &http.Client{Timeout: probeTimeout, Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true}}
	start := p.now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/v1/models", nil)
	resp, err := c.Do(req)
	if err != nil {
		return p.now().Sub(start).Milliseconds(), err
	}
	resp.Body.Close()
	return p.now().Sub(start).Milliseconds(), nil // any HTTP answer = the tunnel works (401 expected)
}

// probeOnce runs every probe concurrently and assembles the snapshot.
func (p *Prober) probeOnce() *Snapshot {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout+2*time.Second)
	defer cancel()
	now := p.now()

	entries, egress, cfgLat, cfgErr := p.fetchEntries(ctx)

	results := map[string]probeResult{}
	var wg sync.WaitGroup
	var rmu sync.Mutex
	for _, e := range entries {
		wg.Add(1)
		go func(e Entry) {
			defer wg.Done()
			r := p.probeFn(ctx, e.Addr, e.Kind == "exit")
			rmu.Lock()
			results[e.ID] = r
			rmu.Unlock()
		}(e)
	}
	var dbErr error
	var dbLat int64
	var dnsIPs []string
	var dnsErr error
	var egressLat int64
	var egressErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		if p.src.DBPing != nil {
			s := p.now()
			dbErr = p.src.DBPing(ctx)
			dbLat = p.now().Sub(s).Milliseconds()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if p.src.DNSHost != "" {
			dnsIPs, dnsErr = net.DefaultResolver.LookupHost(ctx, p.src.DNSHost)
		}
	}()
	if egress != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			egressLat, egressErr = p.probeEgress(ctx, egress)
		}()
	}
	wg.Wait()

	local := -1
	if p.src.LocalDevices != nil {
		if n, err := p.src.LocalDevices(); err == nil {
			local = n
		}
	}

	return Build(BuildInput{
		Now: now, Entries: entries, Results: results,
		ConfigLatencyMS: cfgLat, ConfigErr: cfgErr,
		DBLatencyMS: dbLat, DBErr: dbErr, LocalDevices: local,
		DNSHost: p.src.DNSHost, DNSIPs: dnsIPs, DNSErr: dnsErr,
		Egress: egress, EgressLatencyMS: egressLat, EgressErr: egressErr,
	})
}

// ---------------------------------------------------------------- graph

// BuildInput is the pure input of Build — everything measured, nothing live,
// so the graph logic is unit-testable without a network.
type BuildInput struct {
	Now             time.Time
	Entries         []Entry
	Results         map[string]probeResult
	ConfigLatencyMS int64
	ConfigErr       error
	DBLatencyMS     int64
	DBErr           error
	LocalDevices    int // -1 = unknown
	DNSHost         string
	DNSIPs          []string
	DNSErr          error
	Egress          string
	EgressLatencyMS int64
	EgressErr       error
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func stateOf(ok bool) State {
	if ok {
		return OK
	}
	return Down
}

// Build turns measurements into nodes, edges and the checks table.
func Build(in BuildInput) *Snapshot {
	snap := &Snapshot{CheckedAt: in.Now, IntervalS: int(Interval / time.Second), Overall: Unknown}
	addCheck := func(c Check) {
		c.CheckedAt = in.Now
		snap.Checks = append(snap.Checks, c)
	}

	// Which bridges front which exit, and which remote IPs are bridges.
	bridgesOf := map[string][]Entry{}
	bridgeByHost := map[string]Entry{}
	var exits []Entry
	for _, e := range in.Entries {
		switch e.Kind {
		case "bridge":
			bridgesOf[e.Via] = append(bridgesOf[e.Via], e)
			bridgeByHost[hostOf(e.Addr)] = e
		default:
			exits = append(exits, e)
		}
	}

	// Clients node — devices summed over every exit's active connections.
	allDevices := map[int]bool{}
	nodes := []Node{{ID: "clients", Kind: "clients", Label: "Clients", State: OK}}
	var edges []Edge

	for _, ex := range exits {
		r := in.Results[ex.ID]
		direct := map[int]bool{}
		viaBridge := map[string]map[int]bool{}
		for _, c := range r.conns {
			allDevices[c.DeviceID] = true
			if b, isBridge := bridgeByHost[c.RemoteIP]; isBridge && b.Via == ex.ID {
				if viaBridge[b.ID] == nil {
					viaBridge[b.ID] = map[int]bool{}
				}
				viaBridge[b.ID][c.DeviceID] = true
			} else {
				direct[c.DeviceID] = true
			}
		}
		devices := map[int]bool{}
		for id := range direct {
			devices[id] = true
		}
		for _, m := range viaBridge {
			for id := range m {
				devices[id] = true
			}
		}
		nodes = append(nodes, Node{
			ID: ex.ID, Kind: "exit", Label: ex.Label, Addr: ex.Addr, Sub: hostOf(ex.Addr),
			State: stateOf(r.ok), LatencyMS: r.latencyMS, Devices: len(devices), TotalDevices: r.total, Error: r.err,
		})
		addCheck(Check{ID: "exit:" + ex.ID, Label: "Control plane → " + ex.Label + " (direct)", State: stateOf(r.ok),
			LatencyMS: r.latencyMS, Devices: len(direct), Detail: "TLS + /admin/api/stats/active on " + ex.Addr, Error: r.err})
		edges = append(edges, Edge{ID: "clients-" + ex.ID, From: "clients", To: ex.ID, State: stateOf(r.ok),
			Label: edgeLabel("direct", r.latencyMS, len(direct), r.ok), LatencyMS: r.latencyMS, Devices: len(direct), Error: r.err})
		edges = append(edges, Edge{ID: ex.ID + "-internet", From: ex.ID, To: "internet", State: stateOf(r.ok),
			Label: map[bool]string{true: "ok", false: "no answer"}[r.ok], Devices: len(devices), Error: r.err})

		for _, b := range bridgesOf[ex.ID] {
			br := in.Results[b.ID]
			n := len(viaBridge[b.ID])
			nodes = append(nodes, Node{ID: b.ID, Kind: "bridge", Label: b.Label, Addr: b.Addr, Via: ex.ID, Sub: hostOf(b.Addr),
				State: stateOf(br.ok), LatencyMS: br.latencyMS, Devices: n, Error: br.err})
			addCheck(Check{ID: "bridge:" + b.ID, Label: "via " + b.Label + " (" + b.Addr + ")", State: stateOf(br.ok),
				LatencyMS: br.latencyMS, Devices: n, Detail: "TCP through the bridge's DNAT to " + ex.Label + " (UDP not probed)", Error: br.err})
			edges = append(edges, Edge{ID: "clients-" + b.ID, From: "clients", To: b.ID, State: stateOf(br.ok),
				Label: devicesLabel(n), Devices: n, Error: br.err})
			edges = append(edges, Edge{ID: b.ID + "-" + ex.ID, From: b.ID, To: ex.ID, State: stateOf(br.ok),
				Label: edgeLabel("bridge", br.latencyMS, -1, br.ok), LatencyMS: br.latencyMS, Devices: n, Error: br.err})
		}

		// Replica freshness: the exit's total_devices vs the primary's.
		if r.ok {
			st, detail := OK, "in sync"
			if in.LocalDevices >= 0 && r.total != in.LocalDevices {
				st, detail = Down, fmt.Sprintf("%d devices here, %d on the exit", in.LocalDevices, r.total)
			} else if in.LocalDevices >= 0 {
				detail = fmt.Sprintf("%d devices on both", in.LocalDevices)
			}
			edges = append(edges, Edge{ID: "control-" + ex.ID, From: "control", To: ex.ID, State: st, Label: "DB replica · " + detail})
			addCheck(Check{ID: "replica:" + ex.ID, Label: "DB replica on " + ex.Label, State: st, Detail: detail})
		} else {
			edges = append(edges, Edge{ID: "control-" + ex.ID, From: "control", To: ex.ID, State: Unknown, Label: "DB replica · exit unreachable"})
		}
	}
	nodes[0].Devices = len(allDevices)
	nodes[0].Sub = devicesLabel(len(allDevices)) + " online"

	// Control plane and its parts.
	cfgState := stateOf(in.ConfigErr == nil)
	dbState := stateOf(in.DBErr == nil)
	dnsState := stateOf(in.DNSErr == nil && len(in.DNSIPs) > 0)
	dnsDetail := "A → " + strings.Join(in.DNSIPs, ", ")
	if in.DNSErr != nil {
		dnsDetail = ""
	}
	control := Node{ID: "control", Kind: "control", Label: "Control plane", Sub: in.DNSHost, State: OK}
	control.Checks = []Check{
		{ID: "config", Label: "Config service", State: cfgState, LatencyMS: in.ConfigLatencyMS, Detail: "GET /api/admin/services (local)", Error: errString(in.ConfigErr), CheckedAt: in.Now},
		{ID: "db", Label: "Postgres (primary)", State: dbState, LatencyMS: in.DBLatencyMS, Detail: "SELECT 1", Error: errString(in.DBErr), CheckedAt: in.Now},
		{ID: "dns", Label: "DNS " + in.DNSHost, State: dnsState, Detail: dnsDetail, Error: errString(in.DNSErr), CheckedAt: in.Now},
	}
	for _, c := range control.Checks {
		if c.State != OK {
			control.State = Down
		}
		snap.Checks = append(snap.Checks, c)
	}
	nodes = append(nodes, control, Node{ID: "internet", Kind: "internet", Label: "Internet", State: OK})
	edges = append(edges, Edge{ID: "clients-control", From: "clients", To: "control", State: cfgState, Label: "config poll", LatencyMS: in.ConfigLatencyMS, Error: errString(in.ConfigErr)})

	// Optional Taskless egress path.
	if in.Egress != "" {
		st := stateOf(in.EgressErr == nil)
		nodes = append(nodes,
			Node{ID: "taskless", Kind: "source", Label: "Taskless", Sub: "api", State: st},
			Node{ID: "egress", Kind: "egress", Label: "Egress proxy", Sub: in.Egress, Addr: in.Egress, State: st, LatencyMS: in.EgressLatencyMS, Error: errString(in.EgressErr)})
		edges = append(edges,
			Edge{ID: "taskless-egress", From: "taskless", To: "egress", State: st, Label: edgeLabel("egress", in.EgressLatencyMS, -1, in.EgressErr == nil), LatencyMS: in.EgressLatencyMS, Error: errString(in.EgressErr)},
			Edge{ID: "egress-internet", From: "egress", To: "internet", State: st, Label: "Anthropic · Telegram"})
		addCheck(Check{ID: "egress", Label: "Taskless egress proxy (" + in.Egress + ")", State: st, LatencyMS: in.EgressLatencyMS,
			Detail: "CONNECT api.anthropic.com:443 through the proxy", Error: errString(in.EgressErr)})
	}

	snap.Nodes, snap.Edges = nodes, edges
	sort.SliceStable(snap.Checks, func(i, j int) bool { return snap.Checks[i].ID < snap.Checks[j].ID })
	snap.Overall = OK
	if len(in.Entries) == 0 && in.ConfigErr == nil {
		snap.Overall = Unknown
	}
	for _, c := range snap.Checks {
		if c.State == Down {
			snap.Overall = Down
			break
		}
	}
	return snap
}

func devicesLabel(n int) string {
	switch n {
	case 1:
		return "1 device"
	default:
		return fmt.Sprintf("%d devices", n)
	}
}

func edgeLabel(kind string, latencyMS int64, devices int, ok bool) string {
	if !ok {
		return kind + " · no answer"
	}
	parts := []string{kind, fmt.Sprintf("%d ms", latencyMS)}
	if devices >= 0 {
		parts = append(parts, devicesLabel(devices))
	}
	return strings.Join(parts, " · ")
}
