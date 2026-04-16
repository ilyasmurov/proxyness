import { useEffect, useRef, useState } from "react";
import type { ActiveConn, DeviceRate } from "@/lib/api";
import { authHeaders } from "@/lib/auth";

const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";

// Both streams go through Aeza (valid TLS cert). The /timeweb suffix
// tells the server to proxy the request to Timeweb over the WG tunnel.
const SERVERS = [
  { url: API_URL, suffix: "", label: "Aeza NL" },
  { url: API_URL, suffix: "/timeweb", label: "Timeweb NL" },
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

    function connectTo(url: string, suffix: string, label: string) {
      const key = `${url}${suffix}`;
      async function connect() {
        try {
          const res = await fetch(`${url}/admin/api/stats/stream${suffix}`, {
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
                  serverActive.current.set(key, parsed);
                  merge();
                } else if (event === "rate") {
                  const tagged = (parsed as any[]).map((d: any) => ({ ...d, server: label }));
                  serverRates.current.set(key, tagged);
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

    for (const { url, suffix, label } of SERVERS) {
      connectTo(url, suffix, label);
    }

    return () => controller.abort();
  }, []);

  return { active, rates };
}
