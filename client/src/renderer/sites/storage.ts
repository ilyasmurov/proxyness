import type { LocalSite } from "./types";

// localStorage key namespace. Keep synced with the migration path that
// deletes legacy keys below.
const KEY_LOCAL     = "proxyness-local-sites";
const KEY_LAST_SYNC = "proxyness-last-sync-at";

// Legacy keys kept so the migration in sync.ts can find them.
export const LEGACY_KEY_SITES         = "proxyness-sites";
export const LEGACY_KEY_ENABLED_SITES = "proxyness-enabled-sites";

export interface PersistedState {
  localSites: LocalSite[];
  lastSyncAt: number;
}

export function loadState(): PersistedState {
  return {
    localSites: readJSON<LocalSite[]>(KEY_LOCAL, []),
    lastSyncAt: readJSON<number>(KEY_LAST_SYNC, 0),
  };
}

export function saveLocalSites(sites: LocalSite[]): void {
  localStorage.setItem(KEY_LOCAL, JSON.stringify(sites));
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
