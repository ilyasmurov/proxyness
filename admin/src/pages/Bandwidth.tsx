import { useEffect, useMemo, useState } from "react";
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api } from "@/lib/api";
import type { BandwidthInterface, BandwidthSnapshot } from "@/lib/api";
import { formatBytes } from "../lib/format";

// What the Serverspace plan gives us. Raise this when the plan changes,
// otherwise the "% of plan" readings quietly lie.
const PLAN_LIMIT_MBPS = 50;

const DOWNLOAD_COLOR = "#16a34a";
const UPLOAD_COLOR = "#2563eb";

const LABELS: Record<string, string> = {
  enp0s5: "Uplink (whole channel)",
  awg0: "AmneziaWG",
};

function label(name: string) {
  return LABELS[name] ?? name;
}

function mbps(value: number) {
  if (value >= 100) return `${Math.round(value)} Mbit/s`;
  if (value >= 10) return `${value.toFixed(1)} Mbit/s`;
  return `${value.toFixed(2)} Mbit/s`;
}

function clockLabel(t: number) {
  const d = new Date(t * 1000);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

function dayLabel(t: number) {
  const d = new Date(t * 1000);
  return `${d.getDate()}.${String(d.getMonth() + 1).padStart(2, "0")}`;
}

function percentOfPlan(value: number) {
  return Math.round((value / PLAN_LIMIT_MBPS) * 100);
}

function Metric({ title, children, hint }: { title: string; children: React.ReactNode; hint?: string }) {
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm text-muted-foreground">{title}</CardTitle>
      </CardHeader>
      <CardContent>
        {children}
        {hint && <p className="text-xs text-muted-foreground mt-1">{hint}</p>}
      </CardContent>
    </Card>
  );
}

export function Bandwidth() {
  const [snap, setSnap] = useState<BandwidthSnapshot | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);

  useEffect(() => {
    const load = () => {
      api
        .bandwidth()
        .then((data) => {
          setSnap(data);
          setError(null);
        })
        .catch((e) => setError(String(e.message ?? e)));
    };
    load();
    const interval = setInterval(load, 60000);
    return () => clearInterval(interval);
  }, []);

  const iface: BandwidthInterface | null = useMemo(() => {
    if (!snap || snap.interfaces.length === 0) return null;
    return snap.interfaces.find((i) => i.name === selected) ?? snap.interfaces[0];
  }, [snap, selected]);

  // The last five-minute bucket is still filling up, so its average reads low.
  // Use the one before it as "current".
  const current = iface && iface.fiveminute.length >= 2
    ? iface.fiveminute[iface.fiveminute.length - 2]
    : iface?.fiveminute[iface.fiveminute.length - 1];

  const peakDown = iface ? Math.max(0, ...iface.fiveminute.map((p) => p.rx_mbps)) : 0;
  const peakUp = iface ? Math.max(0, ...iface.fiveminute.map((p) => p.tx_mbps)) : 0;
  const monthBytes = iface ? iface.day.reduce((sum, p) => sum + p.rx + p.tx, 0) : 0;

  const dayData = useMemo(
    () =>
      (iface?.day ?? []).map((p) => ({
        t: p.t,
        rxGb: (p.rx + p.tx) / 1e9,
      })),
    [iface],
  );

  if (error) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">Bandwidth</h1>
        <Card>
          <CardContent className="pt-6">
            <p className="text-red-500">Statistics unavailable.</p>
            <p className="text-sm text-muted-foreground mt-2">{error}</p>
            <p className="text-sm text-muted-foreground mt-2">
              The host timer writes <code>vnstat --json</code> into the data volume every five
              minutes. If this persists, check <code>vnstat-export.timer</code> on the server.
            </p>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!snap || !iface) {
    return (
      <div className="space-y-6">
        <h1 className="text-2xl font-bold">Bandwidth</h1>
        <p className="text-muted-foreground">Loading…</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between flex-wrap gap-3">
        <h1 className="text-2xl font-bold">Bandwidth</h1>
        <div className="flex gap-1">
          {snap.interfaces.map((i) => (
            <button
              key={i.name}
              type="button"
              onClick={() => setSelected(i.name)}
              className={`px-3 py-1.5 rounded-md text-sm font-medium ${
                i.name === iface.name
                  ? "bg-secondary text-secondary-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {label(i.name)}
            </button>
          ))}
        </div>
      </div>

      {snap.stale && (
        <Card>
          <CardContent className="pt-6">
            <p className="text-amber-600 text-sm">
              Data is not being refreshed — last export{" "}
              {Math.round((Date.now() / 1000 - snap.updated_at) / 60)} minutes ago. Charts below
              show whatever was collected before it stopped.
            </p>
          </CardContent>
        </Card>
      )}

      <div className="grid grid-cols-3 gap-4">
        <Metric title="Current" hint="Average over the last completed 5 minutes">
          <p className="text-2xl font-bold" style={{ color: DOWNLOAD_COLOR }}>
            ↓ {mbps(current?.rx_mbps ?? 0)}
          </p>
          <p className="text-lg font-semibold" style={{ color: UPLOAD_COLOR }}>
            ↑ {mbps(current?.tx_mbps ?? 0)}
          </p>
        </Metric>
        <Metric title="Peak, last 24h" hint={`${percentOfPlan(Math.max(peakDown, peakUp))}% of the ${PLAN_LIMIT_MBPS} Mbit/s plan`}>
          <p className="text-2xl font-bold" style={{ color: DOWNLOAD_COLOR }}>
            ↓ {mbps(peakDown)}
          </p>
          <p className="text-lg font-semibold" style={{ color: UPLOAD_COLOR }}>
            ↑ {mbps(peakUp)}
          </p>
        </Metric>
        <Metric title="Traffic, last 30 days" hint={`${iface.day.length} days recorded`}>
          <p className="text-3xl font-bold">{formatBytes(monthBytes)}</p>
        </Metric>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Last 24 hours</CardTitle>
        </CardHeader>
        <CardContent>
          <ResponsiveContainer width="100%" height={260}>
            <LineChart data={iface.fiveminute}>
              <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
              <XAxis dataKey="t" tickFormatter={clockLabel} minTickGap={40} fontSize={12} />
              <YAxis unit=" Mbit/s" width={80} fontSize={12} />
              <Tooltip
                formatter={(value, name) => [
                  mbps(Number(value)),
                  name === "rx_mbps" ? "Download" : "Upload",
                ]}
                labelFormatter={(t) => clockLabel(Number(t))}
              />
              <ReferenceLine
                y={PLAN_LIMIT_MBPS}
                stroke="#ef4444"
                strokeDasharray="4 4"
                label={{ value: `plan ${PLAN_LIMIT_MBPS}`, position: "insideTopRight", fontSize: 11 }}
              />
              <Line
                type="monotone"
                dataKey="rx_mbps"
                stroke={DOWNLOAD_COLOR}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
              <Line
                type="monotone"
                dataKey="tx_mbps"
                stroke={UPLOAD_COLOR}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
          <p className="text-xs text-muted-foreground mt-3">
            Each point averages five minutes, so a 30-second burst at 50 Mbit/s shows up as 5. Good
            enough to tell whether evenings hit the ceiling; not a peak meter.
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Daily volume</CardTitle>
        </CardHeader>
        <CardContent>
          {dayData.length === 0 ? (
            <p className="text-muted-foreground">No daily data yet.</p>
          ) : (
            <ResponsiveContainer width="100%" height={200}>
              <BarChart data={dayData}>
                <CartesianGrid strokeDasharray="3 3" opacity={0.2} />
                <XAxis dataKey="t" tickFormatter={dayLabel} fontSize={12} />
                <YAxis unit=" GB" width={70} fontSize={12} />
                <Tooltip
                  formatter={(value) => [`${Number(value).toFixed(1)} GB`, "Total"]}
                  labelFormatter={(t) => dayLabel(Number(t))}
                />
                <Bar dataKey="rxGb" fill={DOWNLOAD_COLOR} isAnimationActive={false} />
              </BarChart>
            </ResponsiveContainer>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
