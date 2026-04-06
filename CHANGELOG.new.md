## fix
Reduce CPU usage on Windows — retransmit interval 10ms→50ms, process table cache 500ms→2s
SendBuffer.Expired() now reuses buffer to avoid allocations on hot path.

## fix
Reduce log spam in bridgeInbound — log every 10000 packets instead of 100
