import { useEffect, useState, useCallback, useRef } from "react";
import * as syncModule from "./sync";
import type { LocalSite } from "./types";

interface UseSitesReturn {
  sites: LocalSite[];
  syncing: boolean;
  lastSyncAt: number;
  ready: boolean;
  addSite: (primaryDomain: string, label: string) => LocalSite;
  removeSite: (siteId: number) => void;
  toggleSite: (siteId: number, enabled: boolean) => void;
  syncNow: () => Promise<void>;
}

export function useSites(): UseSitesReturn {
  const [sites, setSites] = useState<LocalSite[]>([]);
  const [ready, setReady] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [lastSyncAt, setLastSyncAt] = useState<number>(0);
  const syncingRef = useRef(false);

  // Initialize the sync module once after mount.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      await syncModule.initOnce();
      if (cancelled) return;
      setSites([...syncModule.getLocalSites()]);
      setLastSyncAt(syncModule.getLastSyncAt());
      setReady(true);
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const syncNow = useCallback(async () => {
    if (syncingRef.current || !ready) return;
    syncingRef.current = true;
    setSyncing(true);
    try {
      await syncModule.sync();
    } finally {
      syncingRef.current = false;
      setSyncing(false);
    }
  }, [ready]);

  // Subscribe to module state changes + register sync triggers.
  useEffect(() => {
    if (!ready) return;

    const unsub = syncModule.subscribe(() => {
      setSites([...syncModule.getLocalSites()]);
      setLastSyncAt(syncModule.getLastSyncAt());
    });

    // Initial sync after first render
    syncNow();

    // Sync on online event
    const onOnline = () => {
      syncNow();
    };
    window.addEventListener("online", onOnline);

    // Periodic sync every 5 minutes
    const interval = setInterval(syncNow, 5 * 60 * 1000);

    return () => {
      unsub();
      window.removeEventListener("online", onOnline);
      clearInterval(interval);
    };
  }, [ready, syncNow]);

  return {
    sites,
    syncing,
    lastSyncAt,
    ready,
    addSite: syncModule.addSite,
    removeSite: syncModule.removeSite,
    toggleSite: syncModule.toggleSite,
    syncNow,
  };
}
