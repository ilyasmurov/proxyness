## feat
Per-stream TCP close logging for TUN-routed traffic
Every long-lived (≥10s) TUN stream now prints a single line on close with destination IP:port, SNI host, process name, duration, bytes up/down, and the first error that tore it down (download/upload side + message). Short-lived chatter stays silent. Grep `[tun] tcp close:` in the daemon log to see what actually killed a long streaming connection.

## fix
Recover from silent route loss after network events
Each slow-poll tick now asks the helper to re-install the server host, DNS, and ifscope bypass routes via a new `refresh_routes` IPC action, without tearing down TUN. Rationale: `sendto()` was returning "network is unreachable" instantly despite routes visibly in netstat — a darwin stale neighbor cache on the gateway after an interface flap (Docker vmnetd, USB-ethernet, wifi blip). The only thing that cleared it was the user clicking disconnect+connect, because the helper's `route add -host <server> <gw>` forces fresh neighbor resolution for the gateway. `refresh_routes` runs just that part of the cycle on every tick, so browsers and apps stay connected while the kernel gets unstuck. The 2-minute budget + full engine restart path stays as the fallback. Slow-poll entry still dumps `netstat -rn` and each tick dumps arp + `route get <server>` so the next incident carries its own post-mortem.
