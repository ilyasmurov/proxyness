## feature
Mode selector in the header, traffic switch on the main tab
Full (TUN) / Browser only toggle is now always visible in the title bar, and the All traffic / Selected apps switch lives inline with the Main tab.

## feature
Landing page rebuilt
proxy.smurov.com — new hero, six feature cards (port 443, AutoTransport, hybrid TUN+Browser, split tunneling, Kill Switch, hardware-bound), benchmarks vs WireGuard and Outline, three-step onboarding, EN/RU language switcher with localStorage persistence, admin panel link removed, download buttons resolve to the latest GitHub release with proper Apple and Windows SVG logos.

## fix
Windows startup crash since 1.27 — loader-to-main window handoff
Create the main window before destroying the loader. The old order destroyed the loader first, which fired window-all-closed in the same tick, and on Windows that handler quit the app before createWindow could finish. macOS was unaffected because window-all-closed is a no-op there.

## fix
Windows daemon idle CPU went from 40-70% to 0-1%
A multi-week pprof hunt that uncovered nine independent bugs in the Windows packet path. Each fix unblocked the next one. Highlights:

- Physical interface cache was a no-op stub on Windows. Every protectedDial enumerated network adapters via GetAdaptersAddresses, which dominated CPU and GC.
- procinfo cache forced a full kernel TCP/UDP table scan on every miss, thrashing on browsers cycling ephemeral ports.
- IP_UNICAST_IF byte order was wrong (`<<16` instead of `<<24` / htonl) — Windows silently ignored the bogus value, "bypass" sockets followed the TUN default route, and the daemon's own packets looped back through bridgeInbound.
- UDP and TLS transports were using net.DialUDP / tls.DialWithDialer directly with no interface binding. Even after the byte-order fix, IP_UNICAST_IF turned out to be only "advisory" on connected UDP sockets — fix was binding the socket source IP via Dialer.LocalAddr to the physical interface.
- bridgeInbound and bridgeOutbound were allocating one fresh slice per packet, generating ~67 GB of garbage in 30 seconds. Reused buffers, dropped GC pressure 30×.
- bridgeOutbound silently died on the first packet because `Buffer.ReadAt` returns io.EOF on a successful full read (io.ReaderAt convention) and the new implementation treated that as fatal.
- NAT readLoop held a 64 KB buffer per entry for 60 seconds. Browsers doing hundreds of DNS lookups while watching YouTube ended up with 1 GB resident in NAT alone. Buffer shrunk to 2 KB, pooled via sync.Pool, default TTL dropped to 10s.

## fix
NAT readLoop captured aliased IP slices into a reused packet buffer
After the bridgeInbound buffer-reuse perf fix, NAT.HandlePacket was passing srcIP/dstIP slices that aliased the caller's buffer straight into a goroutine that outlived the call. After bridgeInbound moved to the next packet, the goroutine's stored IPs became garbage — reply UDP packets (DNS responses, voice) were being built with bogus headers and silently dropped. Browsers stayed working because PAC sites go through SOCKS5 (server-side resolve), but Telegram, Discord, and anything depending on local DNS broke. Both IPs are now cloned before the readLoop spawns.

## fix
Synchronous reentrancy guard in client startReconnect
The previous guard checked the React `reconnecting` state, which updates async — when the polling effect and a transport-error effect fired in the same tick, both saw `reconnecting=false` and spawned independent retry loops. Result: parallel /tun/start calls hammering the daemon with "engine already active, restarting" and rebuilding gVisor stack and NAT tables. Now uses a synchronous ref so only one retry loop runs at a time.

## improvement
Diagnostic crash logger and pprof endpoints
Daemon exposes /debug/pprof/* on its local 127.0.0.1:9090 API for CPU/heap/alloc profiling. Main process has an opt-in crash logger behind `--trace` (or SMUROV_DEBUG=1 on macOS — Windows requireAdministrator drops env vars on the elevated child, so use the flag there) that captures unhandled exceptions, renderer crashes, and per-phase boot traces to ~/Desktop/smurov-crash.log. Engine.Start now dumps a goroutine stack trace if it has to stop a still-active engine, so runaway client retry loops are immediately diagnosable.
