# Admin "Topology" page — live traffic map (2026-09-15, PRXNS-23)

Mockup approved by Ilya (docs/topology-preview.html, not tracked).

## What it shows

A diagram of how traffic flows, drawn from the config service's `servers` list
(so a new exit or bridge appears by itself), with every edge coloured by a live
probe and labelled with latency and how many devices are on that path:

`Clients` → `bridge(s)` → `exit(s)` → `Internet`; a `Control plane` box (config
service, admin API + Postgres, DNS) with edges "config poll" (clients → control)
and "DB replica" (control → each exit); an optional `Taskless → egress proxy on
the exit → Internet` side path when `service_config.egress_proxy` is set.

Below the diagram: a table of checks (link, state, latency, devices, checked
at, details/error). Header pill: overall state, "checked N s ago".

## Who probes: the control plane's server process, lazily

The browser cannot probe UDP or other hosts, so `GET /admin/api/topology`
(Basic Auth) on the proxy binary returns a cached snapshot. The prober
(`server/internal/topology`) starts on the first request and runs every 15 s
while requests keep coming; it stops after 2 min without any. Exits run the
same binary but nobody asks them, so they never probe.

Server entries gain `kind: exit|bridge` and `via: <exit id>` (config service
validates: a bridge must point at an exit in the same list). The prober reads
the list from the config service (`GET /api/admin/services`, same admin creds).

Probes, all with 5 s budgets, run concurrently:

- **exit**: TLS to `addr` + `GET /admin/api/stats/active` with admin creds →
  reachable, latency, active connections, and the split of those connections
  by remote IP: ones coming from a bridge's host count for that bridge's edge,
  the rest for the direct edge. Needs `ConnInfo.RemoteIP` (new field in the
  tracker, filled from the relay's peer address). Also `total_devices` for
  the replica check.
- **bridge**: same request to the bridge's `addr` (DNAT to the exit) →
  reachable + latency; proves forwarding. UDP is not probed (no unauthenticated
  handshake); the edge says "TCP via bridge".
- **config service**: `GET <configAddr>/api/admin/services` locally → ok +
  latency.
- **DB**: `SELECT 1` on the server's pool → ok + latency; replica freshness =
  compare local device count with each exit's `total_devices` (equal → "in
  sync", else "N vs M").
- **DNS**: resolve `proxyness.smurov.com` (the config poll host); report the
  IPs; ok when it resolves.
- **egress proxy** (optional): `GET https://api.anthropic.com/v1/models`
  through `egress_proxy` → any HTTP status = ok (401 expected).

Edge states: `ok` (green, animated dashes = traffic flowing when devices > 0),
`down` (red, error text), `unknown` (grey, not probed yet). A path's device
count comes from the exit's active connections.

## Admin page

`/topology` in the SPA: polls `/admin/api/topology` every 15 s (plain fetch,
no SSE — the snapshot is small and already cached server-side). SVG layout by
role in columns: clients | bridges | exits | internet, control-plane box below
the bridges column, egress side path at the bottom; stacks vertically when
there are several bridges or exits. Legend + checks table as in the mockup.

## Out of scope

Probing UDP through bridges; historical uptime bars; AmneziaWG on the map
(no unauthenticated probe exists, a grey box says nothing).
