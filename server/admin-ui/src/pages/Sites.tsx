import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { api } from "@/lib/api";
import type { SiteWithStats } from "@/lib/api";

export function Sites() {
  const [sites, setSites] = useState<SiteWithStats[]>([]);

  const load = () => api.listSites().then(setSites).catch(() => {});
  useEffect(() => { load(); }, []);

  const handleDelete = async (id: number, label: string) => {
    if (!confirm(`Delete site "${label}" and all its user links?`)) return;
    await api.deleteSite(id);
    load();
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Sites</h1>
        <div className="text-sm text-muted-foreground">{sites.length} total</div>
      </div>
      <Card>
        <CardContent className="p-0">
          <Table>
            <TableHeader><TableRow>
              <TableHead>Label</TableHead>
              <TableHead>Primary domain</TableHead>
              <TableHead>Slug</TableHead>
              <TableHead>Created by</TableHead>
              <TableHead className="text-right">Users</TableHead>
              <TableHead className="text-right">Domains</TableHead>
              <TableHead>Approved</TableHead>
              <TableHead></TableHead>
            </TableRow></TableHeader>
            <TableBody>
              {sites.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <Link to={`/admin/sites/${s.id}`} className="font-medium text-blue-500 hover:underline">
                      {s.label}
                    </Link>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{s.primary_domain}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{s.slug}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {s.created_by_user_name || <span className="italic">seed</span>}
                  </TableCell>
                  <TableCell className="text-right">{s.users_count}</TableCell>
                  <TableCell className="text-right">{s.domains_count}</TableCell>
                  <TableCell>
                    <Badge variant={s.approved ? "default" : "secondary"}>
                      {s.approved ? "Yes" : "No"}
                    </Badge>
                  </TableCell>
                  <TableCell>
                    <Button variant="destructive" size="sm" onClick={() => handleDelete(s.id, s.label)}>
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}
