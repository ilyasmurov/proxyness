## fix
Windows daemon CPU — rate-limit procinfo cache miss refreshes
The Windows process-by-port lookup was forcing a full GetExtendedTcpTable/GetExtendedUdpTable scan on every cache miss. With browsers cycling ephemeral UDP source ports for DNS and other lookups, this thrashed the kernel scan. v1.28.7 pprof showed ~13% CPU still in refreshUDP. Now miss-driven refreshes are capped at one per 250ms per network — periodic 2s refresh still keeps the cache fresh, but new unknown ports return "unknown app" briefly instead of triggering an unbounded scan storm.
