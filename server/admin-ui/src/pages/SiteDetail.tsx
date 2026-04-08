import { useEffect, useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import type { SiteDetail as SiteDetailType } from "@/lib/api";

export function SiteDetail() {
  const { id } = useParams();
  const nav = useNavigate();
  const siteId = Number(id);
  const [site, setSite] = useState<SiteDetailType | null>(null);

  const load = () => api.getSite(siteId).then(setSite).catch(() => {});
  useEffect(() => { load(); }, [siteId]);

  const handleDeleteSite = async () => {
    if (!site) return;
    if (!confirm(`Delete site "${site.label}" and ALL user links + domains?`)) return;
    await api.deleteSite(siteId);
    nav("/admin/sites");
  };

  const handleDeleteDomain = async (domain: string) => {
    if (!confirm(`Remove domain "${domain}" from this site?`)) return;
    try {
      await api.deleteSiteDomain(siteId, domain);
      load();
    } catch (e) {
      alert(`Failed: ${(e as Error).message}`);
    }
  };

  if (!site) return <div className="text-muted-foreground">Loading…</div>;

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between">
        <div>
          <h1 className="text-2xl font-bold">{site.label}</h1>
          <div className="text-sm text-muted-foreground mt-1">
            <code>{site.primary_domain}</code> · slug <code>{site.slug}</code> · id {site.id}
          </div>
          <div className="text-xs text-muted-foreground mt-1">
            Created {new Date(site.created_at).toLocaleString()}
            {site.created_by_user_name && ` by ${site.created_by_user_name}`}
          </div>
        </div>
        <div className="flex gap-2 items-start">
          <Badge variant={site.approved ? "default" : "secondary"}>
            {site.approved ? "Approved" : "Not approved"}
          </Badge>
          <Button variant="destructive" onClick={handleDeleteSite}>Delete Site</Button>
        </div>
      </div>

      <Card>
        <CardHeader><CardTitle className="text-sm">Domains ({site.domains.length})</CardTitle></CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader><TableRow>
              <TableHead>Domain</TableHead>
              <TableHead>Primary</TableHead>
              <TableHead></TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {site.domains.map((d) => (
                <TableRow key={d.domain}>
                  <TableCell className="font-mono text-xs">{d.domain}</TableCell>
                  <TableCell>
                    {d.is_primary && <Badge variant="default">Primary</Badge>}
                  </TableCell>
                  <TableCell>
                    {!d.is_primary && (
                      <Button variant="destructive" size="sm" onClick={() => handleDeleteDomain(d.domain)}>
                        Remove
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle className="text-sm">Users ({site.users.length})</CardTitle></CardHeader>
        <CardContent className="p-0">
          {site.users.length === 0 ? (
            <div className="p-4 text-sm text-muted-foreground">No users have this site.</div>
          ) : (
            <Table>
              <TableHeader><TableRow>
                <TableHead>User</TableHead>
                <TableHead>Enabled</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow></TableHeader>
              <TableBody>
                {site.users.map((u) => (
                  <TableRow key={u.id}>
                    <TableCell className="font-medium">{u.name}</TableCell>
                    <TableCell>
                      <Badge variant={u.enabled ? "default" : "secondary"}>
                        {u.enabled ? "Yes" : "No"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {new Date(u.updated_at * 1000).toLocaleString()}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
