## improvement
Burst-based pacing to bypass macOS 1ms sleep granularity
Sub-ms pacing intervals now batch into bursts instead of sleeping per-packet, fixing throughput cap at ~1.3 MB/s.
