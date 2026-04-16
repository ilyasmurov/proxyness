# UDP Transport: Remove ARQ from Data Path

**Date:** 2026-04-16
**Status:** Approved

## Goal

Убрать ARQ (reliability layer) из data path UDP транспорта. Пакеты данных шифруются и отправляются без гарантии доставки — как WireGuard. TCP внутри туннеля сам обеспечивает надёжность через gVisor, UDP-трафик (игры, голос) по определению не нуждается в ретрансмите. Control messages (stream open/close) используют простой retry.

## Motivation

Текущий UDP транспорт реализует полный ARQ: per-packet sequence numbers, ACK generation (25ms tick), retransmit (50ms tick), BBR congestion control, per-stream reordering, send/recv buffers. Это ~1500 строк в `pkg/udp/arq/`.

Проблемы:
- **Throughput**: ARQ overhead + congestion control ограничивают скорость. Бенчмарк WireGuard 8.3 MB/s vs Proxyness 4.9 MB/s (TLS). UDP с ARQ не тестировался отдельно, но overhead аналогичен.
- **Latency**: Потерянный UDP-пакет (игра, голос) ретрансмитится вместо того чтобы быть пропущенным. Игре уже не нужен старый пакет — следующий несёт актуальное состояние.
- **Stability**: При потерях ARQ наращивает ретрансмиты → storm → drop-on-full → потеря данных. Наблюдалось 10× падение throughput на 50MB download.

WireGuard не использует ARQ. Потерялся пакет → TCP внутри туннеля сам попросит повтор. Для UDP-трафика потеря нормальна. Наша задача — шифровать и доставлять, не гарантировать.

## Architecture

### Data path (до)

```
App → gVisor → daemon → chunk 1340B → ARQ enqueue → congestion control →
  pacer → encrypt → UDP send → ... →
  server recv → decrypt → ARQ deliver (reorder) → WriteCh → destination
```

### Data path (после)

```
App → gVisor → daemon → chunk 1340B → encrypt → UDP send → ... →
  server recv → decrypt → find stream → WriteCh → destination
```

### Control path (stream open/close)

```
daemon: send MsgStreamOpen encrypted → wait ACK up to 1s → retry (max 3)
server: recv MsgStreamOpen → dial destination → send ACK (ok/fail)
```

## Changes

### Delete: `pkg/udp/arq/` (entire package)

Files to delete:
- `pkg/udp/arq/controller.go`
- `pkg/udp/arq/send_buffer.go`
- `pkg/udp/arq/recv_buffer.go`
- `pkg/udp/arq/congestion.go`
- `pkg/udp/arq/ack.go`
- `pkg/udp/arq/pacer.go`
- `pkg/udp/arq/rtt.go`
- `pkg/udp/arq/controller_test.go`

### Simplify: `pkg/udp/packet.go`

Remove `PktNum` field from packet header. New inner payload format:

```
[1 byte: Type]
[4 bytes: StreamID]
[2 bytes: DataLen]
[N bytes: Data]
```

Was 15 bytes header (Type + PktNum + StreamID + Seq + DataLen), becomes 7 bytes. Saves 8 bytes per packet overhead.

Message types stay the same:
- `MsgStreamOpen (0x02)` — control, uses retry
- `MsgStreamData (0x03)` — data, fire-and-forget
- `MsgStreamClose (0x04)` — control, uses retry
- `MsgKeepalive (0x05)` — fire-and-forget
- `MsgAck (0x06)` — ACK for control messages only
- `MsgSessionClose (0x07)` — fire-and-forget (best-effort)

### Modify: `daemon/internal/transport/udp.go`

**Remove:**
- `arq *arq.Controller` field from UDPTransport
- `retransmitLoop()` goroutine
- `ackLoop()` goroutine
- ARQ-related config (SendBufSize, RecvBufSize, MaxStreams)
- Drop-on-full logic (no longer needed without ARQ buffers)

**Simplify Write path:**
```go
func (s *udpStream) Write(p []byte) (int, error) {
    // Chunk into 1340-byte segments
    for offset := 0; offset < len(p); {
        end := offset + udpMaxPayload
        if end > len(p) { end = len(p) }
        chunk := p[offset:end]
        // Encode packet: Type=MsgStreamData, StreamID=s.id, Data=chunk
        pkt := encodeDataPacket(s.id, chunk)
        // Encrypt and send directly
        encrypted := encrypt(s.t.sessionKey, pkt)
        frame := encodeFrame(s.t.connID, encrypted)
        s.t.conn.Write(frame)
        offset = end
    }
    return len(p), nil
}
```

**Simplify Read path (recvLoop):**
```go
// In recvLoop, on MsgStreamData:
stream := t.streams[streamID]
if stream == nil { continue }
select {
case stream.recvCh <- data:
default:
    // Drop if consumer slow — same as current behavior but
    // without ARQ making it worse via retransmit storms
}
```

**Add control message retry:**
```go
func (t *UDPTransport) sendReliable(streamID uint32, msg []byte) error {
    for attempt := 0; attempt < 3; attempt++ {
        t.sendPacket(msg)
        select {
        case <-t.ackCh[streamID]:
            return nil
        case <-time.After(time.Second):
            continue
        case <-t.done:
            return errClosed
        }
    }
    return errTimeout
}
```

**OpenStream changes:**
- TCP streams: `sendReliable(MsgStreamOpen)` → wait for ACK with result byte
- UDP streams: send MsgStreamOpen, don't wait for ACK (destination may not "connect")

### Modify: `server/internal/udp/listener.go`

**Remove:**
- `arq *arq.Controller` from Session
- ARQ-related setup in handshake
- `deliverFn` callback (data goes directly to stream)

**Simplify packet processing:**
```go
// On MsgStreamData:
stream := session.streams[streamID]
if stream == nil { continue }
select {
case stream.WriteCh <- data:
default:
    // Drop — consumer slow, upper-layer TCP will retransmit
}
```

**Add ACK for control messages:**
- On MsgStreamOpen: dial destination, send ACK with result byte (0x01 ok / 0x00 fail)
- On MsgStreamClose: close stream, send ACK

### Modify: `daemon/internal/tun/engine.go`

No changes needed. `proxyUDPTransport()` and `handleUDP()` use the `transport.Stream` interface which stays the same (Read/Write/Close). The simplification is transparent to the engine.

## What Does NOT Change

- Handshake protocol (X25519 ECDH + HMAC auth + machine ID)
- Encryption (XChaCha20-Poly1305, random 24-byte nonce per packet)
- QUIC packet disguise (first byte 0x40 | random, 4-byte ConnID)
- Session management (token, streams map, cleanup timer)
- Keepalive (3s interval, 20s dead detection)
- Auto-fallback UDP → TLS (3s timeout in AutoTransport)
- TLS transport (completely untouched)
- Client transport mode selector (auto/udp/tls)
- Server TLS listener and mux

## Expected Impact

- **Throughput**: Remove ARQ overhead → closer to WireGuard. Congestion control no longer throttles; gVisor TCP does its own pacing.
- **Latency**: No retransmit delay on lost UDP packets. Games/voice see raw UDP latency.
- **Stability**: No retransmit storms. Drop-on-full is harmless (upper TCP retransmits, upper UDP doesn't care).
- **Code**: Delete ~1500 lines (ARQ package), simplify ~300 lines (transport + listener). Net reduction ~1200 lines.

## Risks

- **gVisor TCP retransmit timing**: Without ARQ, a lost packet means gVisor's TCP waits for its own RTO before retransmitting. gVisor's default RTO is 200ms min → 1s max. On lossy links this may feel slower than ARQ's 50ms retransmit for TCP-heavy workloads (browsing). Mitigation: monitor after deploy, tune gVisor RTO if needed.
- **Stream open reliability**: If all 3 retry attempts for MsgStreamOpen fail, the connection fails. On very lossy links (>50% loss) this could be noticeable. Acceptable — if 3 packets in 3 seconds can't get through, the link is unusable anyway.
- **Burst loss**: Without send pacing, daemon can burst-send many packets at once, causing switch/router buffer overflow. Mitigation: OS UDP send buffer + kernel pacing should handle this; if not, add simple token-bucket rate limiter later (not in this change).

## Benchmark Plan

After deploy, run `scripts/benchmark.sh` through UDP transport on both VPSes:
- Compare with WireGuard 8.3 MB/s baseline
- Compare with TLS transport
- Test on lossy link (tc netem on test VPS) to verify no retransmit storms
