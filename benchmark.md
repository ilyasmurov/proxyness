# VPN Benchmark: WireGuard/Amnezia vs SmurovProxy vs Outline

> Тест 1–3: 2026-04-04 (Timeweb VPS). Тест 4: 2026-04-04 (Aeza VPS, v1.22.0)

## Test 1: WireGuard/Amnezia (server 82.97.246.65)

**External IP:** 82.97.246.65

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss |
|------------|---------|---------|---------|--------|------|
| 8.8.8.8    | 98.3 ms | 102.0 ms| 117.1 ms| 5.3 ms | 0%   |
| 1.1.1.1    | 140.7 ms| 147.6 ms| 157.0 ms| 5.9 ms | 0%   |
| ya.ru      | 141.4 ms| 145.2 ms| 151.0 ms| 3.8 ms | 0%   |

### DNS Resolution
| Domain       | Time   |
|--------------|--------|
| google.com   | 142 ms |
| youtube.com  | 223 ms |
| github.com   | 222 ms |
| ya.ru        | 142 ms |
| telegram.org | 215 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.248 s  | 0.471 s  | 0.471 s  |
| https://youtube.com  | 0.247 s  | 0.468 s  | 0.471 s  |
| https://github.com   | 0.370 s  | 0.845 s  | 8.172 s  |
| https://ya.ru        | 0.147 s  | 0.446 s  | 0.446 s  |
| https://telegram.org | 0.363 s  | 1.127 s  | 1.127 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 8.3 MB/s    | 25 MB via Cloudflare, 3.0 s    |
| Upload    | 0.26 MB/s   | timeout 30s, sent ~7.9 MB      |

---

## Test 2: SmurovProxy (server 95.181.162.242)

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.9 ms | 61.3 ms | 64.5 ms | 1.9 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

> ICMP (ping) проксируется не для всех адресов — TUN engine пробрасывает только TCP/UDP.

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 65 ms |
| youtube.com  | 64 ms |
| github.com   | 79 ms |
| ya.ru        | 63 ms |
| telegram.org | 68 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.222 s  | 1.293 s  | 1.293 s  |
| https://youtube.com  | 0.151 s  | 1.140 s  | 1.142 s  |
| https://github.com   | 0.149 s  | 1.948 s  | 2.974 s  |
| https://ya.ru        | 0.003 s  | 1.386 s  | 1.386 s  |
| https://telegram.org | 0.152 s  | 2.076 s  | 2.076 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 4.9 MB/s    | 25 MB via Cloudflare, 5.1 s    |
| Upload    | 0.16 MB/s   | timeout 30s, sent ~4.8 MB      |

---

## Test 3: Outline (server 82.97.246.65, Shadowsocks)

**External IP:** 82.97.246.65

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss | Note            |
|------------|---------|---------|---------|--------|------|-----------------|
| 8.8.8.8    | 0.3 ms  | 0.5 ms  | 0.7 ms  | 0.1 ms | 0%   | bypasses tunnel |
| 1.1.1.1    | 0.3 ms  | 0.5 ms  | 1.4 ms  | 0.3 ms | 0%   | bypasses tunnel |
| ya.ru      | 0.2 ms  | 0.4 ms  | 0.5 ms  | 0.1 ms | 0%   | bypasses tunnel |

> ICMP не проксируется — пинги идут напрямую (sub-ms = локальные).

### DNS Resolution
| Domain       | Time   |
|--------------|--------|
| google.com   | 258 ms |
| youtube.com  | 164 ms |
| github.com   | 251 ms |
| ya.ru        | 262 ms |
| telegram.org | 159 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total     |
|----------------------|----------|----------|-----------|
| https://google.com   | timeout  | timeout  | 10.0 s    |
| https://youtube.com  | timeout  | timeout  | 10.0 s    |
| https://github.com   | timeout  | timeout  | 10.0 s    |
| https://ya.ru        | timeout  | timeout  | 10.0 s    |
| https://telegram.org | timeout  | timeout  | 10.0 s    |

### Speed
| Direction | Speed       | Notes                              |
|-----------|-------------|------------------------------------|
| Download  | 0.012 MB/s  | timeout 30s, got only 385 KB       |
| Upload    | 0 MB/s      | timeout 30s, nothing sent          |

> Outline практически неработоспособен — HTTPS не открывается, скорость ~12 KB/s.

---

## Comparison

| Metric              | WireGuard/Amnezia | SmurovProxy       | Outline          |
|---------------------|-------------------|--------------------|------------------|
| Ping 8.8.8.8        | 102 ms            | 61 ms              | N/A (bypassed)   |
| Ping 1.1.1.1        | 148 ms            | 100% loss          | N/A (bypassed)   |
| DNS avg             | 189 ms            | 68 ms              | 219 ms           |
| TTFB google.com     | 0.47 s            | 1.29 s             | timeout          |
| TTFB youtube.com    | 0.47 s            | 1.14 s             | timeout          |
| TTFB ya.ru          | 0.45 s            | 1.39 s             | timeout          |
| Download            | 8.3 MB/s (66 Mbps)| 4.9 MB/s (39 Mbps)| 0.012 MB/s       |
| Upload              | 0.26 MB/s         | 0.16 MB/s          | 0 MB/s           |

### Notes
- **Outline** практически неработоспособен — HTTPS тайм-аутится, скорость ~12 KB/s
- **SmurovProxy** значительно быстрее по DNS (~3x) — резолвинг идёт локально, не через туннель
- **WireGuard** выигрывает по TTFB (~2-3x) — работает на сетевом уровне (L3), нет доп. TLS-хопа
- **WireGuard** быстрее по скорости скачивания (~1.7x) — меньше overhead
- SmurovProxy и Outline не проксируют ICMP
- WireGuard/Amnezia на старом VPS (82.97.246.65), SmurovProxy на Timeweb NL (95.181.162.242), Outline на том же Timeweb

---

## Test 4: SmurovProxy v1.22.0 (server 95.181.162.242, Aeza NL)

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 62.5 ms | 64.6 ms | 70.8 ms | 2.5 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 61 ms |
| github.com   | 64 ms |
| ya.ru        | 60 ms |
| telegram.org | 65 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.150 s  | 1.745 s  | 1.957 s  |
| https://youtube.com  | 0.144 s  | 1.823 s  | 2.322 s  |
| https://github.com   | 0.068 s  | 0.771 s  | 1.271 s  |
| https://ya.ru        | 0.003 s  | 1.012 s  | 1.012 s  |
| https://telegram.org | 0.069 s  | 0.871 s  | 0.877 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 8.2 MB/s    | 25 MB via Cloudflare, 3.0 s    |
| Upload    | 5.5 MB/s    | 25 MB via Cloudflare, 4.8 s    |

---

## Comparison (SmurovProxy v1.22.0 vs old)

| Metric              | SmurovProxy (old)   | SmurovProxy v1.22.0 | Change           |
|---------------------|---------------------|----------------------|------------------|
| Ping 8.8.8.8        | 61.3 ms             | 64.6 ms              | ~same            |
| DNS avg             | 68 ms               | 63 ms                | -7% faster       |
| TTFB google.com     | 1.29 s              | 1.75 s               | +35% slower      |
| TTFB github.com     | 1.95 s              | 0.77 s               | -60% faster      |
| TTFB ya.ru          | 1.39 s              | 1.01 s               | -27% faster      |
| TTFB telegram.org   | 2.08 s              | 0.87 s               | -58% faster      |
| Download            | 4.9 MB/s (39 Mbps)  | 8.2 MB/s (66 Mbps)   | +67% faster      |
| Upload              | 0.16 MB/s (1.3 Mbps)| 5.5 MB/s (44 Mbps)   | +34x faster      |
