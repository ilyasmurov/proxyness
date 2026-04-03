# UDP ARQ Reliability Layer

**Date:** 2026-04-04
**Status:** Approved
**Goal:** Add reliability to the existing UDP transport so bulk transfers work at maximum performance while preserving low TTFB for small requests.

## Problem

The UDP transport sends packets fire-and-forget. `MsgAck` (0x06) is defined but unused. `Seq` is incremented on daemon but ignored on server. `relayFromDest` doesn't set Seq at all.

Result: any packet loss (kernel buffer overrun under load, network drop) silently corrupts TCP streams. Bulk downloads fail at ~600 KB, uploads at ~9 MB. Small requests (TTFB) work great — 40-50% faster than TLS.

## Approach

Connection-level ARQ (QUIC-style) built into the existing protocol:

- Connection-level packet numbers (`PktNum`) for ACK tracking, RTT estimation, congestion control
- Per-stream sequence numbers (`Seq`) for data reordering
- Selective ACK with 256-bit bitmap
- CUBIC congestion control (one window per connection)
- Fast retransmit on 3 duplicate ACKs
- RTO-based retransmit with exponential backoff

Preserves: QUIC-disguise, XChaCha20-Poly1305 encryption, stream multiplexing. No external dependencies.

## Wire Format

### Packet (updated)

Inner payload changes from 11 to 15 bytes:

```
Before: Type(1) + StreamID(4) + Seq(4) + DataLen(2) + Data(N)
After:  Type(1) + PktNum(4) + StreamID(4) + Seq(4) + DataLen(2) + Data(N)
```

`Packet` struct gains a `PktNum uint32` field. `PktNum=0` means not tracked (used by MsgAck, MsgKeepalive, MsgHandshake).

`udpMaxPayload` changes from 1344 to 1340 bytes.

### MsgAck Payload (0x06)

```
CumAck(4) + Bitmap(32) = 36 bytes
```

- `CumAck`: highest N such that all packets 1..N are received
- `Bitmap`: 256 bits, bit i = packet (CumAck + 1 + i) received

Sent as a standard Packet with `PktNum=0, StreamID=0, Seq=0, Type=MsgAck`.

### Reliability Scope by Message Type

| Type | PktNum | Retransmit |
|---|---|---|
| MsgHandshake (0x01) | 0 | No (own retry via reconnect) |
| MsgStreamOpen (0x02) | Yes | Yes |
| MsgStreamData (0x03) | Yes | Yes |
| MsgStreamClose (0x04) | Yes | Yes |
| MsgKeepalive (0x05) | 0 | No |
| MsgAck (0x06) | 0 | No |

## Architecture: `pkg/udp/arq/`

Shared package used by both daemon and server. One `Controller` per session.

### Controller

```go
type Controller struct {
    // Sending
    sendBuf    *SendBuffer
    nextPktNum atomic.Uint32
    cwnd       *CongestionControl
    rtt        *RTTEstimator

    // Receiving
    recvBufs   map[uint32]*RecvBuffer  // per-stream reorder buffers
    ackState   *AckState

    // I/O callbacks
    sendFn     func([]byte) error       // send raw UDP datagram
    deliverFn  func(streamID uint32, data []byte)  // deliver in-order data to stream

    mu         sync.Mutex
    closed     bool
    done       chan struct{}
}
```

### Lifecycle

1. Created after handshake with `sendFn` and `deliverFn` callbacks
2. `Send(streamID, seq, data)` — assigns PktNum, waits for cwnd slot, calls sendFn, stores in sendBuf
3. `HandleData(pkt)` — inserts in recvBufs[streamID], delivers consecutive packets via deliverFn, schedules ACK
4. `HandleAck(data)` — updates sendBuf (marks acked), updates RTT, advances cwnd
5. `RetransmitTick()` — called every 10ms, scans sendBuf for RTO-expired packets
6. `AckTick()` — called every 25ms, sends pending delayed ACK
7. `Close()` — stops goroutines, cleans buffers

### Files

- `controller.go` — Controller, Send/HandleData/HandleAck
- `send_buffer.go` — SentPacket storage, retransmit tracking
- `recv_buffer.go` — per-stream reorder buffer
- `congestion.go` — CUBIC congestion control
- `rtt.go` — RTT estimation (Jacobson/Karels)
- `ack.go` — ACK encode/decode, AckState, delayed ACK logic

## Send Buffer

```go
type SentPacket struct {
    PktNum      uint32
    RawData     []byte     // encoded datagram for retransmit
    SentAt      time.Time  // first send (for RTT; only if not retransmitted)
    LastSentAt  time.Time  // last send (for RTO check)
    Retransmits int
    Acked       bool
}

type SendBuffer struct {
    mu      sync.Mutex
    packets map[uint32]*SentPacket
    minPkt  uint32  // lowest unacked PktNum
}
```

### Retransmission Triggers

1. **RTO timeout**: `RetransmitTick()` scans from minPkt — if `time.Since(LastSentAt) > rto` and not acked → retransmit with **new PktNum** (Karn's algorithm). Old PktNum marked acked. New SentPacket has `SentAt` zeroed (RTT not measured from retransmitted packets). RTO doubled (exponential backoff).

2. **Fast retransmit**: 3 duplicate ACKs (CumAck doesn't advance) → immediate retransmit of first unacked packet. CUBIC multiplicative decrease applied.

### Limits

- Max retransmits per packet: 10 → stream closed with error
- Send buffer capacity: 1024 packets (~1.3 MB) → `Send()` blocks when full (backpressure)
- On ACK with CumAck=N: all packets ≤ N removed. Bitmap-acked packets marked but not removed until CumAck reaches them.

## Receive Buffer

Per-stream reorder buffer:

```go
type RecvBuffer struct {
    mu       sync.Mutex
    expected uint32              // next expected Seq
    buffer   map[uint32][]byte   // out-of-order: Seq → data
    maxBuf   int                 // 512
}
```

### Logic

- `Seq == expected` → deliver, increment expected, flush consecutive buffered packets
- `Seq > expected` → buffer (out-of-order). Drop if buffer > maxBuf
- `Seq < expected` → duplicate, ignore

### AckState (connection-level)

```go
type AckState struct {
    mu          sync.Mutex
    received    map[uint32]bool
    cumAck      uint32
    pendingAck  bool
    dupCount    int
    pktsSinceAck int
}
```

- Every incoming PktNum > 0: add to received, try to advance cumAck
- When cumAck advances: remove all entries ≤ cumAck from `received` map (prevent unbounded growth)
- Gap detected (PktNum != cumAck+1): send ACK **immediately** (triggers fast retransmit on sender)
- No gap: delayed ACK — send after 25ms or after 2 packets

## Congestion Control (CUBIC)

```go
type CongestionControl struct {
    mu        sync.Mutex
    cwnd      float64
    ssthresh  float64
    wMax      float64
    lastLoss  time.Time
    inFlight  int

    beta float64  // 0.7
    c    float64  // 0.4
}
```

### Phases

1. **Slow start** (cwnd < ssthresh): cwnd += 1 per ACKed packet. Exponential growth. Initial cwnd = 10 (RFC 6928).

2. **Congestion avoidance** (cwnd ≥ ssthresh): CUBIC function:
   ```
   K = ∛(wMax * (1 - beta) / C)
   W(t) = C * (t - K)³ + wMax
   cwnd = max(W(t), cwnd)  // never decrease in this phase
   ```

3. **On loss** (RTO or fast retransmit):
   ```
   wMax = cwnd
   ssthresh = cwnd * beta
   cwnd = ssthresh
   lastLoss = now
   ```

### Backpressure

`Send()` blocks when `inFlight >= int(cwnd)`. Unblocks when ACK arrives and `inFlight` decreases. Implemented via `sync.Cond`.

## RTT Estimation

```go
type RTTEstimator struct {
    mu     sync.Mutex
    srtt   time.Duration
    rttvar time.Duration
    rto    time.Duration
    init   bool
}
```

### Update (Jacobson/Karels, only for non-retransmitted packets)

First sample:
```
srtt = sample
rttvar = sample / 2
rto = srtt + 4*rttvar
```

Subsequent:
```
rttvar = 0.75*rttvar + 0.25*|srtt - sample|
srtt = 0.875*srtt + 0.125*sample
rto = clamp(srtt + 4*rttvar, 100ms, 2000ms)
```

Expected for Russia→NL path: RTT ~50-80ms, RTO ~200-400ms.

## Integration: Daemon

```
UDPTransport
  ├── conn *net.UDPConn
  ├── arq  *arq.Controller        // NEW
  ├── streams map[uint32]*udpStream
  └── goroutines:
        ├── recvLoop()              // modified
        ├── keepaliveLoop()         // unchanged
        ├── retransmitLoop()        // NEW
        └── ackLoop()               // NEW
```

### Changes

**`Connect()`**: after handshake, create `arq.Controller` with:
- `sendFn`: encode packet → `conn.Write()`
- `deliverFn`: dispatch to `stream.recvCh`
- Start `retransmitLoop()` and `ackLoop()` goroutines

**`recvLoop()`**:
- `MsgStreamData` → `arq.HandleData(pkt)` (instead of direct recvCh write)
- `MsgAck` → `arq.HandleAck(pkt.Data)` (new case)
- `MsgStreamClose` → unchanged

**`udpStream.Write()`**:
- `arq.Send(streamID, seq, data)` instead of direct `sendPacket()`. Blocks on cwnd.

**`retransmitLoop()`**: every 10ms → `arq.RetransmitTick()`

**`ackLoop()`**: every 25ms → `arq.AckTick()`

## Integration: Server

```
Session
  ├── arq     *arq.Controller      // NEW
  ├── streams map[uint32]*StreamState
  └── goroutines (per session):
        ├── retransmitLoop()         // NEW
        └── ackLoop()                // NEW
```

### Changes

**`handleHandshake()`**: after session creation:
- Create `arq.Controller` with:
  - `sendFn`: encode → `l.conn.WriteTo(data, sess.ClientAddr)`
  - `deliverFn`: looks up stream by streamID → writes to `st.Conn` (destination)
- Start `retransmitLoop()` and `ackLoop()` goroutines

**`processLoop()`**: add `case MsgAck → sess.arq.HandleAck(pkt.Data)`

**`handleStreamData()`**: `arq.HandleData(pkt)` instead of direct `st.Conn.Write()`

**`relayFromDest()`**: `arq.Send(streamID, seq, data)` instead of direct encode+WriteTo. Blocks on cwnd.

**Session cleanup**: `sess.arq.Close()` stops goroutines, cleans buffers.

### Thread Safety

- Multiple `relayFromDest` goroutines call `arq.Send()` concurrently → `Controller.Send()` is mutex-protected (PktNum allocation + cwnd check)
- `processLoop` is single goroutine → `HandleAck()`/`HandleData()` have no contention
- `sendFn` (`WriteTo`) is thread-safe on `PacketConn`

## Key Parameters

| Parameter | Value | Rationale |
|---|---|---|
| Initial cwnd | 10 packets | RFC 6928 |
| Max cwnd | 1024 packets (~1.3 MB) | Covers channel BDP |
| CUBIC beta | 0.7 | CUBIC standard |
| CUBIC C | 0.4 | CUBIC standard |
| Min RTO | 100ms | Floor above typical RTT |
| Max RTO | 2000ms | Upper bound |
| Fast retransmit | 3 dup ACKs | TCP standard |
| Max retransmits | 10 | Then stream error |
| Delayed ACK | 25ms or 2 packets | Latency/overhead balance |
| Send buffer | 1024 packets | Backpressure limit |
| Recv reorder buffer | 512 packets/stream | OOM protection |
| Retransmit tick | 10ms | RTO check granularity |
| ACK tick | 25ms | Delayed ACK interval |
| Max payload | 1340 bytes | 1344 - 4 (PktNum field) |
