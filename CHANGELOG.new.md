## improvement
Stabilize UDP throughput by capping cwnd and reducing loss aggressiveness
maxCwnd 1024→256 prevents overshooting channel capacity. cubicBeta 0.8→0.9 reduces cwnd cut per loss from 20% to 10%. Eliminates boom-bust oscillation (842→64).
