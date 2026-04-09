## fix
TUN bridgeInbound — eliminate per-packet slice allocation
gVisor's buffer.MakeWithData copies the payload into its own pooled chunk, so the caller's slice is dead the instant InjectInbound returns. Pre-fix we made a fresh `[]byte` per inbound packet, generating one allocation per packet for GC to reap. Windows pprof showed ~70% CPU in runtime.gcDrain at idle. Now bridgeInbound reuses a single growing buffer across iterations (single-goroutine, no synchronization needed).
