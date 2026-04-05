## improvement
Replace CUBIC with BBR-like rate-based congestion control for UDP transport
cwnd now tracks actual delivery rate instead of reacting to packet loss — eliminates death spiral on lossy ISP paths
