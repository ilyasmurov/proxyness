import { useState, useEffect, useCallback, useRef } from "react";

const API_BASE = "http://127.0.0.1:9090";

declare const __APP_VERSION__: string;

declare global {
  interface Window {
    sysproxy: { enable: () => void; disable: () => void };
  }
}

interface DaemonStatus {
  status: "connected" | "disconnected";
  uptime: number;
}

export function useDaemon() {
  const [status, setStatus] = useState<DaemonStatus>({
    status: "disconnected",
    uptime: 0,
  });
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const prevStatus = useRef<string>("disconnected");

  const fetchStatus = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/status`);
      if (res.ok) {
        const data = await res.json();
        // Server disconnected us (health check failure)
        if (prevStatus.current === "connected" && data.status === "disconnected") {
          window.sysproxy.disable();
          if (data.error) {
            setError(data.error);
          }
        }
        prevStatus.current = data.status;
        setStatus({ status: data.status, uptime: data.uptime });
      }
    } catch {
      setError("Daemon not running");
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 2000);
    return () => clearInterval(interval);
  }, [fetchStatus]);

  const connect = useCallback(
    async (server: string, key: string) => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetch(`${API_BASE}/connect`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ server, key, version: __APP_VERSION__ }),
        });
        if (!res.ok) {
          setError(await res.text());
        } else {
          window.sysproxy.enable();
          await fetchStatus();
        }
      } catch {
        setError("Failed to connect");
      } finally {
        setLoading(false);
      }
    },
    [fetchStatus]
  );

  const disconnect = useCallback(async () => {
    setLoading(true);
    try {
      window.sysproxy.disable();
      await fetch(`${API_BASE}/disconnect`, { method: "POST" });
      await fetchStatus();
    } catch {
      setError("Failed to disconnect");
    } finally {
      setLoading(false);
    }
  }, [fetchStatus]);

  return { status, error, loading, connect, disconnect };
}
