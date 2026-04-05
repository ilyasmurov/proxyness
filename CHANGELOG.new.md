## fix
Cap UDP burst size to prevent ISP buffer overflow
Uncapped burst of 17 packets at high cwnd overwhelmed ISP buffers causing congestion collapse (40 KB/s). Capped at 8 packets per burst.
