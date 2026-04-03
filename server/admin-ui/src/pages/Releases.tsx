import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { formatBytes } from "@/lib/format";

const GITHUB_REPO = "ilyasmurov/smurov-proxy";

interface Asset {
  name: string;
  size: number;
  browser_download_url: string;
}

interface GHRelease {
  tag_name: string;
  published_at: string;
  body: string;
  assets: Asset[];
}

function platformLabel(name: string): string {
  if (name.endsWith(".pkg")) return name.includes("arm64") ? "macOS (Apple Silicon)" : "macOS (Intel)";
  if (name.endsWith(".exe")) return "Windows";
  return "";
}

function isInstaller(name: string): boolean {
  return name.endsWith(".pkg") || name.endsWith(".exe");
}

export function Releases() {
  const [releases, setReleases] = useState<GHRelease[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetch(`https://api.github.com/repos/${GITHUB_REPO}/releases`)
      .then((r) => r.json())
      .then(setReleases)
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  if (loading) return <p className="text-muted-foreground">Loading...</p>;

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Releases</h1>
      {releases.length === 0 && (
        <p className="text-muted-foreground">No releases found.</p>
      )}
      {releases.map((r, i) => {
        const installers = r.assets.filter((a) => isInstaller(a.name));
        return (
          <Card key={r.tag_name}>
            <CardHeader className="pb-3">
              <div className="flex items-center gap-3">
                <CardTitle className="text-lg">{r.tag_name}</CardTitle>
                {i === 0 && <Badge>Latest</Badge>}
                <span className="text-sm text-muted-foreground ml-auto">
                  {new Date(r.published_at).toLocaleDateString()}
                </span>
              </div>
              {r.body && (
                <p className="text-sm text-muted-foreground mt-2 whitespace-pre-line">{r.body}</p>
              )}
            </CardHeader>
            {installers.length > 0 && (
              <CardContent>
                <div className="space-y-2">
                  {installers.map((a) => (
                    <a
                      key={a.name}
                      href={a.browser_download_url}
                      className="flex items-center justify-between p-3 rounded-md border hover:bg-secondary/50 transition-colors"
                    >
                      <div className="flex items-center gap-3">
                        <span className="text-sm font-medium">{platformLabel(a.name)}</span>
                        <span className="text-xs text-muted-foreground">{a.name}</span>
                      </div>
                      <span className="text-sm text-muted-foreground">{formatBytes(a.size)}</span>
                    </a>
                  ))}
                </div>
              </CardContent>
            )}
          </Card>
        );
      })}
    </div>
  );
}
