# Raw UDP Bypass

Intercept UDP packets in `bridgeInbound` before gVisor injection. Parse IP+UDP headers, identify process, decide proxy/bypass. Bypass UDP flows through regular Go sockets, completely skipping gVisor. TCP remains through gVisor.

## Problem

gVisor userspace TCP/IP stack processes ALL traffic through TUN — even bypass traffic. On Windows this causes 3-5GB RAM and 10%+ CPU because gVisor auto-tunes TCP buffers and maintains full state machines for every connection. UDP bypass traffic (DNS, QUIC attempts, mDNS, telemetry, voice) is a large portion of system traffic.

## Packet Flow

```
Helper -> bridgeInbound -> parse IP header
                            |-- TCP packet -> gVisor (unchanged)
                            |-- UDP packet -> rawUDPHandler
                                               |-- port 443 -> drop (QUIC block)
                                               |-- port 53 -> bypass (DNS)
                                               |-- process lookup -> should proxy? -> gVisor
                                               |-- voice app + port >= 50000 -> bypass
                                               |-- not proxy -> bypass
                                                                  |
                                                    NAT table: remember (srcIP:srcPort, dstIP:dstPort)
                                                    protectedDial("udp", dst) -> send payload
                                                    response from dst -> build IP+UDP packet -> helper -> TUN
```

## Components

### 1. Packet parser (`daemon/internal/tun/packet.go`)

Parse IP and UDP headers from raw `[]byte` without allocations.

Exported functions:
- `ParseIPHeader(pkt []byte) (proto uint8, srcIP, dstIP net.IP, headerLen int, err error)` — returns protocol number, IPs, and header length
- `ParseUDPHeader(pkt []byte) (srcPort, dstPort uint16, payload []byte, err error)` — parses UDP header after IP header
- `BuildUDPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte` — constructs a full IP+UDP packet with checksums for TUN injection

### 2. NAT table (`daemon/internal/tun/nat.go`)

Maps inbound UDP flows to outbound Go sockets.

```go
type natKey struct {
    srcIP   [4]byte
    dstIP   [4]byte
    srcPort uint16
    dstPort uint16
}

type natEntry struct {
    conn     net.Conn       // protectedDial UDP socket
    lastSeen time.Time
}

type NATTable struct {
    mu      sync.RWMutex
    entries map[natKey]*natEntry
    onReply func(pkt []byte)  // callback to write response packet to helper/TUN
}
```

- `HandlePacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) error` — looks up or creates NAT entry, sends payload through socket
- On first packet for a flow: `protectedDial("udp", dst)`, start read goroutine
- Read goroutine: receives response, calls `BuildUDPPacket` with swapped addresses, calls `onReply`
- Cleanup goroutine: runs every 10 seconds, closes entries older than timeout
- Timeouts: 60s default, 120s for voice ports (>= 50000)

### 3. Raw UDP handler (`daemon/internal/tun/rawudp.go`)

Decision engine for UDP packets. Called from `bridgeInbound`.

```go
type RawUDPHandler struct {
    nat      *NATTable
    rules    *Rules
    procInfo ProcessInfo
    selfPath string
}
```

- `Handle(pkt []byte) bool` — returns true if packet was handled (bypass), false if should go to gVisor (proxy)
- Logic:
  1. Parse IP+UDP headers
  2. Drop UDP port 443 (QUIC block) -> return true
  3. Port 53 (DNS) -> bypass always -> return true
  4. Process lookup by srcPort
  5. Check `rules.ShouldProxy(appPath)` — if false -> bypass -> return true
  6. If shouldProxy but `isVoiceApp(appPath)` and dstPort >= 50000 -> bypass -> return true
  7. Otherwise -> return false (let gVisor handle for proxy)
- Bypass: call `nat.HandlePacket(...)` to relay through real UDP socket

### 4. Changes to bridgeInbound (`engine.go`)

Before `ep.InjectInbound()`, check if UDP:

```go
func (e *Engine) bridgeInbound(conn net.Conn, ep *channel.Endpoint) {
    for {
        // ... read packet from helper ...

        // Check if raw UDP handler can process this
        if e.rawUDP != nil && e.rawUDP.Handle(data) {
            continue // bypass — don't inject into gVisor
        }

        // Inject into gVisor (TCP, or proxy UDP)
        ep.InjectInbound(...)
    }
}
```

### 5. Response path (NAT -> TUN)

NAT read goroutine receives UDP response from destination. Builds response packet:

- IP header: version=4, IHL=5, protocol=17(UDP), TTL=64
  - src = original dstIP (the server responding)
  - dst = original srcIP (the app expecting the response)
- UDP header: srcPort = original dstPort, dstPort = original srcPort
- IP checksum (standard RFC 1071)
- UDP checksum (pseudo-header + UDP header + payload)
- Write as length-prefixed packet to helper connection (same format as gVisor outbound)

## What Does NOT Change

- All TCP: remains through gVisor (with 128KB buffer cap from v1.16.5)
- Proxy UDP: remains through gVisor (only selected apps)
- Helper: no changes (reads/writes TUN + length-prefixed IPC, unchanged)
- SOCKS5 tunnel: not affected
- Client (Electron): not affected
- Server: not affected

## Files

| File | Action |
|------|--------|
| `daemon/internal/tun/packet.go` | Create: IP+UDP parser and packet builder |
| `daemon/internal/tun/packet_test.go` | Create: tests for parser and builder |
| `daemon/internal/tun/nat.go` | Create: NAT table with timeout cleanup |
| `daemon/internal/tun/nat_test.go` | Create: NAT table tests |
| `daemon/internal/tun/rawudp.go` | Create: raw UDP handler with routing logic |
| `daemon/internal/tun/rawudp_test.go` | Create: handler routing tests |
| `daemon/internal/tun/engine.go` | Modify: init rawUDP handler, hook into bridgeInbound |
