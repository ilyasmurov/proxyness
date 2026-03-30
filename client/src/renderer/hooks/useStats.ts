import { useState, useEffect, useCallback } from "react";

interface RatePoint {
  t: number;
  down: number;
  up: number;
}

export interface Stats {
  download: number;
  upload: number;
  history: RatePoint[];
}

const emptyStats: Stats = { download: 0, upload: 0, history: [] };

export function useStats(connected: boolean): Stats {
  const [stats, setStats] = useState<Stats>(emptyStats);

  const fetchStats = useCallback(async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/stats");
      const data = await res.json();
      setStats(data);
    } catch {
      // ignore fetch errors
    }
  }, []);

  useEffect(() => {
    if (!connected) {
      setStats(emptyStats);
      return;
    }
    fetchStats();
    const interval = setInterval(fetchStats, 1000);
    return () => clearInterval(interval);
  }, [connected, fetchStats]);

  return stats;
}
