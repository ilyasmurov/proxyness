## fix
Windows daemon idle CPU burn — cache physical interface index
Windows CachePhysicalInterface was a no-op stub, so every protectedDial was calling net.Interfaces → GetAdaptersAddresses per connection. A pprof capture of an idle TUN-mode daemon showed ~28% CPU in that path plus another ~35% in GC cleaning up the allocations — a busy browser was burning 40-60% of one core on nothing but interface enumeration. Now mirrors the darwin implementation: detect once at engine.Start, cache, clear on Stop.
