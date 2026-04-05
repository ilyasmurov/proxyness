## improvement
Add pacing + SACK-based loss recovery for UDP transport
Pacing spreads packet sends over time to avoid burst losses. SACK detection retransmits lost packets in ~1 RTT without cwnd reduction. Raised minCwnd 16→32.
