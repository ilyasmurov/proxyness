# VPN Benchmark: WireGuard/Amnezia vs Proxyness vs Outline

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

## Test 2: Proxyness (server 95.181.162.242)

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

| Metric              | WireGuard/Amnezia | Proxyness       | Outline          |
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
- **Proxyness** значительно быстрее по DNS (~3x) — резолвинг идёт локально, не через туннель
- **WireGuard** выигрывает по TTFB (~2-3x) — работает на сетевом уровне (L3), нет доп. TLS-хопа
- **WireGuard** быстрее по скорости скачивания (~1.7x) — меньше overhead
- Proxyness и Outline не проксируют ICMP
- WireGuard/Amnezia на старом VPS (82.97.246.65), Proxyness на Timeweb NL (95.181.162.242), Outline на том же Timeweb

---

## Test 4: Proxyness v1.22.0 (server 95.181.162.242, Aeza NL)

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

## Comparison (Proxyness v1.22.0 vs old)

| Metric              | Proxyness (old)   | Proxyness v1.22.0 | Change           |
|---------------------|---------------------|----------------------|------------------|
| Ping 8.8.8.8        | 61.3 ms             | 64.6 ms              | ~same            |
| DNS avg             | 68 ms               | 63 ms                | -7% faster       |
| TTFB google.com     | 1.29 s              | 1.75 s               | +35% slower      |
| TTFB github.com     | 1.95 s              | 0.77 s               | -60% faster      |
| TTFB ya.ru          | 1.39 s              | 1.01 s               | -27% faster      |
| TTFB telegram.org   | 2.08 s              | 0.87 s               | -58% faster      |
| Download            | 4.9 MB/s (39 Mbps)  | 8.2 MB/s (66 Mbps)   | +67% faster      |
| Upload              | 0.16 MB/s (1.3 Mbps)| 5.5 MB/s (44 Mbps)   | +34x faster      |

---

## Test 5: Proxyness UDP transport (server 95.181.162.242, Aeza NL)

> 2026-04-04. UDP transport (XChaCha20-Poly1305, ECDH handshake, multiplexed streams).

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.8 ms | 62.6 ms | 69.0 ms | 3.3 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 62 ms |
| github.com   | 72 ms |
| ya.ru        | 66 ms |
| telegram.org | 65 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.140 s  | 0.904 s  | 0.916 s  |
| https://youtube.com  | 0.135 s  | 1.018 s  | 1.204 s  |
| https://github.com   | 0.073 s  | 0.386 s  | 0.428 s  |
| https://ya.ru        | 0.004 s  | 0.598 s  | 0.598 s  |
| https://telegram.org | 0.069 s  | 0.435 s  | 0.435 s  |

### Speed
| Direction | Speed       | Notes                                    |
|-----------|-------------|------------------------------------------|
| Download  | N/A         | Drops at ~600 KB (no retransmission)     |
| Upload    | N/A         | Drops at ~9 MB (no retransmission)       |

> UDP не имеет reliability layer — любой потерянный пакет приводит к обрыву TCP-стрима.
> Скачивание файлов до ~500 KB работает стабильно.

### Small download reliability
| Size     | Result   |
|----------|----------|
| 100 KB   | OK       |
| 500 KB   | OK       |
| 1 MB     | Drops    |
| 5 MB     | Drops    |

---

## Test 5b: Proxyness TLS transport (same session)

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.112 s  | 1.580 s  | 1.770 s  |
| https://youtube.com  | 0.006 s  | 1.484 s  | 1.964 s  |
| https://github.com   | 0.120 s  | 0.823 s  | 1.297 s  |
| https://ya.ru        | 0.004 s  | 0.985 s  | 0.985 s  |
| https://telegram.org | 0.003 s  | 0.780 s  | 0.804 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 6.0 MB/s    | 25 MB via Cloudflare, 4.0 s    |
| Upload    | 7.3 MB/s    | 25 MB via Cloudflare, 3.4 s    |

---

## Test 6: Proxyness UDP+ARQ transport v1.23.11 (server 95.181.162.242, Aeza NL)

> 2026-04-05. UDP transport with ARQ reliability layer (CUBIC congestion control, retransmission, reordering).

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.7 ms | 61.3 ms | 65.7 ms | 1.8 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 63 ms |
| github.com   | 66 ms |
| ya.ru        | 61 ms |
| telegram.org | 64 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.160 s  | 0.914 s  | 0.924 s  |
| https://youtube.com  | 0.150 s  | 1.008 s  | 1.265 s  |
| https://github.com   | 0.082 s  | 0.393 s  | 0.550 s  |
| https://ya.ru        | 0.004 s  | 0.580 s  | 0.580 s  |
| https://telegram.org | 0.080 s  | 0.465 s  | 0.467 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 0.7 MB/s    | 25 MB via Cloudflare, 30 s (cwnd limited) |
| Upload    | 3.2 MB/s    | 25 MB via Cloudflare, 7.8 s    |

> ARQ добавил reliability — bulk transfer работает стабильно (не дропается).
> Download ограничен cwnd death spiral (ssthresh=initCwnd после loss → congestion avoidance).
> Upload значительно лучше (3.2 MB/s) — 12x быстрее WireGuard.

---

## Full Comparison

| Metric              | WireGuard | Outline   | Proxyness TLS  | Proxyness UDP (no ARQ) | Proxyness UDP+ARQ |
|---------------------|-----------|-----------|-------------------|--------------------------|---------------------|
| Ping 8.8.8.8        | 102 ms    | N/A       | 63 ms             | 63 ms                    | 61 ms               |
| DNS avg             | 189 ms    | 219 ms    | 66 ms             | 66 ms                    | 64 ms               |
| TTFB google.com     | 0.47 s    | timeout   | 1.58 s            | 0.90 s                   | **0.91 s**          |
| TTFB github.com     | 0.85 s    | timeout   | 0.82 s            | 0.39 s                   | **0.39 s**          |
| TTFB ya.ru          | 0.45 s    | timeout   | 0.99 s            | 0.60 s                   | **0.58 s**          |
| TTFB telegram.org   | 1.13 s    | timeout   | 0.78 s            | 0.44 s                   | **0.47 s**          |
| Download            | 8.3 MB/s  | 0.01 MB/s | 6.0 MB/s          | ~500 KB max (drops)      | **0.7 MB/s**        |
| Upload              | 0.26 MB/s | 0 MB/s    | 7.3 MB/s          | ~9 MB max (drops)        | **3.2 MB/s**        |

### Выводы
- **UDP+ARQ TTFB = UDP без ARQ** — ARQ не добавил latency, TTFB на 40-50% быстрее TLS
- **UDP+ARQ быстрее WireGuard по TTFB** для github/telegram (0.39s vs 0.85s, 0.47s vs 1.13s)
- **ARQ обеспечил надёжность** — bulk transfer больше не дропается
- **Upload 3.2 MB/s** — в 12x быстрее WireGuard (0.26 MB/s)
- **Download 0.7 MB/s** — узкое место: cwnd death spiral (ssthresh зажат к initCwnd, CUBIC congestion avoidance вместо slow start). Следующая оптимизация.
- **TLS остаётся лучше для download** (6.0 MB/s) — TCP congestion control зрелее
- **AutoTransport** выбирает UDP — оптимально для browsing/messaging, TLS fallback для тяжёлых загрузок

---

## Test 7: Proxyness UDP+ARQ after BBR/pacing fixes (server 95.181.162.242, Aeza NL)

> 2026-04-06. After 5 congestion control fixes: BWE byte counting, BBR STARTUP phase, pacing defer until BWE stable.

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.7 ms | 60.2 ms | 61.4 ms | 0.5 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 66 ms |
| github.com   | 67 ms |
| ya.ru        | 60 ms |
| telegram.org | 61 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.143 s  | 1.140 s  | 1.466 s  |
| https://youtube.com  | 0.142 s  | 1.193 s  | 1.886 s  |
| https://github.com   | 0.106 s  | 0.503 s  | 0.701 s  |
| https://ya.ru        | 0.003 s  | 1.221 s  | 1.221 s  |
| https://telegram.org | 0.071 s  | 0.541 s  | 0.545 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 3.9 MB/s    | 25 MB via Cloudflare, 6.1 s    |
| Upload    | 1.0 MB/s    | 25 MB via Cloudflare, 24.2 s   |

> Superseded by Test 8 below — STARTUP exit was premature, results unreliable.

---

## Test 8: Proxyness UDP+ARQ — STARTUP re-entry + app-limited fix (server 95.181.162.242, Aeza NL)

> 2026-04-06. Fixes: prevent premature STARTUP exit (wait for BWE stability, skip app-limited rounds), re-enter STARTUP on idle→bulk transition, fix pacer burstSize truncation.

**External IP:** 95.181.162.242

### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.4 ms | 61.2 ms | 68.0 ms | 2.4 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 66 ms |
| youtube.com  | 63 ms |
| github.com   | 67 ms |
| ya.ru        | 66 ms |
| telegram.org | 62 ms |

### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.007 s  | 0.768 s  | 0.782 s  |
| https://youtube.com  | 0.007 s  | 0.852 s  | 1.094 s  |
| https://github.com   | 0.003 s  | 0.310 s  | 0.427 s  |
| https://ya.ru        | 0.002 s  | 0.607 s  | 0.608 s  |
| https://telegram.org | 0.069 s  | 0.458 s  | 0.459 s  |

### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 5.0 MB/s    | 25 MB via Cloudflare, 4.7 s    |
| Upload    | 5.0 MB/s    | 25 MB via Cloudflare, 5.0 s    |

> **Download +7.1x** vs Test 6 (0.7→5.0 MB/s) — STARTUP re-entry on idle→bulk + app-limited round skip.
> **Upload +1.6x** vs Test 6 (3.2→5.0 MB/s) — premature STARTUP exit fix + pacer burst rounding.
> **Stable**: two consecutive runs both 5.0/5.0. No more download instability.
> **vs TLS**: TLS download was 6.0 MB/s (Test 5). UDP now at 83% of TLS — close to parity.
> **vs WireGuard**: WG was 0.26 MB/s upload (Test 5). UDP is 19x faster.

---

## Test 9: Proxyness v1.24.0 UDP vs WireGuard (2026-04-06)

> Side-by-side comparison on same machine, same time. Proxyness: Aeza NL (95.181.162.242). WireGuard/Amnezia: Timeweb (82.97.246.65).

### Proxyness UDP v1.24.0

**External IP:** 95.181.162.242

#### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 59.8 ms | 61.2 ms | 64.6 ms | 1.4 ms | 0%    |
| 1.1.1.1    | —       | —       | —       | —      | 100%  |
| ya.ru      | —       | —       | —       | —      | 100%  |

#### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 62 ms |
| github.com   | 66 ms |
| ya.ru        | 61 ms |
| telegram.org | 62 ms |

#### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.142 s  | 0.943 s  | 1.172 s  |
| https://youtube.com  | 0.135 s  | 1.013 s  | 1.505 s  |
| https://github.com   | 0.074 s  | 0.381 s  | 0.533 s  |
| https://ya.ru        | 0.004 s  | 0.600 s  | 0.600 s  |
| https://telegram.org | 0.070 s  | 0.428 s  | 0.431 s  |

#### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 5.0 MB/s    | 25 MB via Cloudflare, 4.8 s    |
| Upload    | 4.6 MB/s    | 25 MB via Cloudflare, 5.4 s    |

### WireGuard/Amnezia (same session)

**External IP:** 82.97.246.65

#### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 98.0 ms | 99.7 ms | 105.5 ms| 2.1 ms | 0%    |
| 1.1.1.1    | 139.3 ms| 142.9 ms| 148.3 ms| 3.2 ms | 0%    |
| ya.ru      | 141.3 ms| 145.0 ms| 160.7 ms| 5.8 ms | 10%   |

#### DNS Resolution
| Domain       | Time   |
|--------------|--------|
| google.com   | 110 ms |
| youtube.com  | 103 ms |
| github.com   | 109 ms |
| ya.ru        | 98 ms  |
| telegram.org | 98 ms  |

#### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.205 s  | 0.659 s  | 0.946 s  |
| https://youtube.com  | 0.493 s  | 1.455 s  | 2.319 s  |
| https://github.com   | 0.262 s  | 0.490 s  | 12.836 s |
| https://ya.ru        | 0.145 s  | 0.747 s  | 0.749 s  |
| https://telegram.org | 0.280 s  | 0.696 s  | 0.709 s  |

#### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 2.6 MB/s    | 25 MB via Cloudflare, 9.3 s    |
| Upload    | 6.3 MB/s    | 25 MB via Cloudflare, 4.0 s    |

### Head-to-head

| Metric              | Proxyness UDP | WireGuard | Winner        |
|---------------------|-----------------|-----------|---------------|
| Ping 8.8.8.8        | **61 ms**       | 100 ms    | Proxyness   |
| DNS avg             | **63 ms**       | 104 ms    | Proxyness   |
| TTFB google.com     | 0.94 s          | **0.66 s**| WireGuard     |
| TTFB github.com     | **0.38 s**      | 0.49 s    | Proxyness   |
| TTFB telegram.org   | **0.43 s**      | 0.70 s    | Proxyness   |
| Download            | **5.0 MB/s**    | 2.6 MB/s  | Proxyness   |
| Upload              | 4.6 MB/s        | **6.3 MB/s**| WireGuard   |

### Выводы
- **Download**: Proxyness **1.9x быстрее** (5.0 vs 2.6 MB/s)
- **Upload**: WireGuard **1.4x быстрее** (6.3 vs 4.6 MB/s)
- **Ping**: Proxyness **39% быстрее** (61 vs 100 ms) — разные VPS, но Aeza route лучше
- **DNS**: Proxyness **1.7x быстрее** — резолвинг локальный
- **TTFB**: Proxyness быстрее на github/telegram, WireGuard быстрее на google — примерный паритет
- **Важно**: серверы разные (Aeza NL vs Timeweb), не полностью чистое сравнение

---

## Test 10: Proxyness UDP vs TLS — same server (2026-04-06)

> Back-to-back comparison on same machine, same server (Aeza NL 95.181.162.242). v1.24.0.

### UDP transport

**External IP:** 95.181.162.242

#### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 60.0 ms | 63.5 ms | 68.3 ms | 3.3 ms | 0%    |

#### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 76 ms |
| youtube.com  | 70 ms |
| github.com   | 68 ms |
| ya.ru        | 62 ms |
| telegram.org | 61 ms |

#### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.259 s  | 1.066 s  | 1.443 s  |
| https://youtube.com  | 0.238 s  | 1.029 s  | 1.537 s  |
| https://github.com   | 0.121 s  | 0.450 s  | 0.607 s  |
| https://ya.ru        | 0.003 s  | 1.039 s  | 1.039 s  |
| https://telegram.org | 0.119 s  | 0.497 s  | 0.498 s  |

#### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 4.3 MB/s    | 25 MB via Cloudflare, 5.6 s    |
| Upload    | 4.6 MB/s    | 25 MB via Cloudflare, 5.5 s    |

### TLS transport (same session)

**External IP:** 95.181.162.242

#### Ping (10 packets)
| Target     | Min     | Avg     | Max     | Stddev | Loss  |
|------------|---------|---------|---------|--------|-------|
| 8.8.8.8    | 60.1 ms | 62.0 ms | 68.6 ms | 3.3 ms | 0%    |

#### DNS Resolution
| Domain       | Time  |
|--------------|-------|
| google.com   | 64 ms |
| youtube.com  | 66 ms |
| github.com   | 69 ms |
| ya.ru        | 71 ms |
| telegram.org | 61 ms |

#### HTTPS Latency (connect / TTFB / total)
| URL                  | Connect  | TTFB     | Total    |
|----------------------|----------|----------|----------|
| https://google.com   | 0.118 s  | 1.586 s  | 1.794 s  |
| https://youtube.com  | 0.008 s  | 1.651 s  | 2.251 s  |
| https://github.com   | 0.119 s  | 0.886 s  | 1.550 s  |
| https://ya.ru        | 0.003 s  | 0.995 s  | 0.995 s  |
| https://telegram.org | 0.003 s  | 0.823 s  | 0.832 s  |

#### Speed
| Direction | Speed       | Notes                          |
|-----------|-------------|--------------------------------|
| Download  | 7.6 MB/s    | 25 MB via Cloudflare, 3.1 s    |
| Upload    | 5.6 MB/s    | 25 MB via Cloudflare, 4.5 s    |

### Head-to-head

| Metric              | UDP         | TLS         | Winner |
|---------------------|-------------|-------------|--------|
| Ping 8.8.8.8        | 63.5 ms     | 62.0 ms     | ~same  |
| DNS avg             | 67 ms       | 66 ms       | ~same  |
| TTFB google.com     | **1.07 s**  | 1.59 s      | UDP (1.5x) |
| TTFB github.com     | **0.45 s**  | 0.89 s      | UDP (2.0x) |
| TTFB telegram.org   | **0.50 s**  | 0.82 s      | UDP (1.6x) |
| Download            | 4.3 MB/s    | **7.6 MB/s**| TLS (1.8x) |
| Upload              | 4.6 MB/s    | **5.6 MB/s**| TLS (1.2x) |

### Выводы
- **TTFB**: UDP **1.5-2x быстрее** — нет TCP-over-TCP, нет head-of-line blocking
- **Download**: TLS **1.8x быстрее** (7.6 vs 4.3 MB/s) — kernel TCP congestion control зрелее нашего BBR
- **Upload**: TLS **1.2x быстрее** (5.6 vs 4.6 MB/s)
- **Ping/DNS**: идентичны — оба через тот же сервер
- **Вывод**: UDP оптимален для browsing (TTFB), TLS — для тяжёлых загрузок. AutoTransport выбирает UDP по умолчанию — правильное решение для типичного использования
