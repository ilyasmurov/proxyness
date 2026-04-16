import { useEffect, useState, useRef } from "react";
import { Badge } from "@/components/ui/badge";
import { api, type LogEntry } from "@/lib/api";

const levelConfig: Record<string, { label: string; color: string }> = {
  error: { label: "ERR", color: "bg-red-500/20 text-red-400 border-red-500/30" },
  warn: { label: "WRN", color: "bg-yellow-500/20 text-yellow-400 border-yellow-500/30" },
  info: { label: "INF", color: "bg-blue-500/20 text-blue-400 border-blue-500/30" },
};

export function Logs() {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [level, setLevel] = useState("");
  const [auto, setAuto] = useState(true);
  const bottomRef = useRef<HTMLDivElement>(null);

  const load = () => {
    api
      .logs(500, 0, level)
      .then((r) => {
        setEntries(r.entries || []);
        setTotal(r.total);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    setLoading(true);
    load();
  }, [level]);

  useEffect(() => {
    if (!auto) return;
    const id = setInterval(load, 3000);
    return () => clearInterval(id);
  }, [auto, level]);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Logs</h1>
        <div className="flex items-center gap-3">
          <span className="text-sm text-muted-foreground">{total} total</span>
          <select
            value={level}
            onChange={(e) => setLevel(e.target.value)}
            className="text-sm rounded-md border bg-secondary px-2 py-1"
          >
            <option value="">All levels</option>
            <option value="error">Errors</option>
            <option value="warn">Warnings</option>
            <option value="info">Info</option>
          </select>
          <label className="flex items-center gap-1.5 text-sm text-muted-foreground cursor-pointer">
            <input
              type="checkbox"
              checked={auto}
              onChange={(e) => setAuto(e.target.checked)}
              className="rounded"
            />
            Auto-refresh
          </label>
        </div>
      </div>

      {loading && entries.length === 0 ? (
        <p className="text-muted-foreground">Loading...</p>
      ) : entries.length === 0 ? (
        <p className="text-muted-foreground">No logs found.</p>
      ) : (
        <div className="rounded-md border bg-card overflow-auto max-h-[calc(100vh-200px)]">
          <table className="w-full text-sm font-mono">
            <thead className="sticky top-0 bg-card border-b">
              <tr className="text-left text-muted-foreground">
                <th className="px-3 py-2 w-[140px]">Time</th>
                <th className="px-3 py-2 w-[60px]">Level</th>
                <th className="px-3 py-2">Message</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => {
                const cfg = levelConfig[e.level] || levelConfig.info;
                return (
                  <tr key={e.id} className="border-b last:border-0 hover:bg-muted/50">
                    <td className="px-3 py-1.5 text-xs text-muted-foreground whitespace-nowrap">
                      {e.created_at.replace("T", " ").slice(0, 19)}
                    </td>
                    <td className="px-3 py-1.5">
                      <Badge variant="outline" className={`${cfg.color} text-xs`}>
                        {cfg.label}
                      </Badge>
                    </td>
                    <td className="px-3 py-1.5 text-xs break-all">{e.message}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          <div ref={bottomRef} />
        </div>
      )}
    </div>
  );
}
