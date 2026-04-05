## fix
Only signal cwnd loss on fresh packet loss, not re-retransmissions
Prevents cwnd from repeatedly crashing on same lost packet; maxRetransmits raised to 50 to prevent cumAck stall from dropped packets
