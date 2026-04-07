import type {
  LocalSite,
  PendingOp,
  SyncRequest,
  SyncResponse,
  SyncResult,
  RemoteSite,
} from "./types";
import {
  loadState,
  saveLocalSites,
  savePendingOps,
  saveLastSyncAt,
  hasLocalSites,
  readLegacySites,
  readLegacyEnabled,
  clearLegacy,
} from "./storage";

const API_BASE = "https://proxy.smurov.com";
const STORAGE_KEY = "smurov-proxy-key"; // same key the tunnel uses

// Module-level state; initialized on first initOnce() call.
let localSites: LocalSite[] = [];
let pendingOps: PendingOp[] = [];
let lastSyncAt = 0;
let initialized = false;
let initPromise: Promise<void> | null = null;
let tempIdSeq = -1;

const listeners = new Set<() => void>();

function notify(): void {
  for (const fn of listeners) fn();
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// initOnce loads persisted state, runs bootstrap if needed, and runs the
// legacy migration. Idempotent: subsequent calls return the same Promise.
export function initOnce(): Promise<void> {
  if (initPromise) return initPromise;
  initPromise = (async () => {
    if (initialized) return;
    const state = loadState();
    localSites = state.localSites;
    pendingOps = state.pendingOps;
    lastSyncAt = state.lastSyncAt;

    if (!hasLocalSites()) {
      await bootstrapFromBundle();
    }
    runLegacyMigrationIfNeeded();
    initialized = true;
  })();
  return initPromise;
}

export function getLocalSites(): LocalSite[] {
  return localSites;
}

export function getLastSyncAt(): number {
  return lastSyncAt;
}

export function addSite(primaryDomain: string, label: string): LocalSite {
  const now = Math.floor(Date.now() / 1000);
  const id = tempIdSeq--;

  const site: LocalSite = {
    id,
    slug: label.toLowerCase().replace(/[^a-z0-9]+/g, "").slice(0, 32) || "site",
    label,
    domains: [primaryDomain.toLowerCase()],
    ips: [],
    enabled: true,
    updatedAt: now,
  };
  localSites = [...localSites, site];
  pendingOps = [
    ...pendingOps,
    { op: "add", localId: id, site: { primary_domain: primaryDomain.toLowerCase(), label }, at: now },
  ];
  persist();
  notify();
  return site;
}

export function removeSite(siteId: number): void {
  const now = Math.floor(Date.now() / 1000);
  localSites = localSites.filter((s) => s.id !== siteId);
  // Only queue a server-side op for positive ids — negatives are unconfirmed adds
  if (siteId > 0) {
    pendingOps = [...pendingOps, { op: "remove", siteId, at: now }];
  } else {
    // Strip the pending add for this temp id (it never made it to the server)
    pendingOps = pendingOps.filter((op) => !(op.op === "add" && op.localId === siteId));
  }
  persist();
  notify();
}

export function toggleSite(siteId: number, enabled: boolean): void {
  const now = Math.floor(Date.now() / 1000);
  localSites = localSites.map((s) =>
    s.id === siteId ? { ...s, enabled, updatedAt: now } : s
  );
  if (siteId > 0) {
    pendingOps = [...pendingOps, { op: enabled ? "enable" : "disable", siteId, at: now }];
  }
  persist();
  notify();
}

export async function sync(): Promise<SyncResult> {
  const key = localStorage.getItem(STORAGE_KEY);
  if (!key) return { ok: false, error: "no key" };

  const requestBody: SyncRequest = {
    last_sync_at: lastSyncAt,
    ops: pendingOps.map(toWireOp),
  };

  let resp: Response;
  try {
    resp = await fetch(`${API_BASE}/api/sync`, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${key}`,
      },
      body: JSON.stringify(requestBody),
    });
  } catch (e) {
    return { ok: false, error: String(e) };
  }

  if (!resp.ok) {
    return { ok: false, error: `HTTP ${resp.status}` };
  }

  let body: SyncResponse;
  try {
    body = (await resp.json()) as SyncResponse;
  } catch (e) {
    return { ok: false, error: "bad json" };
  }

  // Log non-ok op results for debugging; drop them either way.
  for (const r of body.op_results) {
    if (r.status !== "ok") {
      console.warn("[sync] op result", r);
    }
  }

  localSites = body.my_sites.map(remoteToLocal);
  pendingOps = [];
  lastSyncAt = body.server_time;
  persist();
  finalizeLegacyMigration();
  notify();

  return { ok: true };
}

function toWireOp(op: PendingOp): SyncRequest["ops"][number] {
  switch (op.op) {
    case "add":
      return { op: "add", local_id: op.localId, site: op.site, at: op.at };
    case "remove":
      return { op: "remove", site_id: op.siteId, at: op.at };
    case "enable":
      return { op: "enable", site_id: op.siteId, at: op.at };
    case "disable":
      return { op: "disable", site_id: op.siteId, at: op.at };
  }
}

function remoteToLocal(r: RemoteSite): LocalSite {
  return {
    id: r.id,
    slug: r.slug,
    label: r.label,
    domains: r.domains,
    ips: r.ips,
    enabled: r.enabled,
    updatedAt: r.updated_at,
  };
}

function persist(): void {
  saveLocalSites(localSites);
  savePendingOps(pendingOps);
  saveLastSyncAt(lastSyncAt);
}

async function bootstrapFromBundle(): Promise<void> {
  // The bundled seed is a tiny JSON file shipped next to the app binary.
  // Electron main process exposes it via window.appInfo.getSeedSites().
  // If the seed isn't available (dev build, IPC not wired) — start empty
  // and rely on the first sync to populate.
  try {
    const seed = await (window as any).appInfo?.getSeedSites?.();
    if (!Array.isArray(seed)) {
      localSites = [];
      persist();
      return;
    }
    localSites = seed.map((s: any) => ({
      id: s.id,
      slug: s.slug,
      label: s.label,
      domains: s.domains,
      ips: s.ips || [],
      enabled: true,
      updatedAt: 0,
    }));
    persist();
  } catch {
    localSites = [];
    persist();
  }
}

function runLegacyMigrationIfNeeded(): void {
  const legacyCustom = readLegacySites();
  const legacyEnabled = readLegacyEnabled();
  if (legacyCustom == null && legacyEnabled == null) return;

  try {
    const custom: Array<{ domain: string; label: string }> = legacyCustom ? JSON.parse(legacyCustom) : [];
    const now = Math.floor(Date.now() / 1000);
    for (const s of custom) {
      if (!s.domain) continue;
      const id = tempIdSeq--;
      pendingOps = [
        ...pendingOps,
        { op: "add", localId: id, site: { primary_domain: s.domain.toLowerCase(), label: s.label || s.domain }, at: now },
      ];
    }

    // Legacy enabled state is NOT mapped — built-ins default to enabled and
    // custom sites always come in as enabled via their add op. A richer
    // migration can be added later if it turns out users had disabled states
    // they wanted preserved.

    persist();
    // Don't clear legacy keys yet — wait for the first successful sync.
  } catch (e) {
    console.warn("[sync] legacy migration failed", e);
  }
}

function finalizeLegacyMigration(): void {
  clearLegacy();
}
