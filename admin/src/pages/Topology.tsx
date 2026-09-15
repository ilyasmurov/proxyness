import { useEffect, useMemo, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, type TopologySnapshot, type TopologyNode, type TopologyEdge, type TopologyState } from "@/lib/api";

// PRXNS-23: live traffic map. The control plane probes every 15 s while
// this page keeps asking (see server/internal/topology); we only draw.

const POLL_MS = 15_000;
const W = 1040;

type Box = { x: number; y: number; w: number; h: number };

const css = `
.topo .node rect{fill:var(--card);stroke:var(--border);stroke-width:1.2;rx:10}
.topo .node.group rect{fill:var(--muted)}
.topo .node .title{font-size:13px;font-weight:600;fill:var(--foreground)}
.topo .node .sub{font-size:11px;fill:var(--muted-foreground)}
.topo .node .row{font-size:11.5px;fill:var(--foreground)}
.topo .edge path{fill:none;stroke-width:2;stroke:var(--topo-ok);stroke-opacity:.35}
.topo .edge.down path{stroke:var(--topo-bad);stroke-opacity:.9}
.topo .edge.unknown path{stroke:var(--topo-unk);stroke-dasharray:5 5;stroke-opacity:.8}
.topo .edge .lbl{font-size:11px;fill:var(--muted-foreground)}
.topo .edge.down .lbl{fill:var(--topo-bad);font-weight:600}
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

// Column layout: clients | bridges | exits | internet; control plane under
// the bridges column; egress path at the bottom. Rows stack when there are
// several bridges or exits.
function layout(nodes: TopologyNode[]) {
  const boxes = new Map<string, Box>();
  const exits = nodes.filter((n) => n.kind === "exit");
  const bridges = nodes.filter((n) => n.kind === "bridge");
  const egress = nodes.find((n) => n.kind === "egress");
  const rowGap = 110;
  const exitsTop = 110;
  const bridgesTop = 30;
  exits.forEach((n, i) => boxes.set(n.id, { x: 560, y: exitsTop + i * rowGap, w: 160, h: 80 }));
  bridges.forEach((n, i) => boxes.set(n.id, { x: 320, y: bridgesTop + i * 80, w: 160, h: 60 }));
  const exitsBottom = exitsTop + Math.max(1, exits.length) * rowGap - 30;
  const bridgesBottom = bridgesTop + Math.max(1, bridges.length) * 80;
  const controlY = Math.max(270, bridgesBottom + 90, exitsBottom - 20);
  boxes.set("control", { x: 320, y: controlY, w: 220, h: 120 });
  const midY = exitsTop + (Math.max(1, exits.length) * rowGap) / 2 - 15;
  boxes.set("clients", { x: 20, y: midY - 40, w: 152, h: 80 });
  boxes.set("internet", { x: 866, y: midY - 30, w: 150, h: 60 });
  let h = controlY + 120 + 30;
  if (egress) {
    const y = controlY + 100;
    boxes.set("taskless", { x: 20, y, w: 152, h: 60 });
    boxes.set("egress", { x: 560, y, w: 220, h: 60 });
    h = y + 60 + 30;
  }
  return { boxes, height: h };
}

// SVG text does not wrap or clip; keep every label inside its box by
// trimming to what fits with an ellipsis. px-per-char values come from
// measuring rendered Geist (getComputedTextLength, 2026-09-15): 11px sub
// ≈5.0–5.7, 11.5px rows ≈5.2–5.8, 13px/600 titles ≈6.2–7.3 — the
// constants sit at the upper end so a full-width string fits, never wraps.
function fit(text: string, boxW: number, pxPerChar: number, pad = 28): string {
  const max = Math.max(4, Math.floor((boxW - pad) / pxPerChar));
  return text.length <= max ? text : text.slice(0, max - 1) + "…";
}
const stripScheme = (u: string) => u.replace(/^https?:\/\//, "").replace(/\/$/, "");

function anchor(b: Box, side: "l" | "r" | "t" | "b") {
  switch (side) {
    case "l": return { x: b.x, y: b.y + b.h / 2 };
    case "r": return { x: b.x + b.w, y: b.y + b.h / 2 };
    case "t": return { x: b.x + b.w / 2, y: b.y };
    case "b": return { x: b.x + b.w / 2, y: b.y + b.h };
  }
}

function edgePath(from: Box, to: Box, fromNode: TopologyNode | undefined, toNode: TopologyNode | undefined) {
  // control → exit leaves from the top-right of the control box
  if (fromNode?.kind === "control") {
    const a = anchor(from, "r"); const b = anchor(to, "b");
    return `M${a.x} ${a.y} C ${a.x + 70} ${a.y}, ${b.x} ${b.y + 40}, ${b.x} ${b.y}`;
  }
  if (toNode?.kind === "control") {
    const a = anchor(from, "r"); const b = anchor(to, "l");
    return `M${a.x} ${a.y + 28} C ${a.x + 70} ${a.y + 60}, ${b.x - 60} ${b.y}, ${b.x} ${b.y}`;
  }
  const a = anchor(from, "r"); const b = anchor(to, "l");
  const dx = Math.max(40, (b.x - a.x) / 2);
  return `M${a.x} ${a.y} C ${a.x + dx} ${a.y}, ${b.x - dx} ${b.y}, ${b.x} ${b.y}`;
}

function midpoint(from: Box, to: Box, fromNode: TopologyNode | undefined) {
  const a = anchor(from, "r"); const b = anchor(to, "l");
  if (fromNode?.kind === "control") return { x: (a.x + to.x + to.w / 2) / 2 + 20, y: (a.y + to.y + to.h) / 2 - 6 };
  return { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 - 8 };
}

export function Topology() {
  const [snap, setSnap] = useState<TopologySnapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());

  useEffect(() => {
    let alive = true;
    const load = () =>
      api.topology()
        .then((s) => { if (alive) { setSnap(s); setError(null); } })
        .catch((e) => { if (alive) setError(e instanceof Error ? e.message : String(e)); });
    load();
    const t = setInterval(load, POLL_MS);
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => { alive = false; clearInterval(t); clearInterval(tick); };
  }, []);

  const { boxes, height } = useMemo(() => layout(snap?.nodes ?? []), [snap]);
  const nodeById = useMemo(() => new Map((snap?.nodes ?? []).map((n) => [n.id, n])), [snap]);

  if (!snap && !error) return <p className="text-muted-foreground">Loading...</p>;

  const overall = snap?.overall ?? "unknown";
  const overallText = overall === "ok" ? "All links up" : overall === "down" ? "Broken: " + (snap?.checks.filter((c) => c.state === "down").map((c) => c.label).join(", ") ?? "") : "No servers configured";

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
          <CardHeader><CardTitle className="text-base">How traffic flows</CardTitle></CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <svg viewBox={`0 0 ${W} ${height}`} style={{ width: "100%", minWidth: 720, height: "auto", display: "block" }} aria-label="Traffic map">
                <defs>
                  <marker id="topo-arr" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                    <path d="M0 0 L10 5 L0 10 z" fill="var(--muted-foreground)" />
                  </marker>
                </defs>
                {snap.edges.map((e: TopologyEdge) => {
                  const from = boxes.get(e.from); const to = boxes.get(e.to);
                  if (!from || !to) return null;
                  const d = edgePath(from, to, nodeById.get(e.from), nodeById.get(e.to));
                  const m = midpoint(from, to, nodeById.get(e.from));
                  const idle = e.state === "ok" && e.devices === 0 && (e.from === "clients");
                  return (
                    <g key={e.id} className={`edge ${e.state}${idle ? " idle" : ""}`}>
                      <path d={d} markerEnd="url(#topo-arr)" />
                      <path className="flow" d={d} />
                      <text className="lbl" x={m.x} y={m.y} textAnchor="middle">{e.state === "down" && e.error ? e.error.slice(0, 48) : e.label}</text>
                    </g>
                  );
                })}
                {snap.nodes.map((n: TopologyNode) => {
                  const b = boxes.get(n.id);
                  if (!b) return null;
                  const group = n.kind === "control";
                  return (
                    <g key={n.id} className={`node${group ? " group" : ""}`} transform={`translate(${b.x},${b.y})`}>
                      <rect width={b.w} height={b.h} />
                      <text className="title" x={14} y={26}>{fit(n.label, b.w, 7.4, 26)}</text>
                      {n.sub && (
                        <text className="sub" x={14} y={44}>
                          {fit((n.kind === "egress" ? stripScheme(n.sub) : n.sub) + (n.latency_ms ? ` · ${n.latency_ms} ms` : ""), b.w, 5.8, 22)}
                        </text>
                      )}
                      {n.kind === "exit" && <text className="row" x={14} y={64}>{n.devices} device{n.devices === 1 ? "" : "s"}</text>}
                      {n.kind === "clients" && <text className="row" x={14} y={64}>{n.devices} online</text>}
                      {n.kind !== "internet" && <circle className={`st ${n.state}`} cx={b.w - 14} cy={20} r={4.5} />}
                      {group && n.checks && (
                        <g transform="translate(14,54)">
                          {n.checks.map((c, i) => (
                            <g key={c.id} transform={`translate(0,${i * 20})`}>
                              <circle className={`st ${c.state}`} cx={4} cy={-3} r={3.5} />
                              <text className="row" x={14} y={0}>
                                {fit(c.id === "dns" ? (c.detail ? c.detail.replace("A → ", "DNS → ") : "DNS") : c.label + (c.latency_ms ? ` · ${c.latency_ms} ms` : ""), b.w, 6.0, 42)}
                              </text>
                            </g>
                          ))}
                        </g>
                      )}
                    </g>
                  );
                })}
              </svg>
            </div>
            <div className="flex gap-4 flex-wrap text-xs text-muted-foreground mt-3">
              <span><i className="inline-block w-5 border-t-2 border-emerald-500 align-middle mr-1" /> link up; moving dashes = traffic flowing</span>
              <span><i className="inline-block w-5 border-t-2 border-red-500 align-middle mr-1" /> probe failed</span>
              <span><i className="inline-block w-5 border-t-2 border-dashed border-muted-foreground align-middle mr-1" /> not probed</span>
              <span>· numbers on edges: latency from the control plane and devices on that path</span>
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
                  <TableHead>Link</TableHead><TableHead>State</TableHead><TableHead>Latency</TableHead>
                  <TableHead>Devices</TableHead><TableHead>Checked</TableHead><TableHead>Details</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {snap.checks.map((c) => (
                  <TableRow key={c.id}>
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
            </p>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
