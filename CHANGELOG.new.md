## feature
Config service + notifications system
Server-driven notifications, service discovery, and version updates via new smurov-config microservice. Landing page extracted to standalone container.

## fix
D3 stall detector false positives in Selected Apps mode
Stale activeHosts map entries caused D3 to kill SOCKS5 connections every ~5 min during idle browsing.
