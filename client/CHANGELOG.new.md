## feat
Per-stream TCP close logging for TUN-routed traffic
Every long-lived (≥10s) TUN stream now prints a single line on close with destination IP:port, SNI host, process name, duration, bytes up/down, and the first error that tore it down (download/upload side + message). Short-lived chatter stays silent. Grep `[tun] tcp close:` in the daemon log to see what actually killed a long streaming connection.

## fix
Recover from silent route loss after network events
Slow-poll wait (both TUN engine and SOCKS5 tunnel) now has a 2-minute budget instead of looping forever. When the OS silently flushes the helper-installed routes — ifscope bypass or server host route — every outbound socket bound to the physical interface fails with "network is unreachable", and the old waitForNetwork never recovered because it was waiting on the OS to fix routes the helper owns. After the budget exhausts the daemon stops the engine with "Connection lost"; the client's 2-second status poll triggers a fresh engine start, which re-runs the helper's createTUN and reinstalls all routes from scratch. Matches what you had to do manually before. Slow-poll entry also dumps `netstat -rn` to the daemon log so the next incident carries its own post-mortem.
