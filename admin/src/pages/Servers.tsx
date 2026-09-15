import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, type ServiceConfigMap } from "@/lib/api";

// PRXNS-21: the exit list clients dial, in Auto priority order. Stored as a
// JSON string under service_config.servers on the config service; the client
// replaces its built-in list with this one wholesale when it is non-empty.
interface ServerRow {
  id: string;
  label: string;
  addr: string;
}

const ADDR_RE = /^[^\s:]+:\d{1,5}$/;

function parseRows(raw: string | undefined): ServerRow[] {
  if (!raw) return [];
  try {
    const list = JSON.parse(raw);
    if (!Array.isArray(list)) return [];
    return list
      .filter((e) => e && typeof e === "object")
      .map((e) => ({ id: String(e.id ?? ""), label: String(e.label ?? ""), addr: String(e.addr ?? "") }));
  } catch {
    return [];
  }
}

function validate(rows: ServerRow[]): string | null {
  const ids = new Set<string>();
  const addrs = new Set<string>();
  for (const [i, r] of rows.entries()) {
    const n = i + 1;
    if (!r.id.trim() || !r.label.trim() || !r.addr.trim()) return `Row ${n}: id, label and address are required`;
    if (!ADDR_RE.test(r.addr.trim())) return `Row ${n}: address must be host:port`;
    if (ids.has(r.id.trim())) return `Row ${n}: duplicate id`;
    if (addrs.has(r.addr.trim())) return `Row ${n}: duplicate address`;
    ids.add(r.id.trim());
    addrs.add(r.addr.trim());
  }
  return null;
}

export function Servers() {
  const [config, setConfig] = useState<ServiceConfigMap | null>(null);
  const [rows, setRows] = useState<ServerRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<string | null>(null);

  useEffect(() => {
    api
      .getServices()
      .then((cfg) => {
        setConfig(cfg);
        setRows(parseRows((cfg as Record<string, string | undefined>).servers));
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, []);

  const update = (i: number, patch: Partial<ServerRow>) =>
    setRows((prev) => prev.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const move = (i: number, dir: -1 | 1) =>
    setRows((prev) => {
      const j = i + dir;
      if (j < 0 || j >= prev.length) return prev;
      const next = [...prev];
      [next[i], next[j]] = [next[j], next[i]];
      return next;
    });
  const remove = (i: number) => setRows((prev) => prev.filter((_, j) => j !== i));
  const add = () => setRows((prev) => [...prev, { id: "", label: "", addr: "" }]);

  const save = async () => {
    const trimmed = rows.map((r) => ({ id: r.id.trim(), label: r.label.trim(), addr: r.addr.trim() }));
    const problem = validate(trimmed);
    if (problem) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      // Only the servers key is written; other service_config keys stay as they are.
      await api.setServices({ servers: trimmed.length ? JSON.stringify(trimmed) : "" } as unknown as ServiceConfigMap);
      setRows(trimmed);
      setSavedAt(new Date().toLocaleTimeString());
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <p className="text-muted-foreground">Loading...</p>;

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2">
        <h1 className="text-2xl font-bold">Servers</h1>
      </div>

      {error && (
        <div className="text-red-400 text-sm bg-red-500/10 border border-red-500/20 rounded-md px-3 py-2">{error}</div>
      )}

      <Card>
        <CardHeader>
          <CardTitle className="text-base">Exit list sent to clients</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <p className="text-sm text-muted-foreground">
            Clients dial these in order when set to Auto (after the last server that worked), and show them in
            Settings → Proxy Server. Address is host:port. An empty list means clients keep the list built into
            the app. Changes reach clients on their next config poll, within about five minutes.
          </p>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">#</TableHead>
                <TableHead>id</TableHead>
                <TableHead>label</TableHead>
                <TableHead>address</TableHead>
                <TableHead className="w-40"></TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="text-muted-foreground text-sm">
                    No servers configured — clients use the built-in list.
                  </TableCell>
                </TableRow>
              )}
              {rows.map((r, i) => (
                <TableRow key={i}>
                  <TableCell className="text-muted-foreground">{i + 1}</TableCell>
                  <TableCell>
                    <Input value={r.id} placeholder="aeza" onChange={(e) => update(i, { id: e.target.value })} />
                  </TableCell>
                  <TableCell>
                    <Input value={r.label} placeholder="Aeza NL" onChange={(e) => update(i, { label: e.target.value })} />
                  </TableCell>
                  <TableCell>
                    <Input
                      value={r.addr}
                      placeholder="178.236.252.28:443"
                      className="font-mono"
                      onChange={(e) => update(i, { addr: e.target.value })}
                    />
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1 justify-end">
                      <Button variant="outline" size="sm" onClick={() => move(i, -1)} disabled={i === 0} title="Move up">↑</Button>
                      <Button variant="outline" size="sm" onClick={() => move(i, 1)} disabled={i === rows.length - 1} title="Move down">↓</Button>
                      <Button variant="outline" size="sm" onClick={() => remove(i)} title="Remove">✕</Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="flex items-center gap-3">
            <Button variant="outline" onClick={add}>Add server</Button>
            <Button onClick={save} disabled={saving}>{saving ? "Saving..." : "Save"}</Button>
            {savedAt && <span className="text-xs text-muted-foreground">Saved at {savedAt}</span>}
          </div>

          {config && (config as Record<string, string | undefined>).proxy_server && (
            <p className="text-xs text-muted-foreground">
              Legacy <code>proxy_server</code> value: <code>{(config as Record<string, string | undefined>).proxy_server}</code> (no
              longer read by clients).
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
