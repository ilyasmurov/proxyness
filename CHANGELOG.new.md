## feature
LIVE indicators on browser site tiles — real active-connection tracking
The tunnel now tracks every in-flight SOCKS5 connection by hostname and exposes a /tunnel/active-hosts endpoint. The client polls it every 2 seconds and lights up a pulsing LIVE label on every site tile whose primary domain or related CDN domains currently have open connections. Works across the YouTube + googlevideo + ytimg set and similar tracker families via the existing RELATED_DOMAINS table.

## fix
Browser sites counter shows "all sites" instead of total count when All browsers is on
Previously "11 enabled" was confusing when the wildcard was active. Now it reads "all sites" when the wildcard is on, and "N enabled" with the real count of explicitly selected sites otherwise.

## improvement
Daemon: per-host active-connection tracking in the tunnel
Simple concurrent map[string]int of in-flight SOCKS5 connections, incremented/decremented in handleSOCKS. Exposed via the new GET /tunnel/active-hosts endpoint.

## fix
Daemon deploys: server shutdown — tunnel now increments and decrements cleanly
handleSOCKS calls trackHost and untrackHost around the relay lifetime so the counter never leaks on errors.
