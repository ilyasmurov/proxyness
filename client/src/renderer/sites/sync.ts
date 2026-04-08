import type { LocalSite, RemoteSite, SyncResult } from "./types";
import {
  loadState,
  saveLocalSites,
  saveLastSyncAt,
  hasLocalSites,
  readLegacySites,
  clearLegacy,
} from "./storage";

// Module-level state.
let localSites: LocalSite[] = [];
let lastSyncAt = 0;
let initialized = false;
let initPromise: Promise<void> | null = null;

const listeners = new Set<() => void>();

function notify(): void {
  for (const fn of listeners) fn();
}

export function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

// initOnce loads persisted state, runs bootstrap if needed, runs the
// legacy migration, and clears the deprecated pendingOps key from
// pre-daemon-mutations versions. Idempotent.
export function initOnce(): Promise<void> {
  if (initPromise) return initPromise;
  initPromise = (async () => {
    if (initialized) return;
    const state = loadState();
    localSites = state.localSites;
    lastSyncAt = state.lastSyncAt;

    // One-shot cleanup: pendingOps queue from pre-daemon-mutations versions.
    // We can't replay these reliably, and most users will have an empty queue.
    localStorage.removeItem("smurov-proxy-pending-ops");

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

// addSite adds a new site through the daemon. Returns the freshly-created
// LocalSite (with real server-assigned id). Throws on daemon error.
export async function addSite(primaryDomain: string, label: string): Promise<LocalSite> {
  const result = await (window as any).appInfo?.daemonAddSite(primaryDomain, label);
  if (!result || typeof result.site_id !== "number") {
    throw new Error("daemon-add-site: invalid response");
  }
  // After add, the daemon's cache contains the new site. Pull fresh snapshot.
  await sync();
  const created = localSites.find((s) => s.id === result.site_id);
  if (!created) {
    throw new Error("daemon-add-site: site not in fresh snapshot");
  }
  return created;
}

// removeSite removes a site through the daemon. Throws on error.
export async function removeSite(siteId: number): Promise<void> {
  const result = await (window as any).appInfo?.daemonRemoveSite(siteId);
  if (!result || result.ok !== true) {
    throw new Error("daemon-remove-site: failed");
  }
  // Replace localSites with fresh snapshot from response.
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  saveLocalSites(localSites);
  notify();
}

// toggleSite toggles per-user enabled flag through the daemon. Throws on error.
export async function toggleSite(siteId: number, enabled: boolean): Promise<void> {
  const result = await (window as any).appInfo?.daemonSetEnabled(siteId, enabled);
  if (!result || result.ok !== true) {
    throw new Error("daemon-set-enabled: failed");
  }
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  saveLocalSites(localSites);
  notify();
}

// sync refreshes localSites from the daemon's /sites/my endpoint. The
// daemon is the single source of truth for sites — it syncs with the
// catalog server in the background and serves the cache to the renderer.
// Called periodically (every 5 min) and on `online` event.
export async function sync(): Promise<SyncResult> {
  let result: any;
  try {
    result = await (window as any).appInfo?.daemonListSites();
  } catch (e) {
    return { ok: false, error: String(e) };
  }
  if (!result || !Array.isArray(result.my_sites)) {
    return { ok: false, error: "bad response" };
  }
  localSites = (result.my_sites as RemoteSite[]).map(remoteToLocal);
  lastSyncAt = Math.floor(Date.now() / 1000);
  saveLocalSites(localSites);
  saveLastSyncAt(lastSyncAt);
  finalizeLegacyMigration();
  notify();
  return { ok: true };
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

async function bootstrapFromBundle(): Promise<void> {
  try {
    const seed = await (window as any).appInfo?.getSeedSites?.();
    if (!Array.isArray(seed)) {
      localSites = [];
      saveLocalSites(localSites);
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
    saveLocalSites(localSites);
  } catch {
    localSites = [];
    saveLocalSites(localSites);
  }
}

function runLegacyMigrationIfNeeded(): void {
  const legacyCustom = readLegacySites();
  if (legacyCustom == null) return;
  // Best-effort: schedule the legacy custom sites to be added through daemon
  // on first successful sync. For MVP we just log and clear — most users won't
  // have any legacy custom sites at this point in the project lifecycle.
  console.info("[sync] legacy custom sites detected, clearing");
}

function finalizeLegacyMigration(): void {
  clearLegacy();
}
