import type { LocalSite, PendingOp } from "./types";

// localStorage key namespace. Keep synced with the migration path that
// deletes legacy keys below.
const KEY_LOCAL     = "smurov-proxy-local-sites";
const KEY_PENDING   = "smurov-proxy-pending-ops";
const KEY_LAST_SYNC = "smurov-proxy-last-sync-at";

// Legacy keys kept so the migration in sync.ts can find them.
export const LEGACY_KEY_SITES         = "smurov-proxy-sites";
export const LEGACY_KEY_ENABLED_SITES = "smurov-proxy-enabled-sites";

export interface PersistedState {
  localSites: LocalSite[];
  pendingOps: PendingOp[];
  lastSyncAt: number;
}

export function loadState(): PersistedState {
  return {
    localSites: readJSON<LocalSite[]>(KEY_LOCAL, []),
    pendingOps: readJSON<PendingOp[]>(KEY_PENDING, []),
    lastSyncAt: readJSON<number>(KEY_LAST_SYNC, 0),
  };
}

export function saveLocalSites(sites: LocalSite[]): void {
  localStorage.setItem(KEY_LOCAL, JSON.stringify(sites));
}

export function savePendingOps(ops: PendingOp[]): void {
  localStorage.setItem(KEY_PENDING, JSON.stringify(ops));
}

export function saveLastSyncAt(at: number): void {
  localStorage.setItem(KEY_LAST_SYNC, JSON.stringify(at));
}

export function hasLocalSites(): boolean {
  return localStorage.getItem(KEY_LOCAL) !== null;
}

export function readLegacySites(): string | null {
  return localStorage.getItem(LEGACY_KEY_SITES);
}

export function readLegacyEnabled(): string | null {
  return localStorage.getItem(LEGACY_KEY_ENABLED_SITES);
}

export function clearLegacy(): void {
  localStorage.removeItem(LEGACY_KEY_SITES);
  localStorage.removeItem(LEGACY_KEY_ENABLED_SITES);
}

function readJSON<T>(key: string, fallback: T): T {
  const raw = localStorage.getItem(key);
  if (raw == null) return fallback;
  try {
    return JSON.parse(raw) as T;
  } catch {
    return fallback;
  }
}
