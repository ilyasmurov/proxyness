// Shared types for the sites sync module. Mirrors the server wire
// protocol defined in docs/superpowers/specs/2026-04-08-sites-catalog-sync.md.

export interface LocalSite {
  id: number; // positive = confirmed server id, negative = unconfirmed temp id
  slug: string;
  label: string;
  domains: string[]; // includes primary_domain as index [0] where possible
  ips: string[];
  enabled: boolean;
  updatedAt: number; // unix seconds
}

export type PendingOp =
  | { op: "add";     localId: number; site: { primary_domain: string; label: string }; at: number }
  | { op: "remove";  siteId: number; at: number }
  | { op: "enable";  siteId: number; at: number }
  | { op: "disable"; siteId: number; at: number };

export interface SyncRequest {
  last_sync_at: number;
  ops: Array<
    | { op: "add"; local_id: number; site: { primary_domain: string; label: string }; at: number }
    | { op: "remove"; site_id: number; at: number }
    | { op: "enable"; site_id: number; at: number }
    | { op: "disable"; site_id: number; at: number }
  >;
}

export interface OpResult {
  local_id?: number;
  site_id?: number;
  status: "ok" | "error" | "invalid" | "stale";
  deduped?: boolean;
  message?: string;
}

export interface RemoteSite {
  id: number;
  slug: string;
  label: string;
  domains: string[];
  ips: string[];
  enabled: boolean;
  updated_at: number;
}

export interface SyncResponse {
  op_results: OpResult[];
  my_sites: RemoteSite[];
  server_time: number;
}

export interface SyncResult {
  ok: boolean;
  error?: string;
}
