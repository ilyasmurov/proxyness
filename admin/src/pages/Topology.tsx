import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, type TopologySnapshot, type TopologyNode, type TopologyCheck, type TopologyState } from "@/lib/api";

// PRXNS-23: live traffic map, grouped by physical machine. The control
// plane probes every 15 s while this page keeps asking
// (server/internal/topology); this file only derives the drawing:
//   - a node's machine is the host part of its address; the control plane's
//     machine is the IP its DNS name resolves to (the `dns` check);
//   - the replica card is the `replica:<exit>` check drawn inside the exit's
//     machine;
//   - machine names and informational rows without a probe (AmneziaWG…)
//     come from the config service's `hosts` map, edited on the Servers page.
// Edge labels live in a top layer on card-coloured pills, so cards never
// cover them; a failed link shows a short reason here and the full error in
// the checks table.

const POLL_MS = 15_000;
const W = 1120;
const TOP = 20;
const CARD_W = 260;
const MACHINE_W = 300;
const MACHINE_PAD = 20;
const MACHINE_HEAD = 50;
const LEFT_X = 250;
const RIGHT_X = 620;
const GAP = 30;

type Box = { x: number; y: number; w: number; h: number };
type HostInfo = { label: string; extras?: string[] };
type CardKind = "exit" | "bridge" | "control" | "replica" | "extra" | "egress";

interface CardModel {
  id: string; // box id: node id, "replica:<exit>", "extra:<host>:<i>"
  kind: CardKind;
  host: string;
  title: string;
  sub: string;
  row?: string;
  state: TopologyState;
  checks?: TopologyCheck[];
  h: number;
}

interface MachineModel {
  host: string;
  label: string;
  sub: string;
  side: "left" | "right";
  cards: CardModel[];
  state: TopologyState;
}

const css = `
.topo .host rect{fill:var(--muted);fill-opacity:.45;stroke:var(--border);stroke-width:1.5;stroke-dasharray:6 4;rx:14}
.topo .host .htitle{font-size:14px;font-weight:700;fill:var(--foreground)}
.topo .host .hsub{font-size:11px;fill:var(--muted-foreground)}
.topo .node rect{fill:var(--card);stroke:var(--border);stroke-width:1.2;rx:10}
.topo .node.group rect{fill:var(--muted)}
.topo .node .title{font-size:13px;font-weight:600;fill:var(--foreground)}
.topo .node .sub{font-size:11px;fill:var(--muted-foreground)}
.topo .node .row{font-size:11.5px;fill:var(--foreground)}
.topo .edge path{fill:none;stroke-width:2;stroke:var(--topo-ok);stroke-opacity:.35}
.topo .edge.down path{stroke:var(--topo-bad);stroke-opacity:.9}
.topo .edge.unknown path{stroke:var(--topo-unk);stroke-dasharray:5 5;stroke-opacity:.8}
.topo .lbl{font-size:11px;fill:var(--muted-foreground)}
.topo .lblbg{fill:var(--card);stroke:var(--border);stroke-width:1;rx:6;opacity:.96}
.topo .labels .down .lbl{fill:var(--topo-bad);font-weight:600}
/* base line stays faint; bright round dots run along a live link so traffic
   is visibly moving. Dots stop on a failed, unprobed or idle link. */
.topo .edge .flow{stroke:var(--topo-ok);stroke-opacity:1;stroke-width:3.5;stroke-linecap:round;stroke-dasharray:0.1 11;animation:topoflow .8s linear infinite}
.topo .edge.down .flow,.topo .edge.unknown .flow,.topo .edge.idle .flow{display:none}
@keyframes topoflow{to{stroke-dashoffset:-11.1}}
.topo .st{fill:var(--topo-ok)} .topo .st.down{fill:var(--topo-bad)} .topo .st.unknown{fill:var(--topo-unk)}
.topo{--topo-ok:oklch(0.72 0.19 150);--topo-bad:oklch(0.63 0.24 27);--topo-unk:oklch(0.7 0 0)}
`;

function stateBadge(s: TopologyState) {
  const cls =
    s === "ok" ? "bg-emerald-500/15 text-emerald-500" : s === "down" ? "bg-red-500/15 text-red-500" : "bg-muted text-muted-foreground";
  return <span className={`inline-block rounded-md px-2 py-0.5 text-[11.5px] font-semibold ${cls}`}>{s}</span>;
}

function ago(iso: string, now: number) {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  return `${s} s ago`;
}

// SVG text does not wrap or clip; keep every label inside its box by
// trimming to what fits with an ellipsis. px-per-char values come from
// measuring rendered Geist (getComputedTextLength, 2026-09-15): 11px sub
// ≈5.0–5.7, 11.5px rows ≈5.2–5.8, 13px/600 titles ≈6.2–7.3, 14px/700
// machine titles ≈7.8 — the constants sit at the upper end so a full-width
// string fits, never wraps.
function fit(text: string, boxW: number, pxPerChar: number, pad = 28): string {
  const max = Math.max(4, Math.floor((boxW - pad) / pxPerChar));
  return text.length <= max ? text : text.slice(0, max - 1) + "…";
}
const stripScheme = (u: string) => u.replace(/^https?:\/\//, "").replace(/\/$/, "");
function hostOf(addr?: string): string {
  if (!addr) return "";
  const s = stripScheme(addr);
  const i = s.lastIndexOf(":");
  return i > 0 ? s.slice(0, i) : s;
}
function portOf(addr?: string): string {
  if (!addr) return "";
  const s = stripScheme(addr);
  const i = s.lastIndexOf(":");
  return i > 0 ? s.slice(i + 1) : "";
}
const latency = (ms?: number) => (ms ? ` · ${ms} ms` : "");
function plural(n: number, one: string, many: string) {
  return `${n} ${n === 1 ? one : many}`;
}

// The diagram shows why a link is red in a few words; the checks table has
// the full error text.
function shortError(err?: string): string {
  if (!err) return "down";
  const e = err.toLowerCase();
  if (e.includes("timeout") || e.includes("deadline")) return "no answer";
  if (e.includes("refused")) return "connection refused";
  if (e.includes("unreachable") || e.includes("no route")) return "unreachable";
  if (e.includes("401") || e.includes("403")) return "auth rejected";
  if (e.includes("tls") || e.includes("certificate")) return "TLS failed";
  return err.length > 26 ? err.slice(0, 25) + "…" : err;
}

function worst(states: TopologyState[]): TopologyState {
  if (states.includes("down")) return "down";
  if (states.includes("ok")) return "ok";
  return "unknown";
}

// Group the snapshot's nodes into machines. Extras from the hosts map are
// drawn as grey "not probed" cards on the machine they belong to.
function buildMachines(snap: TopologySnapshot, hosts: Record<string, HostInfo>): MachineModel[] {
  const byId = new Map(snap.nodes.map((n) => [n.id, n]));
  const checkById = new Map(snap.checks.map((c) => [c.id, c]));
  const cards: CardModel[] = [];

  const bridges = snap.nodes.filter((n) => n.kind === "bridge");
  const exits = snap.nodes.filter((n) => n.kind === "exit");
  const control = snap.nodes.find((n) => n.kind === "control");
  const egress = snap.nodes.find((n) => n.kind === "egress");

  const dns = checkById.get("dns");
  const dnsIP = dns?.detail?.match(/A → ([^,\s]+)/)?.[1];
  const bridgeHosts = Array.from(new Set(bridges.map((b) => hostOf(b.addr))));
  const controlHost = dnsIP || (bridgeHosts.length === 1 ? bridgeHosts[0] : "control plane");

  for (const b of bridges) {
    const exit = b.via ? byId.get(b.via) : undefined;
    cards.push({
      id: b.id, kind: "bridge", host: hostOf(b.addr), state: b.state, h: 60,
      title: `Bridge · ${b.label}`,
      sub: b.state === "down" ? shortError(b.error) : `${portOf(b.addr)}/tcp → ${exit?.label ?? b.via ?? "exit"}${latency(b.latency_ms)}`,
    });
  }
  if (control) {
    const checks = control.checks ?? [];
    cards.push({
      id: control.id, kind: "control", host: controlHost, state: control.state, checks, h: 60 + checks.length * 20 + 6,
      title: "Control plane", sub: `${control.sub ?? ""} · admin`,
    });
  }
  for (const ex of exits) {
    cards.push({
      id: ex.id, kind: "exit", host: hostOf(ex.addr), state: ex.state, h: 80,
      title: `Exit · ${ex.label}`,
      sub: ex.state === "down" ? shortError(ex.error) : `${portOf(ex.addr)}/tcp · 8443/udp${latency(ex.latency_ms)}`,
      row: `${plural(ex.devices, "device", "devices")} online${ex.total_devices ? ` · ${ex.total_devices} registered` : ""}`,
    });
    const rc = checkById.get(`replica:${ex.id}`);
    cards.push({
      id: `replica:${ex.id}`, kind: "replica", host: hostOf(ex.addr), h: 60,
      state: rc ? rc.state : "unknown",
      title: "Postgres · replica",
      sub: rc ? rc.detail || rc.error || "" : ex.state === "down" ? "exit unreachable" : "not checked yet",
    });
  }
  if (egress) {
    cards.push({
      id: egress.id, kind: "egress", host: hostOf(egress.addr), state: egress.state, h: 60,
      title: "Egress proxy",
      sub: egress.state === "down" ? shortError(egress.error) : `${portOf(egress.addr) || stripScheme(egress.addr ?? "")} · Taskless only${latency(egress.latency_ms)}`,
    });
  }

  const known = new Set(cards.map((c) => c.host));
  for (const [host, info] of Object.entries(hosts)) {
    if (!known.has(host)) continue;
    (info.extras ?? []).forEach((text, i) => {
      const sep = text.indexOf(" · ");
      const title = sep > 0 ? text.slice(0, sep) : text;
      const rest = sep > 0 ? text.slice(sep + 3) : "";
      cards.push({ id: `extra:${host}:${i}`, kind: "extra", host, state: "unknown", h: 60, title, sub: rest ? `${rest} · not probed` : "not probed" });
    });
  }

  const order: Record<CardKind, number> = { bridge: 0, exit: 0, replica: 1, control: 2, extra: 3, egress: 4 };
  const machines = new Map<string, MachineModel>();
  for (const c of cards) {
    let m = machines.get(c.host);
    if (!m) {
      m = { host: c.host, label: hosts[c.host]?.label ?? c.host, sub: "", side: "right", cards: [], state: "unknown" };
      machines.set(c.host, m);
    }
    m.cards.push(c);
  }
  const roleName: Record<CardKind, string> = { bridge: "bridge", control: "control plane", exit: "exit", egress: "egress proxy", replica: "", extra: "" };
  const list = Array.from(machines.values());
  for (const m of list) {
    m.cards.sort((a, b) => order[a.kind] - order[b.kind]);
    const hasExit = m.cards.some((c) => c.kind === "exit");
    m.side = hasExit || !m.cards.some((c) => c.kind === "bridge" || c.kind === "control") ? "right" : "left";
    const roles = Array.from(new Set(m.cards.map((c) => roleName[c.kind]).filter(Boolean)));
    m.sub = (m.label !== m.host ? `${m.host} · ` : "") + roles.join(" + ");
    // extras are informational and never degrade a machine
    m.state = worst(m.cards.filter((c) => c.kind !== "extra").map((c) => c.state));
  }
  // left column first (bridge/control machines), exits keep snapshot order
  const idx = (m: MachineModel) => Math.min(...m.cards.map((c) => snap.nodes.findIndex((n) => n.id === c.id)).filter((i) => i >= 0), 1e9);
  list.sort((a, b) => (a.side === b.side ? idx(a) - idx(b) : a.side === "left" ? -1 : 1));
  return list;
}

function layout(machines: MachineModel[], hasEgress: boolean) {
  const boxes = new Map<string, Box>();
  const machineBoxes = new Map<string, Box>();
  const cols: Record<"left" | "right", MachineModel[]> = { left: [], right: [] };
  for (const m of machines) cols[m.side].push(m);

  // exits go first in their column; the control card waits until it sits
  // below the first exit so the direct clients → exit line crosses the left
  // machine between the bridge and the control plane, not over a card.
  let firstExitBottom = 0;
  let firstExitCenter = 0;
  let bridgeBottom = 0;
  const place = (col: MachineModel[], x: number, deferEgress: boolean) => {
    let y = TOP;
    const bottoms: number[] = [];
    for (const m of col) {
      let cy = y + MACHINE_HEAD + MACHINE_PAD;
      for (const c of m.cards) {
        if (deferEgress && c.kind === "egress") continue;
        if (c.kind === "control" && firstExitBottom) cy = Math.max(cy, firstExitBottom + 60);
        const b = { x: x + MACHINE_PAD, y: cy, w: CARD_W, h: c.h };
        boxes.set(c.id, b);
        if (c.kind === "exit" && !firstExitBottom) { firstExitBottom = b.y + b.h; firstExitCenter = b.y + b.h / 2; }
        if (c.kind === "bridge") bridgeBottom = Math.max(bridgeBottom, b.y + b.h);
        cy += c.h + GAP;
      }
      const h = Math.max(cy - GAP + MACHINE_PAD, MACHINE_HEAD + 2 * MACHINE_PAD) - y;
      machineBoxes.set(m.host, { x, y, w: MACHINE_W, h });
      bottoms.push(y + h);
      y += h + GAP;
    }
    return bottoms;
  };
  const egressMachine = machines.find((m) => m.cards.some((c) => c.kind === "egress"));
  const rightBottoms = place(cols.right, RIGHT_X, true);
  const leftBottoms = place(cols.left, LEFT_X, true);
  let bottom = Math.max(...rightBottoms, ...leftBottoms, TOP + MACHINE_HEAD + 2 * MACHINE_PAD);

  // egress row sits under everything else so its line crosses no card, and
  // its machine's frame stretches down to include it
  if (hasEgress && egressMachine) {
    const eg = egressMachine.cards.find((c) => c.kind === "egress")!;
    const mb = machineBoxes.get(egressMachine.host)!;
    const last = cols[egressMachine.side][cols[egressMachine.side].length - 1] === egressMachine;
    const y = last ? bottom - MACHINE_PAD + 40 : mb.y + mb.h - MACHINE_PAD + GAP;
    boxes.set(eg.id, { x: mb.x + MACHINE_PAD, y, w: CARD_W, h: eg.h });
    if (last) {
      mb.h = y + eg.h + MACHINE_PAD - mb.y;
      bottom = mb.y + mb.h;
    }
    boxes.set("taskless", { x: 20, y, w: 152, h: 60 });
  }
  // both columns end level
  for (const side of ["left", "right"] as const) {
    const col = cols[side];
    if (!col.length) continue;
    const mb = machineBoxes.get(col[col.length - 1].host)!;
    mb.h = bottom - mb.y;
  }

  const controlBox = Array.from(boxes.entries()).find(([id]) => id === "control")?.[1];
  const clientsCenter = bridgeBottom && controlBox
    ? (bridgeBottom + controlBox.y) / 2
    : firstExitCenter || (controlBox ? controlBox.y + controlBox.h / 2 : TOP + 80);
  boxes.set("clients", { x: 20, y: Math.max(TOP, clientsCenter - 40), w: 152, h: 80 });
  boxes.set("internet", { x: 1010, y: Math.max(TOP, (firstExitCenter || clientsCenter) - 30), w: 100, h: 60 });

  return { boxes, machineBoxes, height: bottom + 20 };
}

function endPoint(from: string, toBox: Box, toKind?: string) {
  // bridge and direct lines enter the exit card at different heights so they
  // don't merge into one arrowhead
  if (toKind === "exit" && from === "clients") return { x: toBox.x, y: toBox.y + toBox.h * 0.74 };
  if (toKind === "exit") return { x: toBox.x, y: toBox.y + toBox.h * 0.3 };
  return { x: toBox.x, y: toBox.y + toBox.h / 2 };
}

type Pt = { x: number; y: number };
function cubic(a: Pt, b: Pt) {
  const dx = Math.max(40, (b.x - a.x) / 2);
  const c1 = { x: a.x + dx, y: a.y };
  const c2 = { x: b.x - dx, y: b.y };
  const at = (t: number): Pt => {
    const u = 1 - t;
    return {
      x: u * u * u * a.x + 3 * u * u * t * c1.x + 3 * u * t * t * c2.x + t * t * t * b.x,
      y: u * u * u * a.y + 3 * u * u * t * c1.y + 3 * u * t * t * c2.y + t * t * t * b.y,
    };
  };
  return { d: `M${a.x} ${a.y} C ${c1.x} ${c1.y}, ${c2.x} ${c2.y}, ${b.x} ${b.y}`, at };
}

// The diagram keeps edge text short; the card at the far end and the checks
// table carry the detail.
function edgeText(e: { from: string; state: TopologyState; label: string; error?: string }): string {
  if (e.state === "down") return e.from === "control" ? "out of sync" : shortError(e.error);
  if (e.from === "control") return e.state === "ok" ? "DB replica" : "replica · not checked";
  return e.label;
}

export function Topology() {
  const [snap, setSnap] = useState<TopologySnapshot | null>(null);
  const [hosts, setHosts] = useState<Record<string, HostInfo>>({});
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const [fontsReady, setFontsReady] = useState(false);
  const [labelBoxes, setLabelBoxes] = useState<Record<string, Box>>({});
  const svgRef = useRef<SVGSVGElement>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      api.topology()
        .then((s) => { if (alive) { setSnap(s); setError(null); } })
        .catch((e) => { if (alive) setError(e instanceof Error ? e.message : String(e)); });
    load();
    api.getServices()
      .then((cfg) => {
        const raw = (cfg as Record<string, string | undefined>).hosts;
        if (!alive || !raw) return;
        try {
          const m = JSON.parse(raw);
          if (m && typeof m === "object" && !Array.isArray(m)) setHosts(m as Record<string, HostInfo>);
        } catch { /* unlabelled machines are fine */ }
      })
      .catch(() => { /* labels are cosmetic */ });
    document.fonts?.ready.then(() => { if (alive) setFontsReady(true); });
    const t = setInterval(load, POLL_MS);
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => { alive = false; clearInterval(t); clearInterval(tick); };
  }, []);

  const machines = useMemo(() => (snap ? buildMachines(snap, hosts) : []), [snap, hosts]);
  const hasEgress = !!snap?.nodes.some((n) => n.kind === "egress");
  const { boxes, machineBoxes, height } = useMemo(() => layout(machines, hasEgress), [machines, hasEgress]);
  const cardById = useMemo(() => new Map(machines.flatMap((m) => m.cards).map((c) => [c.id, c])), [machines]);
  const machineOfHost = useMemo(() => new Map(machines.map((m) => [m.host, m])), [machines]);

  // Edges: control → exit is drawn into the exit's replica card; lines
  // leaving the clients box fan out by target height.
  const edges = useMemo(() => {
    if (!snap) return [];
    const mapped = snap.edges
      .map((e) => ({ e, from: e.from, to: e.from === "control" && e.to !== "internet" ? `replica:${e.to}` : e.to }))
      .filter(({ from, to }) => boxes.has(from) && boxes.has(to));
    const fromClients = mapped.filter((m) => m.from === "clients").sort((a, b) => {
      const ba = boxes.get(a.to)!; const bb = boxes.get(b.to)!;
      return ba.y + ba.h / 2 - (bb.y + bb.h / 2);
    });
    return mapped.map(({ e, from, to }) => {
      const fb = boxes.get(from)!; const tb = boxes.get(to)!;
      let a = { x: fb.x + fb.w, y: fb.y + fb.h / 2 };
      if (from === "clients") {
        const i = fromClients.findIndex((m) => m.e.id === e.id);
        a = { x: fb.x + fb.w, y: fb.y + (fb.h * (i + 1)) / (fromClients.length + 1) };
      }
      const toKind = cardById.get(to)?.kind;
      const b = endPoint(from, tb, toKind);
      const idle = e.state === "ok" && e.devices === 0 && from === "clients";
      const { d, at } = cubic(a, b);
      // the direct clients → exit line runs just under the bridge card, so
      // its label hangs below the line; the long egress → internet curve is
      // labelled near its top, clear of the egress card
      const direct = from === "clients" && toKind === "exit";
      const p = at(to === "internet" && toKind !== "exit" && from !== "clients" ? 0.7 : 0.5);
      return { e, d, mid: { x: p.x, y: p.y + (direct ? 18 : -12) }, idle, text: edgeText(e) };
    });
  }, [snap, boxes, cardById]);

  // measure edge labels once drawn, so their pills fit the text exactly
  useLayoutEffect(() => {
    const svg = svgRef.current;
    if (!svg) return;
    const next: Record<string, Box> = {};
    svg.querySelectorAll<SVGTextElement>("text.lbl[data-id]").forEach((t) => {
      const bb = t.getBBox();
      next[t.dataset.id!] = { x: bb.x - 5, y: bb.y - 2, w: bb.width + 10, h: bb.height + 4 };
    });
    setLabelBoxes((prev) => (JSON.stringify(prev) === JSON.stringify(next) ? prev : next));
  }, [edges, fontsReady]);

  if (!snap && !error) return <p className="text-muted-foreground">Loading...</p>;

  const overall = snap?.overall ?? "unknown";
  const overallText =
    overall === "ok" ? "All links up"
      : overall === "down" ? "Broken: " + (snap?.checks.filter((c) => c.state === "down").map((c) => c.label).join(", ") ?? "")
        : "No servers configured";

  const machineLabelOf = (host: string) => machineOfHost.get(host)?.label ?? host;
  const machineOfCheck = (c: TopologyCheck): string => {
    const [kind, id] = c.id.split(":");
    if (kind === "exit" || kind === "replica" || kind === "bridge") return machineLabelOf(cardById.get(id)?.host ?? "");
    if (kind === "egress") return machineLabelOf(cardById.get("egress")?.host ?? "");
    return machineLabelOf(cardById.get("control")?.host ?? "");
  };

  return (
    <div className="space-y-6 topo">
      <style>{css}</style>
      <div className="flex items-center justify-between gap-4 flex-wrap">
        <h1 className="text-2xl font-bold">Topology</h1>
        <div className="flex items-center gap-3 text-sm text-muted-foreground flex-wrap">
          <span className="inline-flex items-center gap-2 border border-border rounded-full px-3 py-1 text-xs">
            <svg width="10" height="10"><circle className={`st ${overall}`} cx="5" cy="5" r="4" /></svg>
            {overallText}
          </span>
          {snap && <span>checked {ago(snap.checked_at, now)} · every {snap.interval_s} s</span>}
        </div>
      </div>

      {error && <div className="text-red-400 text-sm bg-red-500/10 border border-red-500/20 rounded-md px-3 py-2">{error}</div>}

      {snap && (
        <Card>
          <CardHeader><CardTitle className="text-base">How traffic flows · by machine</CardTitle></CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <svg ref={svgRef} viewBox={`0 0 ${W} ${height}`} style={{ width: "100%", minWidth: 760, height: "auto", display: "block" }} aria-label="Traffic map by machine">
                <defs>
                  <marker id="topo-arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                    <path d="M0 0 L10 5 L0 10 z" fill="var(--muted-foreground)" />
                  </marker>
                </defs>

                {machines.map((m) => {
                  const b = machineBoxes.get(m.host);
                  if (!b) return null;
                  return (
                    <g key={m.host} className="host" transform={`translate(${b.x},${b.y})`}>
                      <rect width={b.w} height={b.h} />
                      <text className="htitle" x={16} y={28}>{fit(m.label, b.w, 7.8, 44)}</text>
                      <text className="hsub" x={16} y={46}>{fit(m.sub, b.w, 5.8, 32)}</text>
                      <circle className={`st ${m.state}`} cx={b.w - 18} cy={22} r={5} />
                    </g>
                  );
                })}

                {edges.map(({ e, d, idle }) => (
                  <g key={e.id} className={`edge ${e.state}${idle ? " idle" : ""}`}>
                    <path d={d} markerEnd="url(#topo-arr)" />
                    <path className="flow" d={d} />
                  </g>
                ))}

                {(["clients", "taskless", "internet"] as const).map((id) => {
                  const b = boxes.get(id);
                  const n: TopologyNode | undefined = snap.nodes.find((x) => x.id === id);
                  if (!b || !n) return null;
                  return (
                    <g key={id} className="node" transform={`translate(${b.x},${b.y})`}>
                      <rect width={b.w} height={b.h} />
                      <text className="title" x={14} y={26}>{n.label}</text>
                      {id === "clients" && <text className="sub" x={14} y={44}>Proxyness</text>}
                      {id === "clients" && <text className="row" x={14} y={64}>{n.devices} online</text>}
                      {id === "taskless" && <text className="sub" x={14} y={44}>{n.sub}</text>}
                      {id !== "internet" && <circle className={`st ${n.state}`} cx={b.w - 14} cy={20} r={4.5} />}
                    </g>
                  );
                })}

                {machines.flatMap((m) => m.cards).map((c) => {
                  const b = boxes.get(c.id);
                  if (!b) return null;
                  const group = c.kind === "control";
                  return (
                    <g key={c.id} className={`node${group ? " group" : ""}`} transform={`translate(${b.x},${b.y})`}>
                      <rect width={b.w} height={b.h} />
                      <text className="title" x={14} y={group ? 24 : 26}>{fit(c.title, b.w, 7.4, 26)}</text>
                      <text className="sub" x={14} y={group ? 40 : 44}>{fit(c.sub, b.w, 5.8, 22)}</text>
                      {c.row && <text className="row" x={14} y={64}>{fit(c.row, b.w, 6.0, 22)}</text>}
                      <circle className={`st ${c.state}`} cx={b.w - 14} cy={group ? 18 : 20} r={4.5} />
                      {group && c.checks && (
                        <g transform="translate(14,60)">
                          {c.checks.map((k, i) => (
                            <g key={k.id} transform={`translate(0,${i * 20})`}>
                              <circle className={`st ${k.state}`} cx={4} cy={-3} r={3.5} />
                              <text className="row" x={14} y={0}>
                                {fit(k.id === "dns" ? (k.detail ? k.detail.replace("A → ", "DNS → ") : "DNS") : k.label + latency(k.latency_ms), b.w, 6.0, 42)}
                              </text>
                            </g>
                          ))}
                        </g>
                      )}
                    </g>
                  );
                })}

                <g className="labels">
                  {edges.filter((x) => x.text).map(({ e, mid, text }) => {
                    const lb = labelBoxes[e.id];
                    return (
                      <g key={e.id} className={e.state}>
                        {lb && <rect className="lblbg" x={lb.x} y={lb.y} width={lb.w} height={lb.h} />}
                        <text className="lbl" data-id={e.id} x={mid.x} y={mid.y} textAnchor="middle">{text}</text>
                      </g>
                    );
                  })}
                </g>
              </svg>
            </div>
            <div className="flex gap-4 flex-wrap text-xs text-muted-foreground mt-3">
              <span><i className="inline-block w-5 border-t-2 border-emerald-500 align-middle mr-1" /> link up; moving dots = traffic flowing</span>
              <span><i className="inline-block w-5 border-t-2 border-red-500 align-middle mr-1" /> probe failed</span>
              <span><i className="inline-block w-5 border-t-2 border-dashed border-muted-foreground align-middle mr-1" /> not probed</span>
              <span>· dashed frame = one physical machine, its dot = the worst component inside</span>
            </div>
          </CardContent>
        </Card>
      )}

      {snap && (
        <Card>
          <CardHeader><CardTitle className="text-base">Checks</CardTitle></CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Machine</TableHead><TableHead>Link</TableHead><TableHead>State</TableHead><TableHead>Latency</TableHead>
                  <TableHead>Devices</TableHead><TableHead>Checked</TableHead><TableHead>Details</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {snap.checks.map((c) => (
                  <TableRow key={c.id}>
                    <TableCell className="text-muted-foreground whitespace-nowrap">{machineOfCheck(c)}</TableCell>
                    <TableCell>{c.label}</TableCell>
                    <TableCell>{stateBadge(c.state)}</TableCell>
                    <TableCell>{c.latency_ms ? `${c.latency_ms} ms` : "—"}</TableCell>
                    <TableCell>{c.id.startsWith("exit:") || c.id.startsWith("bridge:") ? c.devices : "—"}</TableCell>
                    <TableCell className="text-muted-foreground">{ago(c.checked_at, now)}</TableCell>
                    <TableCell className="font-mono text-xs">{c.state === "down" && c.error ? c.error : c.detail}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            <p className="text-xs text-muted-foreground mt-3">
              The control plane runs these probes while this page is open. Bridges are checked over TCP through their forwarding; UDP is not probed.
              Machine names and rows without a probe are set on the Servers page.
            </p>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
