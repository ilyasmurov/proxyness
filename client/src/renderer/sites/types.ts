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

export interface RemoteSite {
  id: number;
  slug: string;
  label: string;
  domains: string[];
  ips: string[];
  enabled: boolean;
  updated_at: number;
}

export interface SyncResult {
  ok: boolean;
  error?: string;
}
