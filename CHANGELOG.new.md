## improvement
pprof endpoints on daemon for CPU profiling
Daemon now exposes /debug/pprof/* on its local 127.0.0.1:9090 API so we can capture CPU profiles to investigate Windows idle CPU burn. Curl /debug/pprof/profile?seconds=30 for a 30s CPU sample.

## improvement
ARQ send buffer early-out on empty state
SendBuffer.Expired and AckCumulative skip their loops when the buffer is empty, cutting a bit of 20Hz × N-streams overhead in the UDP retransmit loop.
