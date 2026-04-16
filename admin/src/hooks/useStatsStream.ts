import { useEffect, useRef, useState } from "react";
import type { ActiveConn, DeviceRate } from "@/lib/api";
import { authHeaders } from "@/lib/auth";

const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";

const SERVERS = [
  API_URL,
  "https://proxy.smurov.com",
];

export function useStatsStream() {
  const [active, setActive] = useState<ActiveConn[]>([]);
  const [rates, setRates] = useState<DeviceRate[]>([]);
  const abortRef = useRef<AbortController | null>(null);
  // Per-server state, merged on each update
  const serverActive = useRef<Map<string, ActiveConn[]>>(new Map());
  const serverRates = useRef<Map<string, DeviceRate[]>>(new Map());

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;

    function merge() {
      const allActive: ActiveConn[] = [];
      for (const v of serverActive.current.values()) allActive.push(...v);
      setActive(allActive);

      const allRates: DeviceRate[] = [];
      for (const v of serverRates.current.values()) allRates.push(...v);
      setRates(allRates);
    }

    function connectTo(url: string) {
      async function connect() {
        try {
          const res = await fetch(`${url}/admin/api/stats/stream`, {
            headers: authHeaders(),
            signal: controller.signal,
          });
          if (!res.ok || !res.body) return;

          const reader = res.body.getReader();
          const decoder = new TextDecoder();
          let buf = "";

          while (true) {
            const { done, value } = await reader.read();
            if (done) break;
            buf += decoder.decode(value, { stream: true });

            const parts = buf.split("\n\n");
            buf = parts.pop()!;

            for (const part of parts) {
              let event = "";
              let data = "";
              for (const line of part.split("\n")) {
                if (line.startsWith("event: ")) event = line.slice(7);
                else if (line.startsWith("data: ")) data = line.slice(6);
              }
              if (!data) continue;
              try {
                const parsed = JSON.parse(data);
                if (event === "active") {
                  serverActive.current.set(url, parsed);
                  merge();
                } else if (event === "rate") {
                  serverRates.current.set(url, parsed);
                  merge();
                }
              } catch {}
            }
          }
        } catch (e: any) {
          if (e.name === "AbortError") return;
          await new Promise((r) => setTimeout(r, 3000));
          if (!controller.signal.aborted) connect();
        }
      }
      connect();
    }

    for (const url of SERVERS) {
      connectTo(url);
    }

    return () => controller.abort();
  }, []);

  return { active, rates };
}
