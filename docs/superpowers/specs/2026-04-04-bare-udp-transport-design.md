# Bare UDP Transport Design

## Overview

Add UDP-based transport to SmurovProxy alongside existing TLS/TCP. UDP is the primary transport for lower latency and higher throughput; TLS/TCP remains as fallback for networks that block UDP. The protocol disguises itself as QUIC traffic on port 443 to resist DPI detection by RKN.

## Goals

- Reduce TTFB and increase throughput vs current TCP-over-TLS approach
- Resist DPI fingerprinting (no recognizable VPN patterns)
- Maintain all existing functionality: split tunneling, per-app rules, SOCKS5, TUN
- Forward secrecy via ephemeral key exchange
- Seamless fallback to TLS when UDP is blocked

## Architecture

### Dual Transport

Two transports, one protocol. The transport layer is swappable; everything above it (TUN engine, SOCKS5, per-app routing) is unchanged.

- **UDP transport (primary):** single multiplexed channel to server, all streams inside
- **TLS transport (fallback):** current implementation, no changes

### Transport Mode Selection

Client setting: **Auto** / **UDP** / **TLS**. Default: Auto.

- **Auto:** attempts UDP handshake; if no response within 3 seconds, falls back to TLS
- **UDP / TLS:** forced mode, no fallback
- UI displays current active transport ("UDP" or "TLS") next to connection status

### Server Port 443 — TCP + UDP

Server listens on port 443 for both TCP and UDP simultaneously (different OS-level protocols, no conflict).

- TCP: existing TLS + HTTP/proxy multiplexing, unchanged
- UDP: new `net.ListenPacket` on same port, handles QUIC-disguised protocol

## QUIC Disguise — Wire Format

Every UDP packet on the wire looks like QUIC traffic to DPI.

### Outer Header (unencrypted, visible to DPI)

```
[1 byte:  QUIC-like flags (0x40 | random bits)]
[4 bytes: Connection ID (session token)]
[24 bytes: Nonce (XChaCha20-Poly1305)]
[N bytes: Encrypted payload + 16-byte Poly1305 tag]
```

Connection ID allows the server to look up the session without decryption.

### Inner Payload (decrypted)

```
[1 byte:  Message type]
[4 bytes: Stream ID]
[4 bytes: Sequence number (TCP streams only, 0 for UDP/control)]
[2 bytes: Data length]
[N bytes: Data]
```

### MTU and Fragmentation

Max UDP payload should stay under 1400 bytes to avoid IP fragmentation (Ethernet MTU 1500 minus IP/UDP headers). Outer header overhead: 1 (flags) + 4 (conn ID) + 24 (nonce) + 16 (auth tag) = 45 bytes. Inner header: 1 + 4 + 4 + 2 = 11 bytes. **Max application data per packet: ~1344 bytes.**

For TCP streams with larger payloads, the transport layer chunks data into 1344-byte segments. Sequence numbers ensure correct reassembly.

### Handshake Connection ID

Handshake Request uses Connection ID = 0 (no session yet). Server identifies handshake packets by Connection ID 0 and attempts decryption with all active device keys. Handshake Response contains the assigned session token which becomes the Connection ID for all subsequent packets.

### Message Types

| Type | Name | Description |
|------|------|-------------|
| 0x01 | Handshake | Session establishment (ECDH + auth) |
| 0x02 | StreamOpen | Open new stream (TCP connection or UDP flow) |
| 0x03 | StreamData | Stream payload |
| 0x04 | StreamClose | Close stream, release resources |
| 0x05 | Keepalive | NAT keepalive, every 15 seconds |
| 0x06 | Ack | Delivery confirmation (TCP streams only) |

## Encryption

**Algorithm:** XChaCha20-Poly1305

- 256-bit key, 192-bit nonce (random per packet), 128-bit auth tag
- Go standard library: `golang.org/x/crypto/chacha20poly1305`
- Each packet encrypted independently — no state between packets
- Handshake packets encrypted with device key
- Data packets encrypted with session key (derived via ECDH)

## Session Establishment (Handshake)

One RTT for full handshake with forward secrecy.

```
Client                                     Server
  |                                          |
  |  Handshake Request (encrypted w/ device key):
  |  - Client ephemeral X25519 pubkey (32B)  |
  |  - HMAC auth message (41B)               |
  |  - Machine ID (16B)                      |
  |  ──────────────────────────────────────► |
  |                                          |  Validate HMAC
  |                                          |  Verify Machine ID
  |                                          |  Generate ephemeral keypair
  |                                          |
  |  Handshake Response (encrypted w/ device key):
  |  - Server ephemeral X25519 pubkey (32B)  |
  |  - Session token (4B = Connection ID)    |
  |  ◄────────────────────────────────────── |
  |                                          |
  |  Both sides: ECDH(client_eph, server_eph)|
  |  → HKDF-SHA256 → session key (32B)      |
  |  All subsequent packets use session key  |
```

**Forward secrecy:** ephemeral X25519 keypairs are generated per session and discarded after key derivation. Compromising the device key later cannot decrypt past sessions.

**Reconnection:** if UDP channel drops (WiFi change, NAT timeout), client performs a new handshake — one packet each way, near-instant.

## Stream Multiplexing

Single UDP channel, multiple streams inside. Each application TCP connection or UDP flow gets a unique 32-bit Stream ID.

### StreamOpen

```
[1 byte:  Stream type — TCP (0x01) or UDP (0x02)]
[Variable: Target address (addr_type + host + port, existing format)]
```

Server receives StreamOpen, establishes connection to target, responds with StreamData containing result (ok/fail).

### Reliability (TCP streams only)

TCP applications expect reliable, ordered delivery. For TCP-type streams:

- Sequence number on each StreamData packet
- Receiver sends Ack messages
- Sender retransmits after 2x RTT without Ack
- Ordering enforced per stream

UDP-type streams: fire and forget, no Ack, no ordering — matches UDP semantics.

### StreamClose

Either side sends StreamClose with Stream ID. Both sides release resources for that stream.

## Server Architecture

### Session Management

```go
type Session struct {
    token      uint32
    sessionKey [32]byte
    deviceID   int
    streams    map[uint32]*Stream
    clientAddr net.Addr    // updated on every packet
    lastSeen   time.Time
}
```

**Packet processing:**
1. Read UDP packet, extract Connection ID (4 bytes, no decryption needed)
2. Lookup session in `map[uint32]*Session`
3. No session → Handshake: decrypt with device keys, validate, create session
4. Has session → decrypt with session key, dispatch by Stream ID

**NAT roaming:** `clientAddr` is updated on every received packet. If client changes WiFi/IP, server automatically responds to the new address — no re-handshake needed.

**Timeouts:**
- Session: 2 minutes without any packet (keepalive every 15 sec prevents this)
- Stream: 60 seconds without data

## Client Architecture (Daemon)

### Transport Interface

New package `daemon/internal/transport/`:

```go
type Transport interface {
    Connect(server, key string) error
    OpenStream(msgType byte, addr string) (Stream, error)
    Close() error
    Mode() string  // "udp" or "tls"
}

type Stream interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Close() error
    ID() uint32
}
```

**Implementations:**
- `UDPTransport` — handshake, multiplexing, encryption
- `TLSTransport` — wrapper around current tunnel.go code
- `AutoTransport` — tries UDP, fallback to TLS after 3 seconds

### Integration

TUN engine and SOCKS5 tunnel call `transport.OpenStream()` instead of directly dialing TLS. Internal routing logic (per-app rules, DNS bypass, QUIC drop) unchanged.

### Daemon API

New endpoints:
- `POST /transport` — set mode: `{"mode": "auto|udp|tls"}`
- `GET /transport` — returns current mode and active transport

### Electron Client

- UI: transport indicator next to connection status ("UDP" / "TLS")
- Settings: transport mode selector (Auto / UDP / TLS)

## Module Layout

New and modified packages:

| Package | Description |
|---------|-------------|
| `pkg/proto/udptransport/` | Wire format, encryption, QUIC header, framing |
| `pkg/proto/handshake/` | ECDH key exchange, session key derivation |
| `daemon/internal/transport/` | Transport interface, UDP/TLS/Auto implementations |
| `server/internal/udp/` | UDP listener, session manager, stream dispatcher |
| `server/internal/mux/` | Minor: add UDP listener alongside TCP |
| `daemon/internal/tunnel/` | Refactor: use Transport interface |
| `daemon/internal/tun/` | Refactor: use Transport interface |

Existing packages (`pkg/auth/`, `pkg/proto/`, `server/internal/proxy/`) remain unchanged — the UDP transport reuses auth and proxy logic.
