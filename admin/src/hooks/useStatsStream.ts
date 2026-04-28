import { useEffect, useRef, useState } from "react";
import type { ActiveConn, DeviceRate } from "@/lib/api";
import { authHeaders } from "@/lib/auth";

const API_URL = import.meta.env.VITE_API_URL || "https://proxyness.smurov.com";

export function useStatsStream() {
  const [active, setActive] = useState<ActiveConn[]>([]);
  const [rates, setRates] = useState<DeviceRate[]>([]);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    abortRef.current = controller;

    async function connect() {
      try {
        const res = await fetch(`${API_URL}/admin/api/stats/stream`, {
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
              if (event === "active") setActive(parsed);
              else if (event === "rate") setRates(parsed);
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
    return () => controller.abort();
  }, []);

  return { active, rates };
}
