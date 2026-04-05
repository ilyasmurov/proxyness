## fix
Reduce maxRetransmits 50→15 to cut head-of-line blocking on lossy paths
Rate-limit immediate ACKs (5ms min interval) to prevent ACK spam on out-of-order packets
Add 200ms time threshold for idle detection to prevent false STARTUP re-entry on brief pauses
Guard HandleData/HandleAck after Close() to prevent crash on freed structures
