## fix
RTT sampling, sender-side dup ACK, single OnLoss per tick in ARQ

Sample RTT using Karn's algorithm, track sender-side duplicate ACKs for fast retransmit, call OnLoss/Backoff once per retransmit tick, fix goroutine leak on session cleanup, remove dead fields.
