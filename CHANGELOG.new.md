## fix
TUN bridgeOutbound — eliminate per-packet Flatten() allocation
gVisor's Buffer.Flatten() does an unconditional `make([]byte, 0, size)` internally, so calling it per outbound packet was generating one fresh slice per packet on the GC heap. Mirrors the bridgeInbound fix from 1.28.9: reuse a single growing frame buffer (length prefix + payload) across iterations and copy via ReadAt straight into it. Bonus: also coalesces the two conn.Write calls (length, then data) into a single Write — saves a syscall per packet on Windows.
