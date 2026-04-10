## fix
D3 stall detector false positives in Selected Apps mode
Stale entries in activeHosts map caused D3 to fire every ~5 min during normal idle browsing, killing 170+ SOCKS5 connections. Now uses GetActiveHosts() sweep instead of raw map length.

## feature
Beta badge in client header
Shows amber "BETA" badge next to version when running a beta build.
