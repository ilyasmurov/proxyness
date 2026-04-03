import { useEffect, useState } from "react";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { api, type ChangelogEntry } from "@/lib/api";

const typeConfig = {
  feature: { label: "Feature", color: "bg-green-500/20 text-green-400 border-green-500/30" },
  fix: { label: "Fix", color: "bg-red-500/20 text-red-400 border-red-500/30" },
  improvement: { label: "Improvement", color: "bg-blue-500/20 text-blue-400 border-blue-500/30" },
};

function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString("ru-RU", { day: "numeric", month: "short", year: "numeric" });
}

function groupByDate(entries: ChangelogEntry[]): Map<string, ChangelogEntry[]> {
  const groups = new Map<string, ChangelogEntry[]>();
  for (const e of entries) {
    const day = e.createdAt.slice(0, 10);
    const list = groups.get(day) || [];
    list.push(e);
    groups.set(day, list);
  }
  return groups;
}

export function Changelog() {
  const [entries, setEntries] = useState<ChangelogEntry[]>([]);
  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    api
      .changelog(page, 20)
      .then((r) => {
        setEntries(r.entries || []);
        setPages(r.pages);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [page]);

  if (loading) return <p className="text-muted-foreground">Loading...</p>;

  const groups = groupByDate(entries);

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Changelog</h1>

      {entries.length === 0 && (
        <p className="text-muted-foreground">No entries found.</p>
      )}

      {[...groups.entries()].map(([day, items]) => (
        <div key={day} className="space-y-2">
          <h2 className="text-sm font-medium text-muted-foreground">{formatDate(items[0].createdAt)}</h2>
          <div className="space-y-2">
            {items.map((e) => {
              const cfg = typeConfig[e.type] || typeConfig.improvement;
              return (
                <Card key={e.id} className="py-0">
                  <CardContent className="flex items-start gap-3 px-4 py-3">
                    <Badge variant="outline" className={`${cfg.color} shrink-0 mt-0.5 text-xs`}>
                      {cfg.label}
                    </Badge>
                    <div className="min-w-0">
                      <p className="text-sm font-medium">{e.title}</p>
                      {e.description && (
                        <p className="text-xs text-muted-foreground mt-0.5">{e.description}</p>
                      )}
                    </div>
                  </CardContent>
                </Card>
              );
            })}
          </div>
        </div>
      ))}

      {pages > 1 && (
        <div className="flex items-center justify-center gap-2 pt-4">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="px-3 py-1.5 text-sm rounded-md border bg-secondary hover:bg-secondary/80 disabled:opacity-30 disabled:cursor-default"
          >
            Previous
          </button>
          <span className="text-sm text-muted-foreground px-3">
            {page} / {pages}
          </span>
          <button
            onClick={() => setPage((p) => Math.min(pages, p + 1))}
            disabled={page >= pages}
            className="px-3 py-1.5 text-sm rounded-md border bg-secondary hover:bg-secondary/80 disabled:opacity-30 disabled:cursor-default"
          >
            Next
          </button>
        </div>
      )}
    </div>
  );
}
