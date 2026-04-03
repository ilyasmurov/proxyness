import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { api, ChangelogEntry } from "@/lib/api";

function versionColor(version: string): string {
  const [, minor] = version.replace("v", "").split(".").map(Number);
  if (minor >= 20) return "bg-blue-500/20 text-blue-400 border-blue-500/30";
  if (minor >= 15) return "bg-green-500/20 text-green-400 border-green-500/30";
  if (minor >= 10) return "bg-yellow-500/20 text-yellow-400 border-yellow-500/30";
  return "bg-zinc-500/20 text-zinc-400 border-zinc-500/30";
}

export function Changelog() {
  const [entries, setEntries] = useState<ChangelogEntry[]>([]);
  const [page, setPage] = useState(1);
  const [pages, setPages] = useState(1);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    api
      .changelog(page, 10)
      .then((r) => {
        setEntries(r.entries || []);
        setPages(r.pages);
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, [page]);

  if (loading) return <p className="text-muted-foreground">Loading...</p>;

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Changelog</h1>

      {entries.length === 0 && (
        <p className="text-muted-foreground">No entries found.</p>
      )}

      <div className="space-y-4">
        {entries.map((e) => (
          <Card key={e.id}>
            <CardHeader className="pb-3">
              <div className="flex items-center gap-3">
                <CardTitle className="text-lg">{e.version}</CardTitle>
                <Badge variant="outline" className={versionColor(e.version)}>
                  {e.date}
                </Badge>
              </div>
            </CardHeader>
            <CardContent>
              <ul className="space-y-1.5">
                {e.changes.split("\n").map((line, i) => (
                  <li key={i} className="flex items-start gap-2 text-sm text-muted-foreground">
                    <span className="text-primary mt-1.5 shrink-0 w-1.5 h-1.5 rounded-full bg-primary" />
                    {line}
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
        ))}
      </div>

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
