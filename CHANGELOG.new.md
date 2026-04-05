## fix
Use cwnd/RTT pacing during startup instead of BWE-based pacing
Prevents BWE-pacing feedback loop that throttled upload to 0.1 MB/s
