import { useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { formatBytes } from "@/lib/format";
import { Download, Package } from "lucide-react";

const GITHUB_REPO = "ilyasmurov/smurov-proxy";

interface Asset {
  name: string;
  size: number;
  download_count: number;
  browser_download_url: string;
}

interface GHRelease {
  tag_name: string;
  published_at: string;
  body: string;
  assets: Asset[];
}

function AppleIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 384 512" fill="currentColor" className={className}>
      <path d="M318.7 268.7c-.2-36.7 16.4-64.4 50-84.8-18.8-26.9-47.2-41.7-84.7-44.6-35.5-2.8-74.3 20.7-88.5 20.7-15 0-49.4-19.7-76.4-19.7C63.3 141.2 4 184.8 4 273.5q0 39.3 14.4 81.2c12.8 36.7 59 126.7 107.2 125.2 25.2-.6 43-17.9 75.8-17.9 31.8 0 48.3 17.9 76.4 17.9 48.6-.7 90.4-82.5 102.6-119.3-65.2-30.7-61.7-90-61.7-91.9zm-56.6-164.2c27.3-32.4 24.8-61.9 24-72.5-24.1 1.4-52 16.4-67.9 34.9-17.5 19.8-27.8 44.3-25.6 71.9 26.1 2 49.9-11.4 69.5-34.3z" />
    </svg>
  );
}

function WindowsIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 448 512" fill="currentColor" className={className}>
      <path d="M0 93.7l183.6-25.3v177.4H0V93.7zm0 324.6l183.6 25.3V268.4H0v149.9zm203.8 28L448 480V268.4H203.8v177.9zm0-380.6v180.1H448V32L203.8 65.7z" />
    </svg>
  );
}

function platformInfo(name: string): { label: string; icon: React.ReactNode } | null {
  if (name.endsWith(".pkg")) {
    return {
      label: name.includes("arm64") ? "macOS (Apple Silicon)" : "macOS (Intel)",
      icon: <AppleIcon className="w-4 h-4" />,
    };
  }
  if (name.endsWith(".exe")) {
    return {
      label: "Windows",
      icon: <WindowsIcon className="w-4 h-4" />,
    };
  }
  return null;
}

function isInstaller(name: string): boolean {
  return name.endsWith(".pkg") || name.endsWith(".exe");
}

function timeAgo(date: string): string {
  const seconds = Math.floor((Date.now() - new Date(date).getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return `${months}mo ago`;
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

  const [latest, ...older] = releases;

  return (
    <div className="space-y-8">
      <h1 className="text-2xl font-bold">Releases</h1>

      {!latest && <p className="text-muted-foreground">No releases found.</p>}

      {latest && (
        <Card className="border-primary/30">
          <CardHeader className="pb-3">
            <div className="flex items-center gap-3">
              <Package className="w-5 h-5 text-primary" />
              <CardTitle className="text-xl">{latest.tag_name}</CardTitle>
              <Badge>Latest</Badge>
              <span className="text-sm text-muted-foreground ml-auto">
                {timeAgo(latest.published_at)}
              </span>
            </div>
            {latest.body && (
              <p className="text-sm text-muted-foreground mt-3 whitespace-pre-line">{latest.body}</p>
            )}
          </CardHeader>
          <CardContent>
            <div className="grid gap-3 sm:grid-cols-2">
              {latest.assets.filter(isInstaller).map((a) => {
                const info = platformInfo(a.name);
                if (!info) return null;
                return (
                  <a
                    key={a.name}
                    href={a.browser_download_url}
                    className="flex items-center gap-3 p-4 rounded-lg border hover:bg-secondary/50 transition-colors"
                  >
                    <div className="p-2 rounded-md bg-secondary">{info.icon}</div>
                    <div className="flex-1 min-w-0">
                      <div className="font-medium text-sm">{info.label}</div>
                      <div className="text-xs text-muted-foreground">{formatBytes(a.size)}</div>
                    </div>
                    <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      <Download className="w-3.5 h-3.5" />
                      {a.download_count}
                    </div>
                  </a>
                );
              })}
            </div>
          </CardContent>
        </Card>
      )}

      {older.length > 0 && (
        <div className="space-y-4">
          <h2 className="text-lg font-semibold text-muted-foreground">Previous releases</h2>
          {older.map((r) => {
            const installers = r.assets.filter(isInstaller);
            return (
              <Card key={r.tag_name}>
                <CardHeader className="pb-3">
                  <div className="flex items-center gap-3">
                    <CardTitle className="text-base">{r.tag_name}</CardTitle>
                    <span className="text-sm text-muted-foreground ml-auto">
                      {timeAgo(r.published_at)}
                    </span>
                  </div>
                  {r.body && (
                    <p className="text-sm text-muted-foreground mt-2 whitespace-pre-line">{r.body}</p>
                  )}
                </CardHeader>
                {installers.length > 0 && (
                  <CardContent>
                    <div className="flex flex-wrap gap-2">
                      {installers.map((a) => {
                        const info = platformInfo(a.name);
                        if (!info) return null;
                        return (
                          <a
                            key={a.name}
                            href={a.browser_download_url}
                            className="inline-flex items-center gap-2 px-3 py-1.5 rounded-md border text-sm hover:bg-secondary/50 transition-colors"
                          >
                            {info.icon}
                            <span>{info.label}</span>
                            <span className="text-muted-foreground">·</span>
                            <span className="text-muted-foreground">{formatBytes(a.size)}</span>
                            <span className="text-muted-foreground">·</span>
                            <Download className="w-3 h-3 text-muted-foreground" />
                            <span className="text-muted-foreground">{a.download_count}</span>
                          </a>
                        );
                      })}
                    </div>
                  </CardContent>
                )}
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}
