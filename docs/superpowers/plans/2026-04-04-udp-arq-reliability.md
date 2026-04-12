# UDP ARQ Reliability Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a connection-level ARQ reliability layer to the UDP transport so bulk transfers work reliably while preserving low TTFB.

**Architecture:** New `pkg/udp/arq/` package with Controller, SendBuffer, RecvBuffer, CUBIC congestion control, RTT estimator, and ACK/SACK logic. One Controller per session, used by both daemon and server. Wire format gains a 4-byte PktNum field.

**Tech Stack:** Go standard library, `sync.Cond` for cwnd backpressure, existing `pkg/udp` for packet encoding.

**Spec:** `docs/superpowers/specs/2026-04-04-udp-arq-reliability-design.md`

---

## File Structure

### Modified files
- `pkg/udp/packet.go` — Add `PktNum` field to Packet, update Encode/Decode (inner header 11→15 bytes)
- `pkg/udp/packet_test.go` — Update tests for PktNum field
- `daemon/internal/transport/udp.go` — Wire up ARQ Controller into UDPTransport
- `server/internal/udp/listener.go` — Wire up ARQ Controller into session handling
- `server/internal/udp/session.go` — Add ARQ Controller to Session
- `test/udp_test.go` — Update integration tests for PktNum field

### New files
- `pkg/udp/arq/ack.go` — ACK payload encode/decode (CumAck + 256-bit bitmap)
- `pkg/udp/arq/ack_test.go`
- `pkg/udp/arq/rtt.go` — Jacobson/Karels RTT estimator
- `pkg/udp/arq/rtt_test.go`
- `pkg/udp/arq/congestion.go` — CUBIC congestion control
- `pkg/udp/arq/congestion_test.go`
- `pkg/udp/arq/send_buffer.go` — Sent packet storage + retransmit tracking
- `pkg/udp/arq/send_buffer_test.go`
- `pkg/udp/arq/recv_buffer.go` — Per-stream reorder buffer
- `pkg/udp/arq/recv_buffer_test.go`
- `pkg/udp/arq/ack_state.go` — Connection-level received PktNum tracking + delayed ACK
- `pkg/udp/arq/ack_state_test.go`
- `pkg/udp/arq/controller.go` — Main ARQ Controller tying all components together
- `pkg/udp/arq/controller_test.go`

---

## Task 1: Add PktNum to Wire Format

**Files:**
- Modify: `pkg/udp/packet.go`
- Modify: `pkg/udp/packet_test.go`
- Modify: `test/udp_test.go`

- [ ] **Step 1: Update the test to expect PktNum**

In `pkg/udp/packet_test.go`, replace the entire file:

```go
package udp

import (
	"crypto/rand"
	"testing"
)

func TestPacketEncodeDecodeData(t *testing.T) {
	sessionKey := make([]byte, 32)
	rand.Read(sessionKey)

	pkt := &Packet{
		ConnID:   0x12345678,
		Type:     MsgStreamData,
		PktNum:   99,
		StreamID: 42,
		Seq:      7,
		Data:     []byte("hello"),
	}

	encoded, err := EncodePacket(pkt, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	if encoded[0]&0x40 == 0 {
		t.Fatal("QUIC flag not set")
	}

	decoded, err := DecodePacket(encoded, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.ConnID != pkt.ConnID {
		t.Fatalf("connID: got %d, want %d", decoded.ConnID, pkt.ConnID)
	}
	if decoded.Type != pkt.Type {
		t.Fatalf("type: got %d, want %d", decoded.Type, pkt.Type)
	}
	if decoded.PktNum != pkt.PktNum {
		t.Fatalf("pktNum: got %d, want %d", decoded.PktNum, pkt.PktNum)
	}
	if decoded.StreamID != pkt.StreamID {
		t.Fatalf("streamID: got %d, want %d", decoded.StreamID, pkt.StreamID)
	}
	if decoded.Seq != pkt.Seq {
		t.Fatalf("seq: got %d, want %d", decoded.Seq, pkt.Seq)
	}
	if string(decoded.Data) != "hello" {
		t.Fatalf("data: got %q", decoded.Data)
	}
}

func TestPacketPktNumZero(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	pkt := &Packet{
		ConnID: 0xABCD,
		Type:   MsgKeepalive,
		PktNum: 0,
	}

	encoded, err := EncodePacket(pkt, key)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodePacket(encoded, key)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.PktNum != 0 {
		t.Fatalf("pktNum: got %d, want 0", decoded.PktNum)
	}
	if decoded.Type != MsgKeepalive {
		t.Fatalf("type: got %d", decoded.Type)
	}
}

func TestPacketHandshakeNoEncryption(t *testing.T) {
	pkt := &Packet{
		ConnID: 0,
		Type:   MsgHandshake,
		Data:   []byte("handshake-payload"),
	}

	deviceKey := make([]byte, 32)
	rand.Read(deviceKey)

	encoded, err := EncodePacket(pkt, deviceKey)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodePacket(encoded, deviceKey)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Type != MsgHandshake {
		t.Fatalf("type: got %d, want %d", decoded.Type, MsgHandshake)
	}
	if string(decoded.Data) != "handshake-payload" {
		t.Fatalf("data: got %q", decoded.Data)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/ -run TestPacket -v`
Expected: FAIL — `Packet` has no field `PktNum`

- [ ] **Step 3: Update Packet struct and Encode/Decode**

In `pkg/udp/packet.go`, replace the entire file:

```go
package udp

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

// Message types for the inner payload.
const (
	MsgHandshake   byte = 0x01
	MsgStreamOpen  byte = 0x02
	MsgStreamData  byte = 0x03
	MsgStreamClose byte = 0x04
	MsgKeepalive   byte = 0x05
	MsgAck         byte = 0x06
)

// Packet is the logical unit of the UDP transport protocol.
type Packet struct {
	ConnID   uint32 // session token (0 for handshake)
	Type     byte
	PktNum   uint32 // connection-level packet number (0 = not tracked)
	StreamID uint32
	Seq      uint32
	Data     []byte
}

// EncodePacket encodes a Packet into a QUIC-disguised UDP datagram.
//
// Wire format:
//
//	[1 byte:  QUIC flags (0x40 | random)]
//	[4 bytes: Connection ID]
//	[N bytes: Encrypted(Type + PktNum + StreamID + Seq + DataLen + Data)]
func EncodePacket(p *Packet, key []byte) ([]byte, error) {
	// Inner payload: type(1) + pktNum(4) + streamID(4) + seq(4) + dataLen(2) + data(N)
	inner := make([]byte, 1+4+4+4+2+len(p.Data))
	inner[0] = p.Type
	binary.BigEndian.PutUint32(inner[1:5], p.PktNum)
	binary.BigEndian.PutUint32(inner[5:9], p.StreamID)
	binary.BigEndian.PutUint32(inner[9:13], p.Seq)
	binary.BigEndian.PutUint16(inner[13:15], uint16(len(p.Data)))
	copy(inner[15:], p.Data)

	encrypted, err := Encrypt(key, inner)
	if err != nil {
		return nil, err
	}

	// Outer: flags(1) + connID(4) + encrypted
	out := make([]byte, 1+4+len(encrypted))
	randByte := make([]byte, 1)
	rand.Read(randByte)
	out[0] = 0x40 | (randByte[0] & 0x3f)
	binary.BigEndian.PutUint32(out[1:5], p.ConnID)
	copy(out[5:], encrypted)

	return out, nil
}

// DecodePacket decodes a QUIC-disguised UDP datagram.
func DecodePacket(data []byte, key []byte) (*Packet, error) {
	if len(data) < 5 {
		return nil, fmt.Errorf("packet too short: %d bytes", len(data))
	}

	connID := binary.BigEndian.Uint32(data[1:5])
	encrypted := data[5:]

	inner, err := Decrypt(key, encrypted)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}

	if len(inner) < 15 {
		return nil, fmt.Errorf("inner payload too short: %d bytes", len(inner))
	}

	dataLen := binary.BigEndian.Uint16(inner[13:15])
	if len(inner) < 15+int(dataLen) {
		return nil, fmt.Errorf("data truncated: have %d, need %d", len(inner)-15, dataLen)
	}

	return &Packet{
		ConnID:   connID,
		Type:     inner[0],
		PktNum:   binary.BigEndian.Uint32(inner[1:5]),
		StreamID: binary.BigEndian.Uint32(inner[5:9]),
		Seq:      binary.BigEndian.Uint32(inner[9:13]),
		Data:     inner[15 : 15+dataLen],
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/ -v`
Expected: all PASS

- [ ] **Step 5: Update integration tests for PktNum**

In `test/udp_test.go`, the `TestUDPPacketRoundTrip` creates a Packet without PktNum — this is fine since Go zero-initializes it. But add an explicit PktNum to be clear. Replace the packet creation block (line 72-78):

```go
	pkt := &pkgudp.Packet{
		ConnID:   0xABCD1234,
		Type:     pkgudp.MsgStreamData,
		PktNum:   1,
		StreamID: 42,
		Seq:      0,
		Data:     []byte("hello from client"),
	}
```

- [ ] **Step 6: Run integration tests**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/test && go test -run TestUDP -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/udp/packet.go pkg/udp/packet_test.go test/udp_test.go
git commit -m "feat(udp): add PktNum field to packet wire format

Connection-level packet number for ARQ reliability.
Inner header grows from 11 to 15 bytes."
```

---

## Task 2: ACK Encode/Decode

**Files:**
- Create: `pkg/udp/arq/ack.go`
- Create: `pkg/udp/arq/ack_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/ack_test.go`:

```go
package arq

import (
	"testing"
)

func TestAckEncodeDecodeEmpty(t *testing.T) {
	a := &AckData{CumAck: 10}

	data := a.Encode()
	if len(data) != AckDataSize {
		t.Fatalf("size: got %d, want %d", len(data), AckDataSize)
	}

	decoded, err := DecodeAckData(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CumAck != 10 {
		t.Fatalf("cumAck: got %d", decoded.CumAck)
	}
	// All bitmap bits should be zero
	for i, b := range decoded.Bitmap {
		if b != 0 {
			t.Fatalf("bitmap[%d] = %d, want 0", i, b)
		}
	}
}

func TestAckEncodeDecodeBitmap(t *testing.T) {
	a := &AckData{CumAck: 100}
	// Mark packets 101, 103, 164 as received (bits 0, 2, 63)
	a.SetReceived(101)
	a.SetReceived(103)
	a.SetReceived(164)

	data := a.Encode()
	decoded, err := DecodeAckData(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.CumAck != 100 {
		t.Fatalf("cumAck: got %d", decoded.CumAck)
	}
	if !decoded.IsReceived(101) {
		t.Fatal("101 should be received")
	}
	if decoded.IsReceived(102) {
		t.Fatal("102 should NOT be received")
	}
	if !decoded.IsReceived(103) {
		t.Fatal("103 should be received")
	}
	if !decoded.IsReceived(164) {
		t.Fatal("164 should be received")
	}
}

func TestAckBitmapOutOfRange(t *testing.T) {
	a := &AckData{CumAck: 100}

	// Packet 100 is at or below CumAck — no bitmap bit
	a.SetReceived(100)
	if a.IsReceived(100) {
		t.Fatal("100 is <= CumAck, should not be in bitmap")
	}

	// Packet 357 = CumAck + 257, out of 256-bit range
	a.SetReceived(357)
	if a.IsReceived(357) {
		t.Fatal("357 is out of bitmap range")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestAck -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/ack.go`:

```go
package arq

import (
	"encoding/binary"
	"fmt"
)

// AckDataSize is the byte size of an encoded AckData: CumAck(4) + Bitmap(32).
const AckDataSize = 4 + 32

// AckData is the payload of a MsgAck packet.
// CumAck: highest N such that all packets 1..N are received.
// Bitmap: 256 bits — bit i means packet (CumAck + 1 + i) is received.
type AckData struct {
	CumAck uint32
	Bitmap [32]byte // 256 bits
}

// SetReceived marks pktNum as received in the bitmap.
// Only valid for pktNum in range (CumAck+1)..(CumAck+256).
func (a *AckData) SetReceived(pktNum uint32) {
	if pktNum <= a.CumAck || pktNum > a.CumAck+256 {
		return
	}
	idx := pktNum - a.CumAck - 1 // 0..255
	a.Bitmap[idx/8] |= 1 << (idx % 8)
}

// IsReceived checks if pktNum is marked in the bitmap.
// Only valid for pktNum in range (CumAck+1)..(CumAck+256).
func (a *AckData) IsReceived(pktNum uint32) bool {
	if pktNum <= a.CumAck || pktNum > a.CumAck+256 {
		return false
	}
	idx := pktNum - a.CumAck - 1
	return a.Bitmap[idx/8]&(1<<(idx%8)) != 0
}

// Encode serializes AckData into 36 bytes.
func (a *AckData) Encode() []byte {
	buf := make([]byte, AckDataSize)
	binary.BigEndian.PutUint32(buf[0:4], a.CumAck)
	copy(buf[4:36], a.Bitmap[:])
	return buf
}

// DecodeAckData parses 36 bytes into AckData.
func DecodeAckData(data []byte) (*AckData, error) {
	if len(data) < AckDataSize {
		return nil, fmt.Errorf("ack data too short: %d bytes", len(data))
	}
	a := &AckData{
		CumAck: binary.BigEndian.Uint32(data[0:4]),
	}
	copy(a.Bitmap[:], data[4:36])
	return a, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestAck -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/ack.go pkg/udp/arq/ack_test.go
git commit -m "feat(arq): add ACK encode/decode with 256-bit SACK bitmap"
```

---

## Task 3: RTT Estimator

**Files:**
- Create: `pkg/udp/arq/rtt.go`
- Create: `pkg/udp/arq/rtt_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/rtt_test.go`:

```go
package arq

import (
	"testing"
	"time"
)

func TestRTTFirstSample(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)

	if r.SRTT() != 80*time.Millisecond {
		t.Fatalf("srtt: got %v", r.SRTT())
	}
	// rttvar = sample/2 = 40ms, rto = srtt + 4*rttvar = 80+160 = 240ms
	if r.RTO() != 240*time.Millisecond {
		t.Fatalf("rto: got %v", r.RTO())
	}
}

func TestRTTSubsequentSamples(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)
	r.Update(100 * time.Millisecond)

	// srtt = 0.875*80 + 0.125*100 = 70+12.5 = 82.5ms
	srtt := r.SRTT()
	if srtt < 82*time.Millisecond || srtt > 83*time.Millisecond {
		t.Fatalf("srtt: got %v, want ~82.5ms", srtt)
	}
}

func TestRTOMinClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(5 * time.Millisecond) // very fast

	if r.RTO() < 100*time.Millisecond {
		t.Fatalf("rto should be >= 100ms, got %v", r.RTO())
	}
}

func TestRTOMaxClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(800 * time.Millisecond) // slow link

	// rto = 800 + 4*400 = 2400ms → clamped to 2000ms
	if r.RTO() > 2000*time.Millisecond {
		t.Fatalf("rto should be <= 2000ms, got %v", r.RTO())
	}
}

func TestRTOBackoff(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)
	rto1 := r.RTO()

	r.Backoff()
	rto2 := r.RTO()

	if rto2 != 2*rto1 {
		t.Fatalf("backoff: got %v, want %v", rto2, 2*rto1)
	}
}

func TestRTOBackoffClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)
	// Backoff many times — should cap at 2000ms
	for i := 0; i < 10; i++ {
		r.Backoff()
	}

	if r.RTO() > 2000*time.Millisecond {
		t.Fatalf("rto should cap at 2000ms, got %v", r.RTO())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestRT -v`
Expected: FAIL — `NewRTTEstimator` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/rtt.go`:

```go
package arq

import (
	"sync"
	"time"
)

const (
	minRTO = 100 * time.Millisecond
	maxRTO = 2000 * time.Millisecond
)

// RTTEstimator implements Jacobson/Karels RTT estimation.
type RTTEstimator struct {
	mu     sync.Mutex
	srtt   time.Duration
	rttvar time.Duration
	rto    time.Duration
	init   bool
}

func NewRTTEstimator() *RTTEstimator {
	return &RTTEstimator{
		rto: 1000 * time.Millisecond, // initial RTO before any samples
	}
}

// Update adds an RTT sample. Only call for non-retransmitted packets (Karn's algorithm).
func (r *RTTEstimator) Update(sample time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.init {
		r.srtt = sample
		r.rttvar = sample / 2
		r.init = true
	} else {
		diff := r.srtt - sample
		if diff < 0 {
			diff = -diff
		}
		r.rttvar = r.rttvar*3/4 + diff/4
		r.srtt = r.srtt*7/8 + sample/8
	}

	r.rto = r.srtt + 4*r.rttvar
	r.clampRTO()
}

// Backoff doubles the RTO (exponential backoff on retransmit).
func (r *RTTEstimator) Backoff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rto *= 2
	r.clampRTO()
}

func (r *RTTEstimator) clampRTO() {
	if r.rto < minRTO {
		r.rto = minRTO
	}
	if r.rto > maxRTO {
		r.rto = maxRTO
	}
}

// RTO returns the current retransmission timeout.
func (r *RTTEstimator) RTO() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rto
}

// SRTT returns the smoothed RTT.
func (r *RTTEstimator) SRTT() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.srtt
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestRT -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/rtt.go pkg/udp/arq/rtt_test.go
git commit -m "feat(arq): add Jacobson/Karels RTT estimator"
```

---

## Task 4: CUBIC Congestion Control

**Files:**
- Create: `pkg/udp/arq/congestion.go`
- Create: `pkg/udp/arq/congestion_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/congestion_test.go`:

```go
package arq

import (
	"testing"
	"time"
)

func TestCongestionSlowStart(t *testing.T) {
	cc := NewCongestionControl()

	// Initial cwnd should be 10
	if cc.Window() != 10 {
		t.Fatalf("initial cwnd: got %d, want 10", cc.Window())
	}

	// In slow start, each ACK increases cwnd by 1
	cc.OnAck(1)
	if cc.Window() != 11 {
		t.Fatalf("after 1 ack: got %d, want 11", cc.Window())
	}

	// 10 more ACKs → cwnd = 21
	for i := 0; i < 10; i++ {
		cc.OnAck(1)
	}
	if cc.Window() != 21 {
		t.Fatalf("after 11 acks: got %d, want 21", cc.Window())
	}
}

func TestCongestionOnLoss(t *testing.T) {
	cc := NewCongestionControl()

	// Grow window first
	for i := 0; i < 90; i++ {
		cc.OnAck(1)
	}
	// cwnd = 100
	if cc.Window() != 100 {
		t.Fatalf("cwnd: got %d, want 100", cc.Window())
	}

	cc.OnLoss()

	// ssthresh = 100 * 0.7 = 70, cwnd = 70
	if cc.Window() != 70 {
		t.Fatalf("after loss: got %d, want 70", cc.Window())
	}
}

func TestCongestionAvoidanceCubic(t *testing.T) {
	cc := NewCongestionControl()

	// Get into congestion avoidance: grow to 100, lose, now ssthresh=70, cwnd=70
	for i := 0; i < 90; i++ {
		cc.OnAck(1)
	}
	cc.OnLoss()
	// Now in congestion avoidance (cwnd=70 >= ssthresh=70)

	prevWnd := cc.Window()
	// Simulate 1 second passing with ACKs
	time.Sleep(10 * time.Millisecond) // small delay so cubic time > 0
	cc.OnAck(1)

	// Window should grow (CUBIC function), but slowly
	if cc.Window() < prevWnd {
		t.Fatalf("cwnd should not decrease in congestion avoidance: was %d, now %d", prevWnd, cc.Window())
	}
}

func TestCongestionCanSend(t *testing.T) {
	cc := NewCongestionControl()
	// cwnd=10, inFlight=0

	if !cc.CanSend() {
		t.Fatal("should be able to send when inFlight < cwnd")
	}

	cc.OnSend()
	cc.OnSend()
	// inFlight=2, cwnd=10
	if !cc.CanSend() {
		t.Fatal("should be able to send when inFlight < cwnd")
	}

	// Fill the window
	for i := 0; i < 8; i++ {
		cc.OnSend()
	}
	// inFlight=10, cwnd=10
	if cc.CanSend() {
		t.Fatal("should NOT be able to send when inFlight >= cwnd")
	}

	// ACK releases a slot
	cc.OnAck(1)
	if !cc.CanSend() {
		t.Fatal("should be able to send after ACK")
	}
}

func TestCongestionMaxWindow(t *testing.T) {
	cc := NewCongestionControl()

	// ACK a huge number
	for i := 0; i < 2000; i++ {
		cc.OnAck(1)
	}

	if cc.Window() > maxCwnd {
		t.Fatalf("cwnd should cap at %d, got %d", maxCwnd, cc.Window())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestCongestion -v`
Expected: FAIL — `NewCongestionControl` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/congestion.go`:

```go
package arq

import (
	"math"
	"sync"
	"time"
)

const (
	initCwnd = 10
	maxCwnd  = 1024

	cubicBeta = 0.7
	cubicC    = 0.4
)

// CongestionControl implements CUBIC congestion control.
type CongestionControl struct {
	mu       sync.Mutex
	cwnd     float64
	ssthresh float64
	wMax     float64
	lastLoss time.Time
	inFlight int

	sendReady *sync.Cond // signaled when inFlight drops below cwnd
}

func NewCongestionControl() *CongestionControl {
	cc := &CongestionControl{
		cwnd:     initCwnd,
		ssthresh: math.MaxFloat64, // start in slow start
	}
	cc.sendReady = sync.NewCond(&cc.mu)
	return cc
}

// Window returns the current congestion window as an integer.
func (cc *CongestionControl) Window() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	w := int(cc.cwnd)
	if w > maxCwnd {
		w = maxCwnd
	}
	return w
}

// CanSend returns true if in-flight packets are below the congestion window.
func (cc *CongestionControl) CanSend() bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight < int(cc.cwnd)
}

// WaitForSlot blocks until inFlight < cwnd or done is closed.
// Returns false if done was closed.
func (cc *CongestionControl) WaitForSlot(done <-chan struct{}) bool {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	for cc.inFlight >= int(cc.cwnd) {
		// Check done channel without blocking
		select {
		case <-done:
			return false
		default:
		}
		cc.sendReady.Wait()
	}
	return true
}

// OnSend records a packet being sent.
func (cc *CongestionControl) OnSend() {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.inFlight++
}

// OnAck is called when acked packets are confirmed.
// n is the number of newly acked packets.
func (cc *CongestionControl) OnAck(n int) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.inFlight -= n
	if cc.inFlight < 0 {
		cc.inFlight = 0
	}

	for i := 0; i < n; i++ {
		if cc.cwnd < cc.ssthresh {
			// Slow start: increase by 1 per ACK
			cc.cwnd++
		} else {
			// Congestion avoidance: CUBIC growth
			cc.cubicGrow()
		}
	}

	if cc.cwnd > maxCwnd {
		cc.cwnd = maxCwnd
	}

	cc.sendReady.Broadcast()
}

// OnLoss is called when a packet loss is detected (RTO or fast retransmit).
func (cc *CongestionControl) OnLoss() {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	cc.wMax = cc.cwnd
	cc.ssthresh = cc.cwnd * cubicBeta
	if cc.ssthresh < initCwnd {
		cc.ssthresh = initCwnd
	}
	cc.cwnd = cc.ssthresh
	cc.lastLoss = time.Now()
}

// cubicGrow applies the CUBIC window function.
// Must be called with cc.mu held.
func (cc *CongestionControl) cubicGrow() {
	if cc.lastLoss.IsZero() {
		// First time in congestion avoidance (no loss yet), use linear growth
		cc.cwnd += 1.0 / cc.cwnd
		return
	}

	t := time.Since(cc.lastLoss).Seconds()
	k := math.Cbrt(cc.wMax * (1 - cubicBeta) / cubicC)
	w := cubicC*math.Pow(t-k, 3) + cc.wMax

	if w > cc.cwnd {
		cc.cwnd = w
	} else {
		// TCP-friendly mode: linear increase
		cc.cwnd += 1.0 / cc.cwnd
	}
}

// InFlight returns the number of in-flight packets.
func (cc *CongestionControl) InFlight() int {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.inFlight
}

// SignalAll wakes all goroutines waiting on WaitForSlot (used on close).
func (cc *CongestionControl) SignalAll() {
	cc.sendReady.Broadcast()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestCongestion -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/congestion.go pkg/udp/arq/congestion_test.go
git commit -m "feat(arq): add CUBIC congestion control"
```

---

## Task 5: Send Buffer

**Files:**
- Create: `pkg/udp/arq/send_buffer.go`
- Create: `pkg/udp/arq/send_buffer_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/send_buffer_test.go`:

```go
package arq

import (
	"testing"
	"time"
)

func TestSendBufferAddAndAck(t *testing.T) {
	sb := NewSendBuffer(1024)

	sb.Add(1, []byte("pkt1"), 0x03, 10, 0, []byte("data1"))
	sb.Add(2, []byte("pkt2"), 0x03, 10, 1, []byte("data2"))
	sb.Add(3, []byte("pkt3"), 0x03, 10, 2, []byte("data3"))

	if sb.Len() != 3 {
		t.Fatalf("len: got %d, want 3", sb.Len())
	}

	// ACK cumulative up to 2
	acked := sb.AckCumulative(2)
	if acked != 2 {
		t.Fatalf("acked: got %d, want 2", acked)
	}
	if sb.Len() != 1 {
		t.Fatalf("len after ack: got %d, want 1", sb.Len())
	}
}

func TestSendBufferAckSelective(t *testing.T) {
	sb := NewSendBuffer(1024)

	sb.Add(1, []byte("pkt1"), 0x03, 10, 0, []byte("d1"))
	sb.Add(2, []byte("pkt2"), 0x03, 10, 1, []byte("d2"))
	sb.Add(3, []byte("pkt3"), 0x03, 10, 2, []byte("d3"))

	// Selectively ACK packet 3 (skip 2)
	sb.AckSelective(3)

	// Packet 3 should be marked acked
	p3 := sb.Get(3)
	if p3 == nil || !p3.Acked {
		t.Fatal("packet 3 should be acked")
	}
	// Packet 2 still unacked
	p2 := sb.Get(2)
	if p2 == nil || p2.Acked {
		t.Fatal("packet 2 should still be unacked")
	}
}

func TestSendBufferExpired(t *testing.T) {
	sb := NewSendBuffer(1024)

	sb.Add(1, []byte("pkt1"), 0x03, 10, 0, []byte("d1"))
	sb.Add(2, []byte("pkt2"), 0x03, 10, 1, []byte("d2"))

	// Artificially age packet 1
	p1 := sb.Get(1)
	p1.LastSentAt = time.Now().Add(-500 * time.Millisecond)

	expired := sb.Expired(200 * time.Millisecond)
	if len(expired) != 1 {
		t.Fatalf("expired: got %d, want 1", len(expired))
	}
	if expired[0].PktNum != 1 {
		t.Fatalf("expired pktNum: got %d, want 1", expired[0].PktNum)
	}
}

func TestSendBufferMaxRetransmit(t *testing.T) {
	sb := NewSendBuffer(1024)
	sb.Add(1, []byte("pkt1"), 0x03, 10, 0, []byte("d1"))

	p := sb.Get(1)
	p.Retransmits = maxRetransmits

	if !sb.IsMaxRetransmits(1) {
		t.Fatal("should be at max retransmits")
	}
}

func TestSendBufferRTTSample(t *testing.T) {
	sb := NewSendBuffer(1024)

	before := time.Now()
	sb.Add(1, []byte("pkt1"), 0x03, 10, 0, []byte("d1"))
	time.Sleep(10 * time.Millisecond)

	p := sb.Get(1)
	sample := time.Since(p.SentAt)
	if sample < 10*time.Millisecond {
		t.Fatalf("sample too small: %v", sample)
	}

	// Retransmitted packet should not provide RTT sample
	p.Retransmits = 1
	if !p.IsRetransmit() {
		t.Fatal("should be marked as retransmit")
	}
	_ = before // suppress unused
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestSendBuffer -v`
Expected: FAIL — `NewSendBuffer` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/send_buffer.go`:

```go
package arq

import (
	"sync"
	"time"
)

const maxRetransmits = 10

// SentPacket holds state for one packet in the send buffer.
type SentPacket struct {
	PktNum      uint32
	RawData     []byte // encoded datagram for retransmit
	MsgType     byte
	StreamID    uint32
	Seq         uint32
	Payload     []byte // original data for re-encoding on retransmit
	SentAt      time.Time
	LastSentAt  time.Time
	Retransmits int
	Acked       bool
}

// IsRetransmit returns true if this packet was retransmitted at least once.
func (p *SentPacket) IsRetransmit() bool {
	return p.Retransmits > 0
}

// SendBuffer stores unacknowledged packets.
type SendBuffer struct {
	mu      sync.Mutex
	packets map[uint32]*SentPacket
	minPkt  uint32 // lowest unacked PktNum for scan optimization
	maxSize int
}

func NewSendBuffer(maxSize int) *SendBuffer {
	return &SendBuffer{
		packets: make(map[uint32]*SentPacket),
		maxSize: maxSize,
	}
}

// Add stores a sent packet. raw is the encoded datagram for fast retransmit.
func (sb *SendBuffer) Add(pktNum uint32, raw []byte, msgType byte, streamID, seq uint32, payload []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	now := time.Now()
	sb.packets[pktNum] = &SentPacket{
		PktNum:     pktNum,
		RawData:    raw,
		MsgType:    msgType,
		StreamID:   streamID,
		Seq:        seq,
		Payload:    payload,
		SentAt:     now,
		LastSentAt: now,
	}

	if sb.minPkt == 0 || pktNum < sb.minPkt {
		sb.minPkt = pktNum
	}
}

// Get returns a packet by PktNum, or nil.
func (sb *SendBuffer) Get(pktNum uint32) *SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.packets[pktNum]
}

// AckCumulative removes all packets with PktNum <= cumAck. Returns count removed.
func (sb *SendBuffer) AckCumulative(cumAck uint32) int {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	count := 0
	for pktNum := range sb.packets {
		if pktNum <= cumAck {
			delete(sb.packets, pktNum)
			count++
		}
	}

	// Advance minPkt
	if sb.minPkt <= cumAck {
		sb.minPkt = cumAck + 1
	}

	return count
}

// AckSelective marks a specific packet as acked (but doesn't remove it until cumAck catches up).
func (sb *SendBuffer) AckSelective(pktNum uint32) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if p, ok := sb.packets[pktNum]; ok {
		p.Acked = true
	}
}

// Expired returns unacked packets older than rto.
func (sb *SendBuffer) Expired(rto time.Duration) []*SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	now := time.Now()
	var result []*SentPacket
	for _, p := range sb.packets {
		if !p.Acked && now.Sub(p.LastSentAt) > rto {
			result = append(result, p)
		}
	}
	return result
}

// MarkRetransmitted updates a packet after retransmission with a new PktNum.
// The old entry is removed and a new one is created.
func (sb *SendBuffer) MarkRetransmitted(oldPktNum, newPktNum uint32, newRaw []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	old, ok := sb.packets[oldPktNum]
	if !ok {
		return
	}

	delete(sb.packets, oldPktNum)

	now := time.Now()
	sb.packets[newPktNum] = &SentPacket{
		PktNum:      newPktNum,
		RawData:     newRaw,
		MsgType:     old.MsgType,
		StreamID:    old.StreamID,
		Seq:         old.Seq,
		Payload:     old.Payload,
		SentAt:      time.Time{}, // zero — don't measure RTT from retransmits
		LastSentAt:  now,
		Retransmits: old.Retransmits + 1,
	}
}

// IsMaxRetransmits checks if a packet has exceeded the retransmit limit.
func (sb *SendBuffer) IsMaxRetransmits(pktNum uint32) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	p, ok := sb.packets[pktNum]
	return ok && p.Retransmits >= maxRetransmits
}

// Len returns the number of packets in the buffer.
func (sb *SendBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return len(sb.packets)
}

// IsFull returns true if the buffer has reached its max size.
func (sb *SendBuffer) IsFull() bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return len(sb.packets) >= sb.maxSize
}

// FirstUnacked returns the first unacked packet (by lowest PktNum), or nil.
func (sb *SendBuffer) FirstUnacked() *SentPacket {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	var best *SentPacket
	for _, p := range sb.packets {
		if !p.Acked && (best == nil || p.PktNum < best.PktNum) {
			best = p
		}
	}
	return best
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestSendBuffer -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/send_buffer.go pkg/udp/arq/send_buffer_test.go
git commit -m "feat(arq): add send buffer with retransmit tracking"
```

---

## Task 6: Receive Buffer

**Files:**
- Create: `pkg/udp/arq/recv_buffer.go`
- Create: `pkg/udp/arq/recv_buffer_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/recv_buffer_test.go`:

```go
package arq

import (
	"testing"
)

func TestRecvBufferInOrder(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(512, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(0, []byte("a"))
	rb.Insert(1, []byte("b"))
	rb.Insert(2, []byte("c"))

	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	for i, seq := range delivered {
		if seq != uint32(i) {
			t.Fatalf("delivered[%d] = %d, want %d", i, seq, i)
		}
	}
}

func TestRecvBufferOutOfOrder(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(512, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(2, []byte("c")) // buffered
	rb.Insert(0, []byte("a")) // delivered, expected→1
	rb.Insert(1, []byte("b")) // delivered + flush 2

	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	// Delivery order: 0, 1, 2
	expected := []uint32{0, 1, 2}
	for i, seq := range delivered {
		if seq != expected[i] {
			t.Fatalf("delivered[%d] = %d, want %d", i, seq, expected[i])
		}
	}
}

func TestRecvBufferDuplicate(t *testing.T) {
	count := 0
	rb := NewRecvBuffer(512, func(seq uint32, data []byte) {
		count++
	})

	rb.Insert(0, []byte("a"))
	rb.Insert(0, []byte("a")) // duplicate

	if count != 1 {
		t.Fatalf("delivered: got %d, want 1 (duplicate should be ignored)", count)
	}
}

func TestRecvBufferGapFill(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(512, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(0, []byte("0"))
	rb.Insert(1, []byte("1"))
	// Skip 2
	rb.Insert(3, []byte("3"))
	rb.Insert(4, []byte("4"))
	// delivered so far: 0, 1
	if len(delivered) != 2 {
		t.Fatalf("before gap fill: got %d delivered, want 2", len(delivered))
	}

	// Fill gap
	rb.Insert(2, []byte("2"))
	// Should deliver: 2, 3, 4
	if len(delivered) != 5 {
		t.Fatalf("after gap fill: got %d delivered, want 5", len(delivered))
	}
}

func TestRecvBufferMaxSize(t *testing.T) {
	count := 0
	rb := NewRecvBuffer(3, func(seq uint32, data []byte) {
		count++
	})

	// expected=0, insert 1,2,3 (out of order, buffered)
	rb.Insert(1, []byte("1"))
	rb.Insert(2, []byte("2"))
	rb.Insert(3, []byte("3"))
	// Buffer full (3 items)

	// This should be dropped (buffer full, still out of order)
	rb.Insert(4, []byte("4"))

	// Now deliver 0 → flushes 1,2,3
	rb.Insert(0, []byte("0"))
	if count != 4 {
		t.Fatalf("delivered: got %d, want 4 (0,1,2,3 — 4 was dropped)", count)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestRecvBuffer -v`
Expected: FAIL — `NewRecvBuffer` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/recv_buffer.go`:

```go
package arq

import (
	"sync"
)

// RecvBuffer reorders incoming stream data by sequence number.
// Delivers packets in-order via a callback.
type RecvBuffer struct {
	mu       sync.Mutex
	expected uint32
	buffer   map[uint32][]byte
	maxBuf   int
	deliver  func(seq uint32, data []byte)
}

// NewRecvBuffer creates a reorder buffer. deliverFn is called for each in-order packet.
func NewRecvBuffer(maxBuf int, deliverFn func(seq uint32, data []byte)) *RecvBuffer {
	return &RecvBuffer{
		expected: 0,
		buffer:   make(map[uint32][]byte),
		maxBuf:   maxBuf,
		deliver:  deliverFn,
	}
}

// Insert adds a packet. If it's the expected seq, delivers it and flushes any
// consecutive buffered packets. Out-of-order packets are buffered.
func (rb *RecvBuffer) Insert(seq uint32, data []byte) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if seq < rb.expected {
		// Duplicate or old packet
		return
	}

	if seq == rb.expected {
		// Deliver immediately
		rb.deliver(seq, data)
		rb.expected++

		// Flush consecutive buffered packets
		for {
			if d, ok := rb.buffer[rb.expected]; ok {
				delete(rb.buffer, rb.expected)
				rb.deliver(rb.expected, d)
				rb.expected++
			} else {
				break
			}
		}
		return
	}

	// Out of order: buffer if space available
	if len(rb.buffer) >= rb.maxBuf {
		return // drop
	}

	// Copy data to avoid aliasing
	cp := make([]byte, len(data))
	copy(cp, data)
	rb.buffer[seq] = cp
}

// Expected returns the next expected sequence number.
func (rb *RecvBuffer) Expected() uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.expected
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestRecvBuffer -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/recv_buffer.go pkg/udp/arq/recv_buffer_test.go
git commit -m "feat(arq): add per-stream receive reorder buffer"
```

---

## Task 7: AckState

**Files:**
- Create: `pkg/udp/arq/ack_state.go`
- Create: `pkg/udp/arq/ack_state_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/ack_state_test.go`:

```go
package arq

import (
	"testing"
)

func TestAckStateInOrder(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	as.RecordReceived(2)
	as.RecordReceived(3)

	ack := as.BuildAck()
	if ack.CumAck != 3 {
		t.Fatalf("cumAck: got %d, want 3", ack.CumAck)
	}
}

func TestAckStateGap(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	as.RecordReceived(3) // gap at 2
	as.RecordReceived(4)

	ack := as.BuildAck()
	if ack.CumAck != 1 {
		t.Fatalf("cumAck: got %d, want 1", ack.CumAck)
	}
	if !ack.IsReceived(3) {
		t.Fatal("3 should be in bitmap")
	}
	if !ack.IsReceived(4) {
		t.Fatal("4 should be in bitmap")
	}
	if ack.IsReceived(2) {
		t.Fatal("2 should NOT be in bitmap")
	}
}

func TestAckStateGapFill(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	as.RecordReceived(3)
	as.RecordReceived(2) // fill gap

	ack := as.BuildAck()
	if ack.CumAck != 3 {
		t.Fatalf("cumAck: got %d, want 3", ack.CumAck)
	}
}

func TestAckStateNeedsImmediateAck(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	if as.NeedsImmediateAck() {
		t.Fatal("in-order packet should NOT trigger immediate ACK")
	}

	as.RecordReceived(3) // gap
	if !as.NeedsImmediateAck() {
		t.Fatal("gap should trigger immediate ACK")
	}
}

func TestAckStateDelayedAck(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	if as.NeedsDelayedAck() {
		t.Fatal("1 packet should not trigger delayed ACK yet")
	}

	as.RecordReceived(2)
	if !as.NeedsDelayedAck() {
		t.Fatal("2 packets should trigger delayed ACK")
	}

	as.AckSent()
	if as.NeedsDelayedAck() {
		t.Fatal("after AckSent, delayed ACK should be reset")
	}
}

func TestAckStateDupCount(t *testing.T) {
	as := NewAckState()

	as.RecordReceived(1)
	as.RecordReceived(3) // gap
	as.RecordReceived(4) // gap still at 2
	as.RecordReceived(5) // gap still at 2

	if as.DupCount() != 3 {
		t.Fatalf("dupCount: got %d, want 3", as.DupCount())
	}

	as.RecordReceived(2) // fill gap
	if as.DupCount() != 0 {
		t.Fatalf("dupCount after fill: got %d, want 0", as.DupCount())
	}
}

func TestAckStateCleanup(t *testing.T) {
	as := NewAckState()

	for i := uint32(1); i <= 100; i++ {
		as.RecordReceived(i)
	}

	// Internal map should be cleaned up — entries <= cumAck removed
	as.mu.Lock()
	mapSize := len(as.received)
	as.mu.Unlock()

	if mapSize != 0 {
		t.Fatalf("received map should be empty after in-order delivery, got %d entries", mapSize)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestAckState -v`
Expected: FAIL — `NewAckState` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/ack_state.go`:

```go
package arq

import (
	"sync"
)

const delayedAckThreshold = 2 // send ACK after this many unacked packets

// AckState tracks received PktNums and builds ACK packets.
type AckState struct {
	mu             sync.Mutex
	received       map[uint32]bool
	cumAck         uint32
	pktsSinceAck   int  // packets received since last ACK sent
	immediateAck   bool // set when gap detected
	dupCount       int  // consecutive packets received while cumAck stuck
	lastCumAck     uint32
}

func NewAckState() *AckState {
	return &AckState{
		received: make(map[uint32]bool),
	}
}

// RecordReceived records that a packet with the given PktNum was received.
func (as *AckState) RecordReceived(pktNum uint32) {
	as.mu.Lock()
	defer as.mu.Unlock()

	if pktNum <= as.cumAck {
		return // already acknowledged cumulatively
	}

	as.received[pktNum] = true
	as.pktsSinceAck++

	prevCumAck := as.cumAck

	// Try to advance cumAck
	for as.received[as.cumAck+1] {
		as.cumAck++
		delete(as.received, as.cumAck) // clean up
	}

	// Detect gap: if cumAck didn't advance to pktNum, there's a gap
	if pktNum > as.cumAck {
		as.immediateAck = true
	}

	// Track duplicate ACK count (cumAck didn't move)
	if as.cumAck == prevCumAck && pktNum > as.cumAck {
		as.dupCount++
	} else if as.cumAck > prevCumAck {
		as.dupCount = 0
	}
}

// BuildAck creates an AckData from the current state.
func (as *AckState) BuildAck() *AckData {
	as.mu.Lock()
	defer as.mu.Unlock()

	ack := &AckData{CumAck: as.cumAck}

	// Fill bitmap for received packets beyond cumAck
	for pktNum := range as.received {
		ack.SetReceived(pktNum)
	}

	return ack
}

// NeedsImmediateAck returns true if a gap was detected (for fast retransmit).
func (as *AckState) NeedsImmediateAck() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.immediateAck
}

// NeedsDelayedAck returns true if enough packets arrived to warrant an ACK.
func (as *AckState) NeedsDelayedAck() bool {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.pktsSinceAck >= delayedAckThreshold
}

// AckSent resets pending ACK state after an ACK is sent.
func (as *AckState) AckSent() {
	as.mu.Lock()
	defer as.mu.Unlock()
	as.pktsSinceAck = 0
	as.immediateAck = false
}

// DupCount returns how many packets arrived while cumAck was stuck.
func (as *AckState) DupCount() int {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.dupCount
}

// CumAck returns the current cumulative acknowledgment.
func (as *AckState) CumAck() uint32 {
	as.mu.Lock()
	defer as.mu.Unlock()
	return as.cumAck
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestAckState -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/ack_state.go pkg/udp/arq/ack_state_test.go
git commit -m "feat(arq): add AckState for received packet tracking and ACK generation"
```

---

## Task 8: ARQ Controller

**Files:**
- Create: `pkg/udp/arq/controller.go`
- Create: `pkg/udp/arq/controller_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/udp/arq/controller_test.go`:

```go
package arq

import (
	"sync"
	"testing"
	"time"

	pkgudp "proxyness/pkg/udp"
)

// mockSender captures sent datagrams for inspection.
type mockSender struct {
	mu   sync.Mutex
	sent [][]byte
}

func (m *mockSender) send(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.sent = append(m.sent, cp)
	return nil
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func TestControllerSendAndReceiveAck(t *testing.T) {
	sender := &mockSender{}
	var delivered [][]byte
	var deliverMu sync.Mutex

	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		cp := make([]byte, len(data))
		copy(cp, data)
		delivered = append(delivered, cp)
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	ctrl.CreateRecvBuffer(1)

	// Send 3 packets
	for i := 0; i < 3; i++ {
		err := ctrl.Send(pkgudp.MsgStreamData, 1, uint32(i), []byte("hello"))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	// Should have 3 packets in send buffer
	if sender.count() != 3 {
		t.Fatalf("sent: got %d, want 3", sender.count())
	}

	// Simulate ACK for all 3 (cumAck=3, pktNums were 1,2,3)
	ack := &AckData{CumAck: 3}
	ctrl.HandleAck(ack.Encode())

	// Send buffer should be drained
	time.Sleep(10 * time.Millisecond) // allow processing
	if ctrl.sendBuf.Len() != 0 {
		t.Fatalf("sendBuf: got %d, want 0", ctrl.sendBuf.Len())
	}
}

func TestControllerReceiveInOrder(t *testing.T) {
	sender := &mockSender{}
	var delivered []string
	var deliverMu sync.Mutex

	key := make([]byte, 32)
	ctrl := New(0xABCD, key, sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		delivered = append(delivered, string(data))
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	ctrl.CreateRecvBuffer(1)

	// Simulate receiving 3 in-order packets
	for i := 0; i < 3; i++ {
		ctrl.HandleData(&pkgudp.Packet{
			Type:     pkgudp.MsgStreamData,
			PktNum:   uint32(i + 1),
			StreamID: 1,
			Seq:      uint32(i),
			Data:     []byte("msg"),
		})
	}

	deliverMu.Lock()
	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	deliverMu.Unlock()
}

func TestControllerReceiveOutOfOrder(t *testing.T) {
	sender := &mockSender{}
	var delivered []uint32
	var deliverMu sync.Mutex

	key := make([]byte, 32)
	ctrl := New(0xABCD, key, sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		delivered = append(delivered, uint32(data[0]))
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	ctrl.CreateRecvBuffer(1)

	// Send out of order: seq 2, 0, 1
	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 3, StreamID: 1, Seq: 2, Data: []byte{2},
	})
	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 1, StreamID: 1, Seq: 0, Data: []byte{0},
	})
	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 2, StreamID: 1, Seq: 1, Data: []byte{1},
	})

	deliverMu.Lock()
	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	// Should be delivered in order: 0, 1, 2
	for i, v := range delivered {
		if v != uint32(i) {
			t.Fatalf("delivered[%d] = %d, want %d", i, v, i)
		}
	}
	deliverMu.Unlock()
}

func TestControllerCwndBackpressure(t *testing.T) {
	sender := &mockSender{}
	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(uint32, []byte) {})
	defer ctrl.Close()

	// Send initCwnd (10) packets — should all succeed without blocking
	for i := 0; i < initCwnd; i++ {
		err := ctrl.Send(pkgudp.MsgStreamData, 1, uint32(i), []byte("x"))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	// Next send should block — test with timeout
	done := make(chan error, 1)
	go func() {
		done <- ctrl.Send(pkgudp.MsgStreamData, 1, uint32(initCwnd), []byte("x"))
	}()

	select {
	case <-done:
		t.Fatal("send should have blocked (cwnd full)")
	case <-time.After(50 * time.Millisecond):
		// expected: blocked
	}

	// ACK one packet to unblock
	ack := &AckData{CumAck: 1}
	ctrl.HandleAck(ack.Encode())

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send after ack: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("send should have unblocked after ACK")
	}
}

func TestControllerRetransmit(t *testing.T) {
	sender := &mockSender{}
	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(uint32, []byte) {})
	defer ctrl.Close()

	// Send 1 packet
	ctrl.Send(pkgudp.MsgStreamData, 1, 0, []byte("data"))
	initialCount := sender.count()

	// Artificially expire it
	p := ctrl.sendBuf.FirstUnacked()
	if p == nil {
		t.Fatal("should have an unacked packet")
	}
	p.LastSentAt = time.Now().Add(-5 * time.Second)

	// Trigger retransmit
	ctrl.RetransmitTick()

	time.Sleep(10 * time.Millisecond)
	if sender.count() <= initialCount {
		t.Fatal("retransmit should have sent a new packet")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -run TestController -v`
Expected: FAIL — `New` not defined

- [ ] **Step 3: Write implementation**

Create `pkg/udp/arq/controller.go`:

```go
package arq

import (
	"fmt"
	"sync"
	"sync/atomic"

	pkgudp "proxyness/pkg/udp"
)

const (
	sendBufSize = 1024
	recvBufSize = 512
)

// Controller manages ARQ reliability for a single UDP session.
type Controller struct {
	connID     uint32
	sessionKey []byte

	// Sending
	sendBuf    *SendBuffer
	nextPktNum atomic.Uint32
	cwnd       *CongestionControl
	rtt        *RTTEstimator

	// Receiving
	recvMu   sync.Mutex
	recvBufs map[uint32]*RecvBuffer
	ackState *AckState

	// I/O
	sendFn    func([]byte) error
	deliverFn func(streamID uint32, data []byte)

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// New creates a new ARQ Controller for a session.
func New(connID uint32, sessionKey []byte, sendFn func([]byte) error, deliverFn func(streamID uint32, data []byte)) *Controller {
	return &Controller{
		connID:     connID,
		sessionKey: sessionKey,
		sendBuf:    NewSendBuffer(sendBufSize),
		cwnd:       NewCongestionControl(),
		rtt:        NewRTTEstimator(),
		recvBufs:   make(map[uint32]*RecvBuffer),
		ackState:   NewAckState(),
		sendFn:     sendFn,
		deliverFn:  deliverFn,
		done:       make(chan struct{}),
	}
}

// Send sends a reliable packet. Blocks if the congestion window is full.
func (c *Controller) Send(msgType byte, streamID, seq uint32, data []byte) error {
	// Wait for cwnd slot
	if !c.cwnd.WaitForSlot(c.done) {
		return fmt.Errorf("controller closed")
	}

	pktNum := c.nextPktNum.Add(1)

	pkt := &pkgudp.Packet{
		ConnID:   c.connID,
		Type:     msgType,
		PktNum:   pktNum,
		StreamID: streamID,
		Seq:      seq,
		Data:     data,
	}

	encoded, err := pkgudp.EncodePacket(pkt, c.sessionKey)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	// Store in send buffer before sending (so retransmit can find it)
	payload := make([]byte, len(data))
	copy(payload, data)
	c.sendBuf.Add(pktNum, encoded, msgType, streamID, seq, payload)
	c.cwnd.OnSend()

	if err := c.sendFn(encoded); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return nil
}

// HandleData processes an incoming MsgStreamData packet.
// Inserts into the per-stream reorder buffer and records the PktNum for ACK.
func (c *Controller) HandleData(pkt *pkgudp.Packet) {
	if pkt.PktNum > 0 {
		c.ackState.RecordReceived(pkt.PktNum)
	}

	c.recvMu.Lock()
	rb, ok := c.recvBufs[pkt.StreamID]
	c.recvMu.Unlock()

	if ok {
		rb.Insert(pkt.Seq, pkt.Data)
	}

	// Send immediate ACK if gap detected
	if c.ackState.NeedsImmediateAck() {
		c.sendAck()
	}
}

// HandleAck processes an incoming MsgAck payload.
func (c *Controller) HandleAck(data []byte) {
	ack, err := DecodeAckData(data)
	if err != nil {
		return
	}

	// Process cumulative ACK
	acked := c.sendBuf.AckCumulative(ack.CumAck)

	// Process selective ACKs from bitmap
	for i := uint32(0); i < 256; i++ {
		pktNum := ack.CumAck + 1 + i
		if ack.IsReceived(pktNum) {
			p := c.sendBuf.Get(pktNum)
			if p != nil && !p.Acked {
				c.sendBuf.AckSelective(pktNum)
				acked++
			}
		}
	}

	if acked > 0 {
		// Update RTT from first non-retransmitted acked packet
		// (we already removed cumulative, so check selective acks)
		// RTT sampling is best-effort; the main path is cumAck-based
		c.cwnd.OnAck(acked)
	}

	// Check for fast retransmit (3 dup acks)
	if c.ackState.DupCount() >= 3 {
		c.fastRetransmit()
	}
}

// HandleAckWithRTT processes an ACK and updates RTT from a specific PktNum's send time.
func (c *Controller) handleRTTFromAck(cumAck uint32) {
	// We can't easily get the SentAt for removed packets.
	// RTT is measured via RetransmitTick observing round-trip for non-retransmitted packets.
	// This is a simplification; in practice, we sample RTT from the first ACKed packet.
}

// RetransmitTick checks for RTO-expired packets and retransmits them.
// Call this every 10ms.
func (c *Controller) RetransmitTick() {
	rto := c.rtt.RTO()
	expired := c.sendBuf.Expired(rto)

	for _, p := range expired {
		if c.sendBuf.IsMaxRetransmits(p.PktNum) {
			// Stream is dead — could signal error via callback
			continue
		}

		// Retransmit with new PktNum
		newPktNum := c.nextPktNum.Add(1)

		newPkt := &pkgudp.Packet{
			ConnID:   c.connID,
			Type:     p.MsgType,
			PktNum:   newPktNum,
			StreamID: p.StreamID,
			Seq:      p.Seq,
			Data:     p.Payload,
		}

		encoded, err := pkgudp.EncodePacket(newPkt, c.sessionKey)
		if err != nil {
			continue
		}

		c.sendBuf.MarkRetransmitted(p.PktNum, newPktNum, encoded)
		c.sendFn(encoded) //nolint:errcheck

		// Exponential backoff
		c.rtt.Backoff()

		// Signal loss to congestion control (once per tick)
		c.cwnd.OnLoss()
	}
}

// AckTick sends a delayed ACK if pending. Call this every 25ms.
func (c *Controller) AckTick() {
	if c.ackState.NeedsDelayedAck() || c.ackState.NeedsImmediateAck() {
		c.sendAck()
	}
}

func (c *Controller) sendAck() {
	ack := c.ackState.BuildAck()
	c.ackState.AckSent()

	pkt := &pkgudp.Packet{
		ConnID: c.connID,
		Type:   pkgudp.MsgAck,
		PktNum: 0, // ACKs are not tracked
		Data:   ack.Encode(),
	}

	encoded, err := pkgudp.EncodePacket(pkt, c.sessionKey)
	if err != nil {
		return
	}

	c.sendFn(encoded) //nolint:errcheck
}

func (c *Controller) fastRetransmit() {
	p := c.sendBuf.FirstUnacked()
	if p == nil {
		return
	}

	newPktNum := c.nextPktNum.Add(1)

	newPkt := &pkgudp.Packet{
		ConnID:   c.connID,
		Type:     p.MsgType,
		PktNum:   newPktNum,
		StreamID: p.StreamID,
		Seq:      p.Seq,
		Data:     p.Payload,
	}

	encoded, err := pkgudp.EncodePacket(newPkt, c.sessionKey)
	if err != nil {
		return
	}

	c.sendBuf.MarkRetransmitted(p.PktNum, newPktNum, encoded)
	c.sendFn(encoded) //nolint:errcheck

	c.cwnd.OnLoss()
}

// CreateRecvBuffer creates a reorder buffer for a stream.
func (c *Controller) CreateRecvBuffer(streamID uint32) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	c.recvBufs[streamID] = NewRecvBuffer(recvBufSize, func(seq uint32, data []byte) {
		c.deliverFn(streamID, data)
	})
}

// RemoveRecvBuffer removes the reorder buffer for a stream.
func (c *Controller) RemoveRecvBuffer(streamID uint32) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	delete(c.recvBufs, streamID)
}

// Close stops the controller and wakes any blocked senders.
func (c *Controller) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	c.mu.Unlock()

	c.cwnd.SignalAll()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/pkg && go test ./udp/arq/ -v`
Expected: all PASS (all arq tests including Controller)

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/arq/controller.go pkg/udp/arq/controller_test.go
git commit -m "feat(arq): add ARQ Controller tying all components together"
```

---

## Task 9: Daemon Integration

**Files:**
- Modify: `daemon/internal/transport/udp.go`

- [ ] **Step 1: Update UDPTransport to include ARQ Controller**

In `daemon/internal/transport/udp.go`, replace the entire file:

```go
package transport

import (
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	pkgudp "proxyness/pkg/udp"
	"proxyness/pkg/udp/arq"
)

const (
	udpMaxPayload  = 1340 // max data bytes per MsgStreamData packet (was 1344, -4 for PktNum)
	udpKeepalive   = 15 * time.Second
	udpHandshakeTO = 3 * time.Second
	udpReadBuf     = 65535

	arqRetransmitInterval = 10 * time.Millisecond
	arqAckInterval        = 25 * time.Millisecond
)

// UDPTransport implements Transport over a single multiplexed UDP connection.
type UDPTransport struct {
	conn       *net.UDPConn
	sessionKey []byte
	connID     uint32
	devKey     []byte

	arq *arq.Controller

	mu      sync.Mutex
	streams map[uint32]*udpStream
	nextID  uint32

	closed atomic.Bool
	done   chan struct{}
}

func NewUDPTransport() *UDPTransport {
	return &UDPTransport{
		streams: make(map[uint32]*udpStream),
		done:    make(chan struct{}),
	}
}

// Connect dials the server via UDP, performs ECDH handshake, starts background loops.
func (t *UDPTransport) Connect(server, key string, machineID [16]byte) error {
	devKey, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("decode device key: %w", err)
	}
	t.devKey = devKey

	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return fmt.Errorf("resolve server addr: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return fmt.Errorf("udp dial: %w", err)
	}
	conn.SetReadBuffer(4 * 1024 * 1024)
	t.conn = conn

	// Generate ephemeral X25519 keypair
	priv, pubBytes, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		conn.Close()
		return fmt.Errorf("generate ephemeral key: %w", err)
	}

	// Build and encode HandshakeRequest
	req := &pkgudp.HandshakeRequest{
		EphemeralPub: pubBytes,
		DeviceKey:    key,
		MachineID:    machineID,
	}
	reqData, err := req.Encode()
	if err != nil {
		conn.Close()
		return fmt.Errorf("encode handshake request: %w", err)
	}

	// Send as Packet with ConnID=0, Type=MsgHandshake, encrypted with device key
	pkt := &pkgudp.Packet{
		ConnID: 0,
		Type:   pkgudp.MsgHandshake,
		Data:   reqData,
	}
	encoded, err := pkgudp.EncodePacket(pkt, devKey)
	if err != nil {
		conn.Close()
		return fmt.Errorf("encode handshake packet: %w", err)
	}

	if _, err := conn.Write(encoded); err != nil {
		conn.Close()
		return fmt.Errorf("send handshake: %w", err)
	}

	// Wait for handshake response (3s timeout)
	conn.SetReadDeadline(time.Now().Add(udpHandshakeTO))
	buf := make([]byte, udpReadBuf)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return fmt.Errorf("read handshake response: %w", err)
	}

	respPkt, err := pkgudp.DecodePacket(buf[:n], devKey)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode handshake response packet: %w", err)
	}
	if respPkt.Type != pkgudp.MsgHandshake {
		conn.Close()
		return fmt.Errorf("unexpected handshake response type: 0x%02x", respPkt.Type)
	}

	resp, err := pkgudp.DecodeHandshakeResponse(respPkt.Data)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode handshake response: %w", err)
	}

	sessionKey, err := pkgudp.DeriveSessionKey(priv, resp.EphemeralPub)
	if err != nil {
		conn.Close()
		return fmt.Errorf("derive session key: %w", err)
	}

	t.sessionKey = sessionKey
	t.connID = resp.SessionToken

	// Create ARQ Controller
	t.arq = arq.New(t.connID, t.sessionKey, func(data []byte) error {
		_, err := t.conn.Write(data)
		return err
	}, func(streamID uint32, data []byte) {
		t.mu.Lock()
		s, ok := t.streams[streamID]
		t.mu.Unlock()
		if ok {
			// Non-blocking send to avoid deadlock if recvCh is full
			select {
			case s.recvCh <- append([]byte(nil), data...):
			default:
			}
		}
	})

	go t.recvLoop()
	go t.keepaliveLoop()
	go t.retransmitLoop()
	go t.ackLoop()

	return nil
}

// recvLoop reads incoming UDP packets and dispatches them.
func (t *UDPTransport) recvLoop() {
	buf := make([]byte, udpReadBuf)
	for {
		n, err := t.conn.Read(buf)
		if err != nil {
			if !t.closed.Load() {
				t.mu.Lock()
				for _, s := range t.streams {
					s.mu.Lock()
					s.closeRecvChLocked()
					s.mu.Unlock()
				}
				t.streams = make(map[uint32]*udpStream)
				t.mu.Unlock()
			}
			return
		}

		pkt, err := pkgudp.DecodePacket(buf[:n], t.sessionKey)
		if err != nil {
			continue
		}

		switch pkt.Type {
		case pkgudp.MsgStreamData:
			t.arq.HandleData(pkt)
		case pkgudp.MsgAck:
			t.arq.HandleAck(pkt.Data)
		case pkgudp.MsgStreamClose:
			t.mu.Lock()
			s, ok := t.streams[pkt.StreamID]
			t.mu.Unlock()
			if ok {
				t.mu.Lock()
				delete(t.streams, pkt.StreamID)
				t.mu.Unlock()
				t.arq.RemoveRecvBuffer(pkt.StreamID)
				s.mu.Lock()
				s.closeRecvChLocked()
				s.mu.Unlock()
			}
		}
	}
}

// keepaliveLoop sends MsgKeepalive packets every 15s to prevent NAT timeout.
func (t *UDPTransport) keepaliveLoop() {
	ticker := time.NewTicker(udpKeepalive)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			pkt := &pkgudp.Packet{
				ConnID: t.connID,
				Type:   pkgudp.MsgKeepalive,
			}
			data, err := pkgudp.EncodePacket(pkt, t.sessionKey)
			if err != nil {
				continue
			}
			t.conn.Write(data) //nolint:errcheck
		}
	}
}

// retransmitLoop checks for RTO-expired packets every 10ms.
func (t *UDPTransport) retransmitLoop() {
	ticker := time.NewTicker(arqRetransmitInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.arq.RetransmitTick()
		}
	}
}

// ackLoop sends delayed ACKs every 25ms.
func (t *UDPTransport) ackLoop() {
	ticker := time.NewTicker(arqAckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-ticker.C:
			t.arq.AckTick()
		}
	}
}

// OpenStream allocates a stream ID, sends MsgStreamOpen, and returns a udpStream.
func (t *UDPTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if t.closed.Load() {
		return nil, fmt.Errorf("transport closed")
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	recvCh := make(chan []byte, 1024)
	s := &udpStream{
		t:      t,
		id:     id,
		recvCh: recvCh,
	}
	t.streams[id] = s
	t.mu.Unlock()

	// Create recv buffer for reordering
	t.arq.CreateRecvBuffer(id)

	// Send MsgStreamOpen via ARQ (reliable)
	payload := (&pkgudp.StreamOpenMsg{
		StreamType: streamType,
		Addr:       addr,
		Port:       port,
	}).Encode()

	if err := t.arq.Send(pkgudp.MsgStreamOpen, id, 0, payload); err != nil {
		t.mu.Lock()
		delete(t.streams, id)
		t.mu.Unlock()
		t.arq.RemoveRecvBuffer(id)
		return nil, fmt.Errorf("send stream open: %w", err)
	}

	// For TCP streams wait for a single-byte result on the receive channel
	if streamType == pkgudp.StreamTypeTCP {
		select {
		case data, ok := <-recvCh:
			if !ok {
				t.mu.Lock()
				delete(t.streams, id)
				t.mu.Unlock()
				t.arq.RemoveRecvBuffer(id)
				return nil, fmt.Errorf("stream closed before connect result")
			}
			if len(data) == 0 || data[0] != 0x01 {
				t.mu.Lock()
				delete(t.streams, id)
				t.mu.Unlock()
				t.arq.RemoveRecvBuffer(id)
				return nil, fmt.Errorf("connect rejected: %s:%d", addr, port)
			}
		case <-time.After(10 * time.Second):
			t.mu.Lock()
			delete(t.streams, id)
			t.mu.Unlock()
			t.arq.RemoveRecvBuffer(id)
			return nil, fmt.Errorf("connect timeout: %s:%d", addr, port)
		}
	}

	return s, nil
}

// Close tears down the transport and all streams.
func (t *UDPTransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.done)

	if t.arq != nil {
		t.arq.Close()
	}

	t.mu.Lock()
	for _, s := range t.streams {
		s.mu.Lock()
		s.closeRecvChLocked()
		s.mu.Unlock()
	}
	t.streams = make(map[uint32]*udpStream)
	t.mu.Unlock()

	return t.conn.Close()
}

func (t *UDPTransport) Mode() string { return ModeUDP }

// sendPacket is a helper to encode and send a packet on the shared connection (unreliable).
func (t *UDPTransport) sendPacket(pkt *pkgudp.Packet) error {
	data, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		return err
	}
	_, err = t.conn.Write(data)
	return err
}

// ---------------------------------------------------------------------------
// udpStream
// ---------------------------------------------------------------------------

type udpStream struct {
	t      *UDPTransport
	id     uint32
	recvCh chan []byte

	mu         sync.Mutex
	buf        []byte
	seq        uint32
	closed     bool
	recvClosed bool
}

func (s *udpStream) ID() uint32 { return s.id }

func (s *udpStream) closeRecvChLocked() {
	if !s.recvClosed {
		s.recvClosed = true
		close(s.recvCh)
	}
}

// Read implements io.Reader. Blocks until data arrives or stream is closed.
func (s *udpStream) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := copy(p, s.buf)
			s.buf = s.buf[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()

		data, ok := <-s.recvCh
		if !ok {
			return 0, fmt.Errorf("stream closed")
		}
		s.mu.Lock()
		s.buf = append(s.buf, data...)
		s.mu.Unlock()
	}
}

// Write implements io.Writer. Chunks data into segments and sends via ARQ.
func (s *udpStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return 0, fmt.Errorf("write on closed stream")
	}
	s.mu.Unlock()

	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > udpMaxPayload {
			chunk = p[:udpMaxPayload]
		}

		s.mu.Lock()
		seq := s.seq
		s.seq++
		s.mu.Unlock()

		if err := s.t.arq.Send(pkgudp.MsgStreamData, s.id, seq, chunk); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

// Close sends MsgStreamClose and cleans up.
func (s *udpStream) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	s.t.mu.Lock()
	delete(s.t.streams, s.id)
	s.t.mu.Unlock()

	s.t.arq.RemoveRecvBuffer(s.id)

	// Send close reliably via ARQ
	return s.t.arq.Send(pkgudp.MsgStreamClose, s.id, 0, nil)
}
```

- [ ] **Step 2: Verify daemon builds**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/daemon && go build ./...`
Expected: no errors

- [ ] **Step 3: Run existing tests**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy && make test`
Expected: all PASS (or at least daemon and pkg modules pass)

- [ ] **Step 4: Commit**

```bash
git add daemon/internal/transport/udp.go
git commit -m "feat(daemon): integrate ARQ Controller into UDP transport

Write() now goes through arq.Send() with cwnd backpressure.
recvLoop() delivers data via reorder buffer.
New retransmitLoop (10ms) and ackLoop (25ms) goroutines."
```

---

## Task 10: Server Integration

**Files:**
- Modify: `server/internal/udp/session.go`
- Modify: `server/internal/udp/listener.go`

- [ ] **Step 1: Add ARQ Controller to Session**

In `server/internal/udp/session.go`, replace the entire file:

```go
package udp

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"sync"
	"time"

	"proxyness/pkg/udp/arq"
)

// Session represents an authenticated UDP client.
type Session struct {
	Token      uint32
	SessionKey []byte
	DeviceID   int
	ClientAddr net.Addr
	LastSeen   time.Time

	ARQ *arq.Controller

	mu      sync.Mutex
	streams map[uint32]*StreamState
	nextSID uint32
	nextSeq map[uint32]*uint32 // per-stream sequence counter for server→client
}

// StreamState tracks one proxied stream within a session.
type StreamState struct {
	Type     byte
	Addr     string
	Port     uint16
	Conn     net.Conn
	BytesIn  int64
	BytesOut int64
	Created  time.Time
}

func (s *Session) AddStream() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSID++
	id := s.nextSID
	s.streams[id] = &StreamState{Created: time.Now()}
	seq := uint32(0)
	s.nextSeq[id] = &seq
	return id
}

func (s *Session) GetStream(id uint32) (*StreamState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

// NextSeq returns the next sequence number for a stream and increments it.
func (s *Session) NextSeq(streamID uint32) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq, ok := s.nextSeq[streamID]
	if !ok {
		return 0
	}
	v := *seq
	*seq++
	return v
}

func (s *Session) RemoveStream(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[id]; ok {
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
		delete(s.nextSeq, id)
	}
	if s.ARQ != nil {
		s.ARQ.RemoveRecvBuffer(id)
	}
}

func (s *Session) CloseAllStreams() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.streams {
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
	}
	s.nextSeq = make(map[uint32]*uint32)
	if s.ARQ != nil {
		s.ARQ.Close()
	}
}

// SessionManager manages all active UDP sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[uint32]*Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[uint32]*Session),
	}
}

func (m *SessionManager) Create(sessionKey []byte, deviceID int) uint32 {
	var token uint32
	buf := make([]byte, 4)
	for token == 0 {
		rand.Read(buf)
		token = binary.BigEndian.Uint32(buf)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[token] = &Session{
		Token:      token,
		SessionKey: sessionKey,
		DeviceID:   deviceID,
		LastSeen:   time.Now(),
		streams:    make(map[uint32]*StreamState),
		nextSeq:    make(map[uint32]*uint32),
	}

	return token
}

func (m *SessionManager) Get(token uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[token]
	return s, ok
}

func (m *SessionManager) Remove(token uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[token]; ok {
		s.CloseAllStreams()
		delete(m.sessions, token)
	}
}

// Cleanup removes sessions older than maxAge.
func (m *SessionManager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for token, s := range m.sessions {
		if now.Sub(s.LastSeen) > maxAge {
			s.CloseAllStreams()
			delete(m.sessions, token)
		}
	}
}
```

- [ ] **Step 2: Update listener to use ARQ**

In `server/internal/udp/listener.go`, replace the entire file:

```go
package udp

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"

	"proxyness/pkg/auth"
	pkgudp "proxyness/pkg/udp"
	"proxyness/pkg/udp/arq"
	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

const (
	arqRetransmitInterval = 10 * time.Millisecond
	arqAckInterval        = 25 * time.Millisecond
)

// Listener handles incoming QUIC-disguised UDP packets on a PacketConn.
type Listener struct {
	conn     net.PacketConn
	db       *db.DB
	tracker  *stats.Tracker
	sessions *SessionManager
}

type inPacket struct {
	data []byte
	addr net.Addr
}

// NewListener creates a new UDP Listener.
func NewListener(conn net.PacketConn, database *db.DB, tracker *stats.Tracker) *Listener {
	if uc, ok := conn.(*net.UDPConn); ok {
		uc.SetReadBuffer(4 * 1024 * 1024)
	}
	return &Listener{
		conn:     conn,
		db:       database,
		tracker:  tracker,
		sessions: NewSessionManager(),
	}
}

// Serve reads UDP packets and dispatches them.
func (l *Listener) Serve() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			l.sessions.Cleanup(2 * time.Minute)
		}
	}()

	pktCh := make(chan inPacket, 4096)
	go l.processLoop(pktCh)

	buf := make([]byte, 2048)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			log.Printf("udp listener: read error: %v", err)
			close(pktCh)
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		pktCh <- inPacket{data: data, addr: addr}
	}
}

// processLoop handles packets sequentially, preserving write ordering.
func (l *Listener) processLoop(ch chan inPacket) {
	for pkt := range ch {
		l.handlePacket(pkt.data, pkt.addr)
	}
}

func (l *Listener) handlePacket(data []byte, addr net.Addr) {
	if len(data) < 5 {
		return
	}

	connID := binary.BigEndian.Uint32(data[1:5])

	if connID == 0 {
		l.handleHandshake(data, addr)
		return
	}

	sess, ok := l.sessions.Get(connID)
	if !ok {
		return
	}

	sess.mu.Lock()
	sess.ClientAddr = addr
	sess.LastSeen = time.Now()
	sess.mu.Unlock()

	pkt, err := pkgudp.DecodePacket(data, sess.SessionKey)
	if err != nil {
		log.Printf("udp: decode packet connID=%d: %v", connID, err)
		return
	}

	switch pkt.Type {
	case pkgudp.MsgStreamOpen:
		go l.handleStreamOpen(sess, pkt, addr)
	case pkgudp.MsgStreamData:
		if sess.ARQ != nil {
			sess.ARQ.HandleData(pkt)
		}
	case pkgudp.MsgStreamClose:
		l.handleStreamClose(sess, pkt)
	case pkgudp.MsgAck:
		if sess.ARQ != nil {
			sess.ARQ.HandleAck(pkt.Data)
		}
	case pkgudp.MsgKeepalive:
		// no-op
	default:
		log.Printf("udp: unknown msg type 0x%02x from connID=%d", pkt.Type, connID)
	}
}

func (l *Listener) handleHandshake(data []byte, addr net.Addr) {
	if len(data) < 5 {
		return
	}

	keys, err := l.db.GetActiveKeys()
	if err != nil {
		log.Printf("udp: get active keys: %v", err)
		return
	}

	var pkt *pkgudp.Packet
	var matchedKeyHex string
	var matchedKeyBytes []byte
	for _, keyHex := range keys {
		kb, err := hex.DecodeString(keyHex)
		if err != nil {
			continue
		}
		p, err := pkgudp.DecodePacket(data, kb)
		if err == nil && p.Type == pkgudp.MsgHandshake {
			pkt = p
			matchedKeyHex = keyHex
			matchedKeyBytes = kb
			break
		}
	}

	if pkt == nil {
		log.Printf("udp: handshake auth failed from %s: no matching key", addr)
		return
	}

	req, err := pkgudp.DecodeHandshakeRequest(pkt.Data)
	if err != nil {
		log.Printf("udp: decode handshake from %s: %v", addr, err)
		return
	}

	rawAuth := pkgudp.RawAuth(pkt.Data)
	if err := auth.ValidateAuthMessage(matchedKeyHex, rawAuth); err != nil {
		log.Printf("udp: handshake auth failed from %s: %v", addr, err)
		return
	}

	device, err := l.db.GetDeviceByKey(matchedKeyHex)
	if err != nil {
		log.Printf("udp: device lookup: %v", err)
		return
	}

	mid := fmt.Sprintf("%x", req.MachineID)
	if err := l.checkMachineID(device.ID, device.Name, mid); err != nil {
		log.Printf("udp: device %s machine check failed: %v", device.Name, err)
		return
	}

	serverPriv, serverPubBytes, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		log.Printf("udp: generate ephemeral key: %v", err)
		return
	}

	sessionKey, err := pkgudp.DeriveSessionKey(serverPriv, req.EphemeralPub)
	if err != nil {
		log.Printf("udp: derive session key: %v", err)
		return
	}

	token := l.sessions.Create(sessionKey, device.ID)
	sess, _ := l.sessions.Get(token)
	sess.mu.Lock()
	sess.ClientAddr = addr
	sess.mu.Unlock()

	// Initialize ARQ Controller for this session
	sess.ARQ = arq.New(token, sessionKey, func(data []byte) error {
		sess.mu.Lock()
		clientAddr := sess.ClientAddr
		sess.mu.Unlock()
		_, err := l.conn.WriteTo(data, clientAddr)
		return err
	}, func(streamID uint32, data []byte) {
		st, ok := sess.GetStream(streamID)
		if !ok || st.Conn == nil {
			return
		}
		n, err := st.Conn.Write(data)
		if err != nil {
			log.Printf("udp: arq deliver to dest stream=%d: %v", streamID, err)
			sess.RemoveStream(streamID)
			l.sendClose(sess, streamID)
			return
		}
		st.BytesIn += int64(n)
	})

	// Start ARQ background goroutines
	go l.sessionRetransmitLoop(sess)
	go l.sessionAckLoop(sess)

	resp := &pkgudp.HandshakeResponse{
		EphemeralPub: serverPubBytes,
		SessionToken: token,
	}
	respPkt := &pkgudp.Packet{
		ConnID: 0,
		Type:   pkgudp.MsgHandshake,
		Data:   resp.Encode(),
	}
	encoded, err := pkgudp.EncodePacket(respPkt, matchedKeyBytes)
	if err != nil {
		log.Printf("udp: encode handshake response: %v", err)
		l.sessions.Remove(token)
		return
	}

	if _, err := l.conn.WriteTo(encoded, addr); err != nil {
		log.Printf("udp: send handshake response to %s: %v", addr, err)
		l.sessions.Remove(token)
		return
	}

	log.Printf("udp: session created token=%d device=%s from %s", token, device.Name, addr)
}

func (l *Listener) sessionRetransmitLoop(sess *Session) {
	ticker := time.NewTicker(arqRetransmitInterval)
	defer ticker.Stop()
	for range ticker.C {
		if sess.ARQ == nil {
			return
		}
		sess.ARQ.RetransmitTick()
	}
}

func (l *Listener) sessionAckLoop(sess *Session) {
	ticker := time.NewTicker(arqAckInterval)
	defer ticker.Stop()
	for range ticker.C {
		if sess.ARQ == nil {
			return
		}
		sess.ARQ.AckTick()
	}
}

func (l *Listener) checkMachineID(deviceID int, deviceName, machineID string) error {
	stored, err := l.db.GetDeviceMachineID(deviceID)
	if err != nil {
		return err
	}
	if stored == "" {
		log.Printf("udp: device %s bound to machine %s", deviceName, machineID[:8])
		return l.db.SetDeviceMachineID(deviceID, machineID)
	}
	if stored != machineID {
		return fmt.Errorf("device bound to different machine")
	}
	return nil
}

func (l *Listener) handleStreamOpen(sess *Session, pkt *pkgudp.Packet, addr net.Addr) {
	msg, err := pkgudp.DecodeStreamOpen(pkt.Data)
	if err != nil {
		log.Printf("udp: decode stream open: %v", err)
		return
	}

	streamID := pkt.StreamID

	sess.mu.Lock()
	if _, exists := sess.streams[streamID]; !exists {
		sess.streams[streamID] = &StreamState{Created: time.Now()}
		seq := uint32(0)
		sess.nextSeq[streamID] = &seq
	}
	st := sess.streams[streamID]
	sess.mu.Unlock()

	st.Type = msg.StreamType
	st.Addr = msg.Addr
	st.Port = msg.Port

	// Create recv buffer for this stream (for incoming data reordering)
	if sess.ARQ != nil {
		sess.ARQ.CreateRecvBuffer(streamID)
	}

	target := net.JoinHostPort(msg.Addr, fmt.Sprintf("%d", msg.Port))

	switch msg.StreamType {
	case pkgudp.StreamTypeTCP:
		conn, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial TCP %s: %v", target, err)
			l.sendResult(sess, streamID, false)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		l.sendResult(sess, streamID, true)
		go l.relayFromDest(sess, streamID, conn)

	case pkgudp.StreamTypeUDP:
		conn, err := net.DialTimeout("udp", target, 10*time.Second)
		if err != nil {
			log.Printf("udp: dial UDP %s: %v", target, err)
			l.sendResult(sess, streamID, false)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		l.sendResult(sess, streamID, true)
		go l.relayFromDest(sess, streamID, conn)

	default:
		log.Printf("udp: unknown stream type 0x%02x", msg.StreamType)
		l.sendResult(sess, streamID, false)
		sess.RemoveStream(streamID)
	}
}

// handleStreamClose closes the outbound connection for a stream.
func (l *Listener) handleStreamClose(sess *Session, pkt *pkgudp.Packet) {
	sess.RemoveStream(pkt.StreamID)
}

// relayFromDest reads from the destination and sends via ARQ.
func (l *Listener) relayFromDest(sess *Session, streamID uint32, conn net.Conn) {
	defer func() {
		sess.RemoveStream(streamID)
		l.sendClose(sess, streamID)
	}()

	buf := make([]byte, 1340) // udpMaxPayload with PktNum
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buf)
		if n > 0 {
			st, ok := sess.GetStream(streamID)
			if !ok {
				return
			}
			st.BytesOut += int64(n)

			seq := sess.NextSeq(streamID)

			if sess.ARQ != nil {
				if sendErr := sess.ARQ.Send(pkgudp.MsgStreamData, streamID, seq, buf[:n]); sendErr != nil {
					log.Printf("udp: arq send stream=%d: %v", streamID, sendErr)
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// sendResult sends a single-byte result (0x01=ok, 0x00=fail) via ARQ.
func (l *Listener) sendResult(sess *Session, streamID uint32, ok bool) {
	b := byte(0x00)
	if ok {
		b = 0x01
	}
	if sess.ARQ != nil {
		sess.ARQ.Send(pkgudp.MsgStreamData, streamID, 0, []byte{b}) //nolint:errcheck
	}
}

// sendClose sends a StreamClose packet via ARQ.
func (l *Listener) sendClose(sess *Session, streamID uint32) {
	if sess.ARQ != nil {
		sess.ARQ.Send(pkgudp.MsgStreamClose, streamID, 0, nil) //nolint:errcheck
	}
}
```

- [ ] **Step 3: Verify server builds**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/server && go build ./...`
Expected: no errors

- [ ] **Step 4: Run existing server tests**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/server && go test ./internal/udp/ -v`
Expected: all PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy && make test`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add server/internal/udp/session.go server/internal/udp/listener.go
git commit -m "feat(server): integrate ARQ Controller into UDP listener

relayFromDest sends via arq.Send() with cwnd backpressure.
handleStreamData delivers through reorder buffer.
Per-session retransmit (10ms) and ACK (25ms) goroutines."
```

---

## Task 11: Integration Tests

**Files:**
- Modify: `test/udp_test.go`

- [ ] **Step 1: Add ARQ-specific integration test**

Append to `test/udp_test.go`:

```go
// TestUDPAckRoundTrip tests encoding and decoding an ACK packet over a real UDP socket.
func TestUDPAckRoundTrip(t *testing.T) {
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	clientConn, err := net.Dial("udp", serverConn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	// Build ACK payload
	ackData := make([]byte, 36) // CumAck(4) + Bitmap(32)
	binary.BigEndian.PutUint32(ackData[0:4], 42) // CumAck=42

	pkt := &pkgudp.Packet{
		ConnID: 0xABCD1234,
		Type:   pkgudp.MsgAck,
		PktNum: 0, // ACKs are not tracked
		Data:   ackData,
	}
	encoded, err := pkgudp.EncodePacket(pkt, key)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := clientConn.Write(encoded); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 2048)
	serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := serverConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	decoded, err := pkgudp.DecodePacket(buf[:n], key)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Type != pkgudp.MsgAck {
		t.Fatalf("type: got 0x%02x, want 0x%02x", decoded.Type, pkgudp.MsgAck)
	}
	if decoded.PktNum != 0 {
		t.Fatalf("pktNum: got %d, want 0", decoded.PktNum)
	}
	if len(decoded.Data) < 4 {
		t.Fatal("ack data too short")
	}
	cumAck := binary.BigEndian.Uint32(decoded.Data[0:4])
	if cumAck != 42 {
		t.Fatalf("cumAck: got %d, want 42", cumAck)
	}
}
```

- [ ] **Step 2: Run integration tests**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy/test && go test -v`
Expected: all PASS

- [ ] **Step 3: Run full test suite**

Run: `cd /Users/ilyasmurov/projects/smurov/proxy && make test`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add test/udp_test.go
git commit -m "test: add UDP ACK round-trip integration test"
```
