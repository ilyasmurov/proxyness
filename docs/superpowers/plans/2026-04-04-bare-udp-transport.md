# Bare UDP Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add QUIC-disguised UDP transport with multiplexed streams, forward secrecy, and TLS fallback alongside the existing TCP/TLS transport.

**Architecture:** New `pkg/udp/` package implements the wire protocol (QUIC header, XChaCha20-Poly1305 encryption, ECDH handshake, stream multiplexing). `daemon/internal/transport/` provides a `Transport` interface with UDP, TLS, and Auto implementations. Server adds a UDP listener on port 443 alongside existing TCP. Existing SOCKS5/TUN routing logic is unchanged — only the transport pipe to the server is swapped.

**Tech Stack:** Go (`golang.org/x/crypto/chacha20poly1305`, `crypto/ecdh`), gVisor netstack (unchanged), Electron/React (transport selector UI)

**Spec:** `docs/superpowers/specs/2026-04-04-bare-udp-transport-design.md`

---

### Task 1: Encryption Primitives

**Files:**
- Create: `pkg/udp/crypto.go`
- Create: `pkg/udp/crypto_test.go`

- [ ] **Step 1: Write failing tests for encrypt/decrypt**

```go
// pkg/udp/crypto_test.go
package udp

import (
	"crypto/rand"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	plaintext := []byte("hello world")

	ciphertext, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatal(err)
	}

	// ciphertext = 24-byte nonce + encrypted + 16-byte tag
	if len(ciphertext) != 24+len(plaintext)+16 {
		t.Fatalf("wrong ciphertext length: %d", len(ciphertext))
	}

	decrypted, err := Decrypt(key, ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	if string(decrypted) != "hello world" {
		t.Fatalf("got %q", decrypted)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1 := make([]byte, 32)
	key2 := make([]byte, 32)
	rand.Read(key1)
	rand.Read(key2)

	ciphertext, _ := Encrypt(key1, []byte("secret"))
	_, err := Decrypt(key2, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting with wrong key")
	}
}

func TestDecryptTampered(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)

	ciphertext, _ := Encrypt(key, []byte("secret"))
	ciphertext[len(ciphertext)-1] ^= 0xff // flip last byte

	_, err := Decrypt(key, ciphertext)
	if err == nil {
		t.Fatal("expected error decrypting tampered ciphertext")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd pkg && go test ./udp/ -run TestEncrypt -v`
Expected: FAIL — `Encrypt` not defined

- [ ] **Step 3: Implement encryption**

```go
// pkg/udp/crypto.go
package udp

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// Encrypt encrypts plaintext with XChaCha20-Poly1305.
// Returns: [24-byte nonce][ciphertext + 16-byte tag].
func Encrypt(key, plaintext []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("new xchacha20: %w", err)
	}

	nonce := make([]byte, aead.NonceSize()) // 24 bytes
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}

	// nonce + Seal appends ciphertext+tag to nonce
	return aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts a packet produced by Encrypt.
func Decrypt(key, data []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("new xchacha20: %w", err)
	}

	nonceSize := aead.NonceSize()
	if len(data) < nonceSize+aead.Overhead() {
		return nil, fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return aead.Open(nil, nonce, ciphertext, nil)
}
```

- [ ] **Step 4: Add `golang.org/x/crypto` dependency**

Run: `cd pkg && go get golang.org/x/crypto`

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd pkg && go test ./udp/ -v`
Expected: PASS (3 tests)

- [ ] **Step 6: Commit**

```bash
git add pkg/udp/ pkg/go.mod pkg/go.sum
git commit -m "feat(udp): add XChaCha20-Poly1305 encryption primitives"
```

---

### Task 2: QUIC-Disguised Packet Format

**Files:**
- Create: `pkg/udp/packet.go`
- Create: `pkg/udp/packet_test.go`

- [ ] **Step 1: Write failing tests for packet encode/decode**

```go
// pkg/udp/packet_test.go
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
		StreamID: 42,
		Seq:      7,
		Data:     []byte("hello"),
	}

	encoded, err := EncodePacket(pkt, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	// First byte should have QUIC flag (0x40 set)
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

func TestPacketHandshakeNoEncryption(t *testing.T) {
	pkt := &Packet{
		ConnID: 0, // handshake
		Type:   MsgHandshake,
		Data:   []byte("handshake-payload"),
	}

	// Handshake packets use device key, not session key
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

Run: `cd pkg && go test ./udp/ -run TestPacket -v`
Expected: FAIL — `Packet` not defined

- [ ] **Step 3: Implement packet format**

```go
// pkg/udp/packet.go
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
	StreamID uint32
	Seq      uint32
	Data     []byte
}

// EncodePacket encodes a Packet into a QUIC-disguised UDP datagram.
//
// Wire format:
//   [1 byte:  QUIC flags (0x40 | random)]
//   [4 bytes: Connection ID]
//   [N bytes: Encrypted(Type + StreamID + Seq + DataLen + Data)]
func EncodePacket(p *Packet, key []byte) ([]byte, error) {
	// Inner payload: type(1) + streamID(4) + seq(4) + dataLen(2) + data(N)
	inner := make([]byte, 1+4+4+2+len(p.Data))
	inner[0] = p.Type
	binary.BigEndian.PutUint32(inner[1:5], p.StreamID)
	binary.BigEndian.PutUint32(inner[5:9], p.Seq)
	binary.BigEndian.PutUint16(inner[9:11], uint16(len(p.Data)))
	copy(inner[11:], p.Data)

	encrypted, err := Encrypt(key, inner)
	if err != nil {
		return nil, err
	}

	// Outer: flags(1) + connID(4) + encrypted
	out := make([]byte, 1+4+len(encrypted))
	randByte := make([]byte, 1)
	rand.Read(randByte)
	out[0] = 0x40 | (randByte[0] & 0x3f) // QUIC flag set
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

	if len(inner) < 11 {
		return nil, fmt.Errorf("inner payload too short: %d bytes", len(inner))
	}

	dataLen := binary.BigEndian.Uint16(inner[9:11])
	if len(inner) < 11+int(dataLen) {
		return nil, fmt.Errorf("data truncated: have %d, need %d", len(inner)-11, dataLen)
	}

	return &Packet{
		ConnID:   connID,
		Type:     inner[0],
		StreamID: binary.BigEndian.Uint32(inner[1:5]),
		Seq:      binary.BigEndian.Uint32(inner[5:9]),
		Data:     inner[11 : 11+dataLen],
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd pkg && go test ./udp/ -v`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/packet.go pkg/udp/packet_test.go
git commit -m "feat(udp): add QUIC-disguised packet encode/decode"
```

---

### Task 3: ECDH Handshake

**Files:**
- Create: `pkg/udp/handshake.go`
- Create: `pkg/udp/handshake_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pkg/udp/handshake_test.go
package udp

import (
	"testing"
)

func TestHandshakeKeyExchange(t *testing.T) {
	// Client generates ephemeral keypair
	clientPriv, clientPub, err := GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	// Server generates ephemeral keypair
	serverPriv, serverPub, err := GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	// Both derive the same session key
	clientSessionKey, err := DeriveSessionKey(clientPriv, serverPub)
	if err != nil {
		t.Fatal(err)
	}

	serverSessionKey, err := DeriveSessionKey(serverPriv, clientPub)
	if err != nil {
		t.Fatal(err)
	}

	if len(clientSessionKey) != 32 {
		t.Fatalf("key length: %d", len(clientSessionKey))
	}

	for i := range clientSessionKey {
		if clientSessionKey[i] != serverSessionKey[i] {
			t.Fatal("session keys do not match")
		}
	}
}

func TestHandshakeRequestEncodeDecode(t *testing.T) {
	_, pub, _ := GenerateEphemeralKey()
	deviceKey := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"
	machineID := [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	req := &HandshakeRequest{
		EphemeralPub: pub,
		DeviceKey:    deviceKey,
		MachineID:    machineID,
	}

	data, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodeHandshakeRequest(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.MachineID != machineID {
		t.Fatal("machine ID mismatch")
	}

	// DeviceKey is not decoded — server validates auth via ValidateAuthMessageMulti
	// Verify the raw auth bytes can be extracted for validation
	encoded, _ := req.Encode()
	rawAuth := RawAuth(encoded)
	if len(rawAuth) != 41 {
		t.Fatalf("raw auth length: %d", len(rawAuth))
	}
}

func TestHandshakeResponseEncodeDecode(t *testing.T) {
	_, pub, _ := GenerateEphemeralKey()

	resp := &HandshakeResponse{
		EphemeralPub: pub,
		SessionToken: 0xDEADBEEF,
	}

	data := resp.Encode()

	decoded, err := DecodeHandshakeResponse(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.SessionToken != 0xDEADBEEF {
		t.Fatalf("token: got %x", decoded.SessionToken)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd pkg && go test ./udp/ -run TestHandshake -v`
Expected: FAIL

- [ ] **Step 3: Implement handshake**

```go
// pkg/udp/handshake.go
package udp

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"golang.org/x/crypto/hkdf"
	"io"

	"proxyness/pkg/auth"
)

// GenerateEphemeralKey generates an X25519 ephemeral keypair.
func GenerateEphemeralKey() (*ecdh.PrivateKey, []byte, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return priv, priv.PublicKey().Bytes(), nil
}

// DeriveSessionKey performs ECDH and derives a 32-byte session key via HKDF.
func DeriveSessionKey(priv *ecdh.PrivateKey, peerPub []byte) ([]byte, error) {
	pub, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, fmt.Errorf("parse peer public key: %w", err)
	}

	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, fmt.Errorf("ecdh: %w", err)
	}

	hk := hkdf.New(sha256.New, shared, nil, []byte("proxyness-udp-session"))
	sessionKey := make([]byte, 32)
	if _, err := io.ReadFull(hk, sessionKey); err != nil {
		return nil, err
	}

	return sessionKey, nil
}

// HandshakeRequest is sent by the client to establish a UDP session.
type HandshakeRequest struct {
	EphemeralPub []byte   // 32 bytes X25519 public key
	DeviceKey    string   // hex-encoded device key (for HMAC auth)
	MachineID    [16]byte // hardware fingerprint
}

// Encode serializes HandshakeRequest: pubkey(32) + auth(41) + machineID(16) = 89 bytes.
func (r *HandshakeRequest) Encode() ([]byte, error) {
	authMsg, err := auth.CreateAuthMessage(r.DeviceKey)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 32+len(authMsg)+16)
	copy(buf[0:32], r.EphemeralPub)
	copy(buf[32:32+len(authMsg)], authMsg)
	copy(buf[32+len(authMsg):], r.MachineID[:])

	return buf, nil
}

// DecodeHandshakeRequest parses a HandshakeRequest from raw bytes.
func DecodeHandshakeRequest(data []byte) (*HandshakeRequest, error) {
	if len(data) < 32+41+16 {
		return nil, fmt.Errorf("handshake request too short: %d", len(data))
	}

	return &HandshakeRequest{
		EphemeralPub: data[0:32],
		DeviceKey:    "", // server validates auth via ValidateAuthMessageMulti
		MachineID:    [16]byte(data[73:89]),
	}, nil
}

// RawAuth returns the 41-byte auth message from encoded request.
func RawAuth(encoded []byte) []byte {
	return encoded[32:73]
}

// HandshakeResponse is sent by the server after successful auth.
type HandshakeResponse struct {
	EphemeralPub []byte // 32 bytes X25519 public key
	SessionToken uint32 // becomes Connection ID for all future packets
}

// Encode serializes HandshakeResponse: pubkey(32) + token(4) = 36 bytes.
func (r *HandshakeResponse) Encode() []byte {
	buf := make([]byte, 36)
	copy(buf[0:32], r.EphemeralPub)
	binary.BigEndian.PutUint32(buf[32:36], r.SessionToken)
	return buf
}

// DecodeHandshakeResponse parses a HandshakeResponse from raw bytes.
func DecodeHandshakeResponse(data []byte) (*HandshakeResponse, error) {
	if len(data) < 36 {
		return nil, fmt.Errorf("handshake response too short: %d", len(data))
	}

	return &HandshakeResponse{
		EphemeralPub: data[0:32],
		SessionToken: binary.BigEndian.Uint32(data[32:36]),
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd pkg && go test ./udp/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/handshake.go pkg/udp/handshake_test.go
git commit -m "feat(udp): add ECDH handshake with forward secrecy"
```

---

### Task 4: Stream Multiplexing Messages

**Files:**
- Create: `pkg/udp/stream.go`
- Create: `pkg/udp/stream_test.go`

- [ ] **Step 1: Write failing tests**

```go
// pkg/udp/stream_test.go
package udp

import (
	"testing"
)

func TestStreamOpenEncodeDecode(t *testing.T) {
	msg := &StreamOpenMsg{
		StreamType: StreamTypeTCP,
		Addr:       "example.com",
		Port:       443,
	}

	data := msg.Encode()

	decoded, err := DecodeStreamOpen(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.StreamType != StreamTypeTCP {
		t.Fatalf("type: got %d", decoded.StreamType)
	}
	if decoded.Addr != "example.com" {
		t.Fatalf("addr: got %q", decoded.Addr)
	}
	if decoded.Port != 443 {
		t.Fatalf("port: got %d", decoded.Port)
	}
}

func TestStreamOpenIPv4(t *testing.T) {
	msg := &StreamOpenMsg{
		StreamType: StreamTypeUDP,
		Addr:       "1.2.3.4",
		Port:       8080,
	}

	data := msg.Encode()
	decoded, err := DecodeStreamOpen(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Addr != "1.2.3.4" {
		t.Fatalf("addr: got %q", decoded.Addr)
	}
	if decoded.Port != 8080 {
		t.Fatalf("port: got %d", decoded.Port)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd pkg && go test ./udp/ -run TestStream -v`
Expected: FAIL

- [ ] **Step 3: Implement stream messages**

```go
// pkg/udp/stream.go
package udp

import (
	"encoding/binary"
	"fmt"
	"net"
)

const (
	StreamTypeTCP byte = 0x01
	StreamTypeUDP byte = 0x02
)

// StreamOpenMsg is the payload of a MsgStreamOpen packet.
type StreamOpenMsg struct {
	StreamType byte   // StreamTypeTCP or StreamTypeUDP
	Addr       string // destination host (IP or domain)
	Port       uint16
}

// Encode serializes StreamOpenMsg: streamType(1) + address encoding.
// Address encoding reuses existing format: addrType(1) + addr + port(2).
func (m *StreamOpenMsg) Encode() []byte {
	var buf []byte

	if ip := net.ParseIP(m.Addr); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			buf = make([]byte, 1+1+4+2)
			buf[0] = m.StreamType
			buf[1] = 0x01 // IPv4
			copy(buf[2:6], ip4)
			binary.BigEndian.PutUint16(buf[6:8], m.Port)
		} else {
			buf = make([]byte, 1+1+16+2)
			buf[0] = m.StreamType
			buf[1] = 0x04 // IPv6
			copy(buf[2:18], ip.To16())
			binary.BigEndian.PutUint16(buf[18:20], m.Port)
		}
	} else {
		buf = make([]byte, 1+1+1+len(m.Addr)+2)
		buf[0] = m.StreamType
		buf[1] = 0x03 // domain
		buf[2] = byte(len(m.Addr))
		copy(buf[3:3+len(m.Addr)], m.Addr)
		binary.BigEndian.PutUint16(buf[3+len(m.Addr):], m.Port)
	}

	return buf
}

// DecodeStreamOpen parses a StreamOpenMsg from raw bytes.
func DecodeStreamOpen(data []byte) (*StreamOpenMsg, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("stream open too short")
	}

	msg := &StreamOpenMsg{StreamType: data[0]}
	addrType := data[1]

	switch addrType {
	case 0x01: // IPv4
		if len(data) < 8 {
			return nil, fmt.Errorf("ipv4 too short")
		}
		msg.Addr = net.IP(data[2:6]).String()
		msg.Port = binary.BigEndian.Uint16(data[6:8])
	case 0x04: // IPv6
		if len(data) < 20 {
			return nil, fmt.Errorf("ipv6 too short")
		}
		msg.Addr = net.IP(data[2:18]).String()
		msg.Port = binary.BigEndian.Uint16(data[18:20])
	case 0x03: // domain
		dlen := int(data[2])
		if len(data) < 3+dlen+2 {
			return nil, fmt.Errorf("domain too short")
		}
		msg.Addr = string(data[3 : 3+dlen])
		msg.Port = binary.BigEndian.Uint16(data[3+dlen : 5+dlen])
	default:
		return nil, fmt.Errorf("unknown addr type: 0x%02x", addrType)
	}

	return msg, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd pkg && go test ./udp/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/udp/stream.go pkg/udp/stream_test.go
git commit -m "feat(udp): add stream open/close message encoding"
```

---

### Task 5: Transport Interface and TLS Wrapper

**Files:**
- Create: `daemon/internal/transport/transport.go`
- Create: `daemon/internal/transport/tls.go`

- [ ] **Step 1: Define Transport and Stream interfaces**

```go
// daemon/internal/transport/transport.go
package transport

import "io"

// Mode constants for transport selection.
const (
	ModeAuto = "auto"
	ModeUDP  = "udp"
	ModeTLS  = "tls"
)

// Transport abstracts the connection to the proxy server.
type Transport interface {
	// Connect establishes a session with the server.
	Connect(server, key string, machineID [16]byte) error

	// OpenStream opens a new proxied stream to the given destination.
	// msgType is StreamTypeTCP (0x01) or StreamTypeUDP (0x02).
	OpenStream(streamType byte, addr string, port uint16) (Stream, error)

	// Close tears down the transport and all streams.
	Close() error

	// Mode returns the active transport type ("udp" or "tls").
	Mode() string
}

// Stream is a single proxied connection within a Transport.
type Stream interface {
	io.ReadWriteCloser
	ID() uint32
}
```

- [ ] **Step 2: Implement TLS transport wrapping existing code**

```go
// daemon/internal/transport/tls.go
package transport

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"proxyness/pkg/auth"
	"proxyness/pkg/proto"
)

// TLSTransport wraps the existing per-connection TLS approach.
// Each OpenStream creates a new TLS connection (same as current behavior).
type TLSTransport struct {
	server    string
	key       string
	machineID [16]byte
	connected atomic.Bool
	mu        sync.Mutex
	nextID    uint32
}

func NewTLSTransport() *TLSTransport {
	return &TLSTransport{}
}

func (t *TLSTransport) Connect(server, key string, machineID [16]byte) error {
	t.server = server
	t.key = key
	t.machineID = machineID

	// Verify server is reachable with a test auth
	conn, err := t.dial()
	if err != nil {
		return err
	}
	conn.Close()

	t.connected.Store(true)
	return nil
}

func (t *TLSTransport) dial() (net.Conn, error) {
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10e9},
		"tcp", t.server,
		&tls.Config{InsecureSkipVerify: true},
	)
	if err != nil {
		return nil, fmt.Errorf("tls dial: %w", err)
	}

	if err := proto.WriteAuth(conn, t.key); err != nil {
		conn.Close()
		return nil, fmt.Errorf("auth: %w", err)
	}
	ok, err := proto.ReadResult(conn)
	if err != nil || !ok {
		conn.Close()
		return nil, fmt.Errorf("auth rejected")
	}

	if err := proto.WriteMachineID(conn, t.machineID); err != nil {
		conn.Close()
		return nil, fmt.Errorf("machine id: %w", err)
	}
	ok, err = proto.ReadResult(conn)
	if err != nil || !ok {
		conn.Close()
		return nil, fmt.Errorf("machine id rejected")
	}

	return conn, nil
}

func (t *TLSTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if !t.connected.Load() {
		return nil, fmt.Errorf("not connected")
	}

	conn, err := t.dial()
	if err != nil {
		return nil, err
	}

	msgType := proto.MsgTypeTCP
	if streamType == 0x02 {
		msgType = proto.MsgTypeUDP
	}

	if err := proto.WriteMsgType(conn, msgType); err != nil {
		conn.Close()
		return nil, err
	}

	// For TCP, send connect and read result
	if streamType == 0x01 {
		if err := proto.WriteConnect(conn, addr, port); err != nil {
			conn.Close()
			return nil, err
		}
		ok, err := proto.ReadResult(conn)
		if err != nil || !ok {
			conn.Close()
			return nil, fmt.Errorf("connect rejected: %s:%d", addr, port)
		}
	}

	t.mu.Lock()
	t.nextID++
	id := t.nextID
	t.mu.Unlock()

	return &tlsStream{conn: conn, id: id}, nil
}

func (t *TLSTransport) Close() error {
	t.connected.Store(false)
	return nil
}

func (t *TLSTransport) Mode() string { return ModeTLS }

type tlsStream struct {
	conn net.Conn
	id   uint32
}

func (s *tlsStream) Read(p []byte) (int, error)  { return s.conn.Read(p) }
func (s *tlsStream) Write(p []byte) (int, error) { return s.conn.Write(p) }
func (s *tlsStream) Close() error                { return s.conn.Close() }
func (s *tlsStream) ID() uint32                  { return s.id }
```

- [ ] **Step 3: Verify daemon module compiles**

Run: `cd daemon && go build ./...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add daemon/internal/transport/
git commit -m "feat(transport): add Transport interface and TLS implementation"
```

---

### Task 6: Server Session Manager

**Files:**
- Create: `server/internal/udp/session.go`
- Create: `server/internal/udp/session_test.go`

- [ ] **Step 1: Write failing tests for session lifecycle**

```go
// server/internal/udp/session_test.go
package udp

import (
	"crypto/rand"
	"testing"
	"time"
)

func TestSessionManagerCreateAndLookup(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)
	if token == 0 {
		t.Fatal("token should not be 0")
	}

	sess, ok := sm.Get(token)
	if !ok {
		t.Fatal("session not found")
	}

	if sess.DeviceID != 1 {
		t.Fatalf("deviceID: got %d", sess.DeviceID)
	}
}

func TestSessionManagerExpiry(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)

	// Manually expire
	sm.mu.Lock()
	sm.sessions[token].LastSeen = time.Now().Add(-3 * time.Minute)
	sm.mu.Unlock()

	sm.Cleanup(2 * time.Minute)

	_, ok := sm.Get(token)
	if ok {
		t.Fatal("expired session should be removed")
	}
}

func TestSessionManagerOpenCloseStream(t *testing.T) {
	sm := NewSessionManager()

	key := make([]byte, 32)
	rand.Read(key)

	token := sm.Create(key, 1)
	sess, _ := sm.Get(token)

	streamID := sess.AddStream()
	if streamID == 0 {
		t.Fatal("streamID should not be 0")
	}

	st, ok := sess.GetStream(streamID)
	if !ok || st == nil {
		t.Fatal("stream not found")
	}

	sess.RemoveStream(streamID)
	_, ok = sess.GetStream(streamID)
	if ok {
		t.Fatal("stream should be removed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd server && go test ./internal/udp/ -v`
Expected: FAIL

- [ ] **Step 3: Implement session manager**

```go
// server/internal/udp/session.go
package udp

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"sync"
	"time"
)

// Session represents an authenticated UDP client.
type Session struct {
	Token      uint32
	SessionKey []byte
	DeviceID   int
	ClientAddr net.Addr
	LastSeen   time.Time

	mu       sync.Mutex
	streams  map[uint32]*StreamState
	nextSID  uint32
}

// StreamState tracks one proxied stream within a session.
type StreamState struct {
	Type     byte // 0x01=TCP, 0x02=UDP
	Addr     string
	Port     uint16
	Conn     net.Conn // outbound connection to destination
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
	return id
}

func (s *Session) GetStream(id uint32) (*StreamState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

func (s *Session) RemoveStream(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[id]; ok {
		if st.Conn != nil {
			st.Conn.Close()
		}
		delete(s.streams, id)
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

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && go test ./internal/udp/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add server/internal/udp/
git commit -m "feat(server): add UDP session manager"
```

---

### Task 7: Server UDP Listener

**Files:**
- Create: `server/internal/udp/listener.go`
- Modify: `server/internal/mux/mux.go` — add UDP listener startup

- [ ] **Step 1: Implement UDP listener with handshake + dispatch**

```go
// server/internal/udp/listener.go
package udp

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"time"

	"proxyness/pkg/auth"
	pkgudp "proxyness/pkg/udp"
	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

// Listener handles incoming UDP packets on port 443.
type Listener struct {
	conn     net.PacketConn
	db       *db.DB
	tracker  *stats.Tracker
	sessions *SessionManager
}

func NewListener(conn net.PacketConn, database *db.DB, tracker *stats.Tracker) *Listener {
	return &Listener{
		conn:     conn,
		db:       database,
		tracker:  tracker,
		sessions: NewSessionManager(),
	}
}

// Serve reads UDP packets and dispatches them.
func (l *Listener) Serve() {
	// Session cleanup every 30s
	go func() {
		for {
			time.Sleep(30 * time.Second)
			l.sessions.Cleanup(2 * time.Minute)
		}
	}()

	buf := make([]byte, 1500)
	for {
		n, addr, err := l.conn.ReadFrom(buf)
		if err != nil {
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		go l.handlePacket(data, addr)
	}
}

func (l *Listener) handlePacket(data []byte, addr net.Addr) {
	if len(data) < 5 {
		return
	}

	// Extract Connection ID from outer header (bytes 1-4)
	connID := uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])

	if connID == 0 {
		l.handleHandshake(data, addr)
		return
	}

	sess, ok := l.sessions.Get(connID)
	if !ok {
		return // unknown session, drop
	}

	sess.LastSeen = time.Now()
	sess.ClientAddr = addr

	pkt, err := pkgudp.DecodePacket(data, sess.SessionKey)
	if err != nil {
		return // decrypt failed, drop
	}

	switch pkt.Type {
	case pkgudp.MsgStreamOpen:
		l.handleStreamOpen(sess, pkt, addr)
	case pkgudp.MsgStreamData:
		l.handleStreamData(sess, pkt)
	case pkgudp.MsgStreamClose:
		sess.RemoveStream(pkt.StreamID)
	case pkgudp.MsgKeepalive:
		// LastSeen already updated
	}
}

func (l *Listener) handleHandshake(data []byte, addr net.Addr) {
	keys, err := l.db.GetActiveKeys()
	if err != nil || len(keys) == 0 {
		return
	}

	// Try each device key to decrypt the handshake
	var decryptedData []byte
	var matchedKey string
	for _, k := range keys {
		keyBytes, err := hex.DecodeString(k)
		if err != nil || len(keyBytes) != 32 {
			continue
		}
		pkt, err := pkgudp.DecodePacket(data, keyBytes)
		if err != nil {
			continue
		}
		if pkt.Type != pkgudp.MsgHandshake {
			continue
		}
		decryptedData = pkt.Data
		matchedKey = k
		break
	}

	if decryptedData == nil {
		return
	}

	// Parse handshake request
	req, err := pkgudp.DecodeHandshakeRequest(decryptedData)
	if err != nil {
		return
	}

	// Validate auth
	rawAuth := pkgudp.RawAuth(decryptedData)
	if err := auth.ValidateAuthMessage(rawAuth, matchedKey); err != nil {
		return
	}

	// Look up device
	device, err := l.db.GetDeviceByKey(matchedKey)
	if err != nil {
		return
	}

	// Check machine ID
	storedMID, _ := l.db.GetDeviceMachineID(device.ID)
	reqMID := fmt.Sprintf("%x", req.MachineID)
	if storedMID != "" && storedMID != reqMID {
		return // machine ID mismatch
	}
	if storedMID == "" {
		l.db.SetDeviceMachineID(device.ID, reqMID)
	}

	// Generate server ephemeral key and derive session key
	serverPriv, serverPub, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		return
	}

	sessionKey, err := pkgudp.DeriveSessionKey(serverPriv, req.EphemeralPub)
	if err != nil {
		return
	}

	// Create session
	token := l.sessions.Create(sessionKey, device.ID)

	// Send response encrypted with device key
	resp := &pkgudp.HandshakeResponse{
		EphemeralPub: serverPub,
		SessionToken: token,
	}

	keyBytes, _ := hex.DecodeString(matchedKey)
	respPkt := &pkgudp.Packet{
		ConnID: 0,
		Type:   pkgudp.MsgHandshake,
		Data:   resp.Encode(),
	}
	encoded, err := pkgudp.EncodePacket(respPkt, keyBytes)
	if err != nil {
		return
	}

	l.conn.WriteTo(encoded, addr)

	// Store client address
	if sess, ok := l.sessions.Get(token); ok {
		sess.ClientAddr = addr
	}
}

func (l *Listener) handleStreamOpen(sess *Session, pkt *pkgudp.Packet, addr net.Addr) {
	msg, err := pkgudp.DecodeStreamOpen(pkt.Data)
	if err != nil {
		return
	}

	streamID := sess.AddStream()
	st, _ := sess.GetStream(streamID)
	st.Type = msg.StreamType
	st.Addr = msg.Addr
	st.Port = msg.Port

	target := fmt.Sprintf("%s:%d", msg.Addr, msg.Port)

	if msg.StreamType == pkgudp.StreamTypeTCP {
		conn, err := net.DialTimeout("tcp", target, 10*time.Second)
		if err != nil {
			l.sendResult(sess, pkt.StreamID, false, addr)
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn
		l.sendResult(sess, streamID, true, addr)

		// Spawn goroutine to read from destination and send back
		go l.relayFromDest(sess, streamID, conn, addr)
	} else {
		conn, err := net.Dial("udp", target)
		if err != nil {
			sess.RemoveStream(streamID)
			return
		}
		st.Conn = conn

		go l.relayFromDest(sess, streamID, conn, addr)
	}
}

func (l *Listener) handleStreamData(sess *Session, pkt *pkgudp.Packet) {
	st, ok := sess.GetStream(pkt.StreamID)
	if !ok || st.Conn == nil {
		return
	}

	n, err := st.Conn.Write(pkt.Data)
	if err != nil {
		sess.RemoveStream(pkt.StreamID)
		return
	}
	st.BytesOut += int64(n)
}

func (l *Listener) relayFromDest(sess *Session, streamID uint32, conn net.Conn, clientAddr net.Addr) {
	buf := make([]byte, 1344) // max payload per packet
	for {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buf)
		if err != nil {
			l.sendClose(sess, streamID)
			sess.RemoveStream(streamID)
			return
		}

		st, ok := sess.GetStream(streamID)
		if !ok {
			return
		}
		st.BytesIn += int64(n)

		pkt := &pkgudp.Packet{
			ConnID:   sess.Token,
			Type:     pkgudp.MsgStreamData,
			StreamID: streamID,
			Data:     buf[:n],
		}

		encoded, err := pkgudp.EncodePacket(pkt, sess.SessionKey)
		if err != nil {
			return
		}

		// Use latest client address (NAT roaming)
		l.conn.WriteTo(encoded, sess.ClientAddr)
	}
}

func (l *Listener) sendResult(sess *Session, streamID uint32, ok bool, addr net.Addr) {
	result := byte(0x00)
	if ok {
		result = 0x01
	}

	pkt := &pkgudp.Packet{
		ConnID:   sess.Token,
		Type:     pkgudp.MsgStreamData,
		StreamID: streamID,
		Data:     []byte{result},
	}

	encoded, _ := pkgudp.EncodePacket(pkt, sess.SessionKey)
	l.conn.WriteTo(encoded, addr)
}

func (l *Listener) sendClose(sess *Session, streamID uint32) {
	pkt := &pkgudp.Packet{
		ConnID:   sess.Token,
		Type:     pkgudp.MsgStreamClose,
		StreamID: streamID,
	}

	encoded, _ := pkgudp.EncodePacket(pkt, sess.SessionKey)
	if sess.ClientAddr != nil {
		l.conn.WriteTo(encoded, sess.ClientAddr)
	}
}
```

- [ ] **Step 2: Verify server module compiles**

Run: `cd server && go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add server/internal/udp/listener.go
git commit -m "feat(server): add UDP listener with handshake and stream dispatch"
```

---

### Task 8: Client UDP Transport

**Files:**
- Create: `daemon/internal/transport/udp.go`

- [ ] **Step 1: Implement UDP transport with handshake and multiplexing**

```go
// daemon/internal/transport/udp.go
package transport

import (
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	pkgudp "proxyness/pkg/udp"
)

// UDPTransport implements Transport over a single multiplexed UDP channel.
type UDPTransport struct {
	conn       net.Conn // UDP "connected" socket to server
	sessionKey []byte
	connID     uint32
	server     string

	mu       sync.Mutex
	streams  map[uint32]*udpStream
	nextID   uint32
	recvBuf  map[uint32]chan []byte // per-stream receive channels
	closed   chan struct{}
}

func NewUDPTransport() *UDPTransport {
	return &UDPTransport{
		streams: make(map[uint32]*udpStream),
		recvBuf: make(map[uint32]chan []byte),
		closed:  make(chan struct{}),
	}
}

func (t *UDPTransport) Connect(server, key string, machineID [16]byte) error {
	t.server = server

	// Dial UDP (connected mode — sends/receives to/from one addr)
	conn, err := net.DialTimeout("udp", server, 5*time.Second)
	if err != nil {
		return fmt.Errorf("udp dial: %w", err)
	}
	t.conn = conn

	// Generate ephemeral keypair
	clientPriv, clientPub, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		conn.Close()
		return fmt.Errorf("keygen: %w", err)
	}

	// Build handshake request
	req := &pkgudp.HandshakeRequest{
		EphemeralPub: clientPub,
		DeviceKey:    key,
		MachineID:    machineID,
	}
	reqData, err := req.Encode()
	if err != nil {
		conn.Close()
		return fmt.Errorf("encode handshake: %w", err)
	}

	// Encrypt with device key and send
	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode key: %w", err)
	}

	pkt := &pkgudp.Packet{
		ConnID: 0, // handshake
		Type:   pkgudp.MsgHandshake,
		Data:   reqData,
	}
	encoded, err := pkgudp.EncodePacket(pkt, keyBytes)
	if err != nil {
		conn.Close()
		return err
	}

	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(encoded); err != nil {
		conn.Close()
		return fmt.Errorf("send handshake: %w", err)
	}

	// Read response
	buf := make([]byte, 1500)
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		conn.Close()
		return fmt.Errorf("read handshake response: %w", err)
	}

	respPkt, err := pkgudp.DecodePacket(buf[:n], keyBytes)
	if err != nil {
		conn.Close()
		return fmt.Errorf("decode response: %w", err)
	}

	resp, err := pkgudp.DecodeHandshakeResponse(respPkt.Data)
	if err != nil {
		conn.Close()
		return fmt.Errorf("parse response: %w", err)
	}

	// Derive session key
	sessionKey, err := pkgudp.DeriveSessionKey(clientPriv, resp.EphemeralPub)
	if err != nil {
		conn.Close()
		return fmt.Errorf("derive key: %w", err)
	}

	t.sessionKey = sessionKey
	t.connID = resp.SessionToken

	conn.SetReadDeadline(time.Time{})
	conn.SetWriteDeadline(time.Time{})

	// Start receive loop
	go t.recvLoop()

	// Start keepalive
	go t.keepaliveLoop()

	return nil
}

func (t *UDPTransport) recvLoop() {
	buf := make([]byte, 1500)
	for {
		select {
		case <-t.closed:
			return
		default:
		}

		t.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := t.conn.Read(buf)
		if err != nil {
			continue
		}

		pkt, err := pkgudp.DecodePacket(buf[:n], t.sessionKey)
		if err != nil {
			continue
		}

		switch pkt.Type {
		case pkgudp.MsgStreamData:
			t.mu.Lock()
			ch, ok := t.recvBuf[pkt.StreamID]
			t.mu.Unlock()
			if ok {
				select {
				case ch <- pkt.Data:
				default: // buffer full, drop
				}
			}
		case pkgudp.MsgStreamClose:
			t.mu.Lock()
			if ch, ok := t.recvBuf[pkt.StreamID]; ok {
				close(ch)
				delete(t.recvBuf, pkt.StreamID)
			}
			delete(t.streams, pkt.StreamID)
			t.mu.Unlock()
		}
	}
}

func (t *UDPTransport) keepaliveLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-t.closed:
			return
		case <-ticker.C:
			pkt := &pkgudp.Packet{
				ConnID: t.connID,
				Type:   pkgudp.MsgKeepalive,
			}
			encoded, err := pkgudp.EncodePacket(pkt, t.sessionKey)
			if err != nil {
				continue
			}
			t.conn.Write(encoded)
		}
	}
}

func (t *UDPTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	t.mu.Lock()
	t.nextID++
	streamID := t.nextID
	recvCh := make(chan []byte, 256)
	t.recvBuf[streamID] = recvCh
	t.mu.Unlock()

	// Send StreamOpen
	openMsg := &pkgudp.StreamOpenMsg{
		StreamType: streamType,
		Addr:       addr,
		Port:       port,
	}
	pkt := &pkgudp.Packet{
		ConnID:   t.connID,
		Type:     pkgudp.MsgStreamOpen,
		StreamID: streamID,
		Data:     openMsg.Encode(),
	}
	encoded, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		return nil, err
	}
	if _, err := t.conn.Write(encoded); err != nil {
		return nil, err
	}

	// For TCP: wait for connect result
	if streamType == pkgudp.StreamTypeTCP {
		select {
		case data := <-recvCh:
			if len(data) == 0 || data[0] != 0x01 {
				return nil, fmt.Errorf("stream open rejected: %s:%d", addr, port)
			}
		case <-time.After(10 * time.Second):
			return nil, fmt.Errorf("stream open timeout: %s:%d", addr, port)
		}
	}

	s := &udpStream{
		t:        t,
		id:       streamID,
		recvCh:   recvCh,
	}

	t.mu.Lock()
	t.streams[streamID] = s
	t.mu.Unlock()

	return s, nil
}

func (t *UDPTransport) Close() error {
	select {
	case <-t.closed:
		return nil
	default:
		close(t.closed)
	}

	t.mu.Lock()
	for id, ch := range t.recvBuf {
		close(ch)
		delete(t.recvBuf, id)
	}
	t.streams = make(map[uint32]*udpStream)
	t.mu.Unlock()

	return t.conn.Close()
}

func (t *UDPTransport) Mode() string { return ModeUDP }

func (t *UDPTransport) send(streamID uint32, data []byte) error {
	pkt := &pkgudp.Packet{
		ConnID:   t.connID,
		Type:     pkgudp.MsgStreamData,
		StreamID: streamID,
		Data:     data,
	}
	encoded, err := pkgudp.EncodePacket(pkt, t.sessionKey)
	if err != nil {
		return err
	}
	_, err = t.conn.Write(encoded)
	return err
}

func (t *UDPTransport) closeStream(streamID uint32) {
	pkt := &pkgudp.Packet{
		ConnID:   t.connID,
		Type:     pkgudp.MsgStreamClose,
		StreamID: streamID,
	}
	encoded, _ := pkgudp.EncodePacket(pkt, t.sessionKey)
	t.conn.Write(encoded)

	t.mu.Lock()
	if ch, ok := t.recvBuf[streamID]; ok {
		close(ch)
		delete(t.recvBuf, streamID)
	}
	delete(t.streams, streamID)
	t.mu.Unlock()
}

// udpStream implements Stream for UDP transport.
type udpStream struct {
	t      *UDPTransport
	id     uint32
	recvCh chan []byte
	// partial holds leftover bytes from last Read
	partial []byte
}

func (s *udpStream) Read(p []byte) (int, error) {
	// Drain partial first
	if len(s.partial) > 0 {
		n := copy(p, s.partial)
		s.partial = s.partial[n:]
		return n, nil
	}

	data, ok := <-s.recvCh
	if !ok {
		return 0, fmt.Errorf("stream closed")
	}

	n := copy(p, data)
	if n < len(data) {
		s.partial = data[n:]
	}
	return n, nil
}

func (s *udpStream) Write(p []byte) (int, error) {
	// Chunk into 1344-byte segments
	sent := 0
	for sent < len(p) {
		end := sent + 1344
		if end > len(p) {
			end = len(p)
		}
		if err := s.t.send(s.id, p[sent:end]); err != nil {
			return sent, err
		}
		sent = end
	}
	return sent, nil
}

func (s *udpStream) Close() error {
	s.t.closeStream(s.id)
	return nil
}

func (s *udpStream) ID() uint32 { return s.id }
```

- [ ] **Step 2: Verify daemon module compiles**

Run: `cd daemon && go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add daemon/internal/transport/udp.go
git commit -m "feat(transport): add UDP transport with handshake and multiplexing"
```

---

### Task 9: Auto Transport with Fallback

**Files:**
- Create: `daemon/internal/transport/auto.go`

- [ ] **Step 1: Implement Auto transport**

```go
// daemon/internal/transport/auto.go
package transport

import (
	"fmt"
	"log"
	"time"
)

const udpTimeout = 3 * time.Second

// AutoTransport tries UDP first, falls back to TLS.
type AutoTransport struct {
	active Transport
}

func NewAutoTransport() *AutoTransport {
	return &AutoTransport{}
}

func (a *AutoTransport) Connect(server, key string, machineID [16]byte) error {
	// Try UDP first
	udp := NewUDPTransport()
	done := make(chan error, 1)
	go func() {
		done <- udp.Connect(server, key, machineID)
	}()

	select {
	case err := <-done:
		if err == nil {
			a.active = udp
			log.Printf("[transport] connected via UDP")
			return nil
		}
		log.Printf("[transport] UDP failed: %v, falling back to TLS", err)
	case <-time.After(udpTimeout):
		udp.Close()
		log.Printf("[transport] UDP timeout, falling back to TLS")
	}

	// Fallback to TLS
	tls := NewTLSTransport()
	if err := tls.Connect(server, key, machineID); err != nil {
		return fmt.Errorf("both transports failed: %w", err)
	}
	a.active = tls
	log.Printf("[transport] connected via TLS")
	return nil
}

func (a *AutoTransport) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	if a.active == nil {
		return nil, fmt.Errorf("not connected")
	}
	return a.active.OpenStream(streamType, addr, port)
}

func (a *AutoTransport) Close() error {
	if a.active == nil {
		return nil
	}
	return a.active.Close()
}

func (a *AutoTransport) Mode() string {
	if a.active == nil {
		return ModeAuto
	}
	return a.active.Mode()
}
```

- [ ] **Step 2: Verify daemon module compiles**

Run: `cd daemon && go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add daemon/internal/transport/auto.go
git commit -m "feat(transport): add Auto transport with UDP-first, TLS fallback"
```

---

### Task 10: Daemon Integration — Refactor Tunnel and TUN Engine

**Files:**
- Modify: `daemon/internal/tunnel/tunnel.go` — use Transport interface for SOCKS5 proxying
- Modify: `daemon/internal/tun/engine.go` — use Transport interface for TUN proxying
- Modify: `daemon/internal/api/api.go` — add transport mode endpoints, pass transport to tunnel/engine

This is the largest task. The key change: instead of each proxied connection doing its own TLS dial + auth + machine ID, it calls `transport.OpenStream()` which returns a `Stream` (either a TLS connection or a multiplexed UDP stream).

- [ ] **Step 1: Add Transport field to Tunnel struct**

In `daemon/internal/tunnel/tunnel.go`, add a `transport` field to `Tunnel` and modify `handleSOCKS()` to use `transport.OpenStream()` instead of manual TLS dial + auth + machine ID + message type + connect.

The existing `handleSOCKS()` (lines 229-306) does:
1. TLS dial → auth → machine ID → MsgTypeTCP → WriteConnect → CountingRelay

Replace with:
1. `transport.OpenStream(StreamTypeTCP, addr, port)` → relay between SOCKS5 client and stream

- [ ] **Step 2: Add Transport field to Engine struct**

In `daemon/internal/tun/engine.go`, modify `proxyTCP()` (line 508) and `proxyUDP()` (line 642) to use `transport.OpenStream()` instead of manual TLS dial + auth + machine ID.

`proxyTCP()` currently:
1. TLS dial → auth → machine ID → MsgTypeTCP → WriteConnect → idleRelay

Replace with:
1. `transport.OpenStream(StreamTypeTCP, addr, port)` → idleRelay

`proxyUDP()` currently:
1. TLS dial → auth → machine ID → MsgTypeUDP → WriteUDPFrame loop

Replace with:
1. `transport.OpenStream(StreamTypeUDP, addr, port)` → Write/Read loop

- [ ] **Step 3: Add transport mode to daemon API**

In `daemon/internal/api/api.go`, add:
- `POST /transport` handler — sets transport mode (auto/udp/tls), recreates transport
- `GET /transport` handler — returns `{"mode": "auto", "active": "udp"}`
- Store transport mode in Server struct
- Pass transport to Tunnel and Engine on connect

- [ ] **Step 4: Verify daemon compiles and existing tests pass**

Run: `cd daemon && go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add daemon/
git commit -m "feat(daemon): integrate Transport interface into tunnel and TUN engine"
```

---

### Task 11: Server Integration — Add UDP Listener to Mux

**Files:**
- Modify: `server/internal/mux/mux.go` — start UDP listener alongside TCP
- Modify: `server/cmd/main.go` (or wherever the server starts) — pass UDP listener to mux

- [ ] **Step 1: Add UDP listener to server startup**

In the server's main startup (where `PreTLSMux` is created), add:

```go
// Start UDP listener on same port
udpConn, err := net.ListenPacket("udp", ":443")
if err != nil {
    log.Fatal(err)
}
udpListener := udp.NewListener(udpConn, database, tracker)
go udpListener.Serve()
```

- [ ] **Step 2: Verify server compiles**

Run: `cd server && go build ./...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add server/
git commit -m "feat(server): add UDP listener on port 443 alongside TCP"
```

---

### Task 12: Integration Tests

**Files:**
- Create: `test/udp_test.go`

- [ ] **Step 1: Write end-to-end test**

```go
// test/udp_test.go
package test

import (
	"net"
	"testing"
	"time"

	pkgudp "proxyness/pkg/udp"
)

func TestUDPHandshakeE2E(t *testing.T) {
	// This test requires a running server.
	// Skip in CI, run manually with: go test -run TestUDPHandshake -tags e2e

	if testing.Short() {
		t.Skip("skipping e2e test")
	}

	// Generate ephemeral key
	clientPriv, clientPub, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	// Build handshake request
	req := &pkgudp.HandshakeRequest{
		EphemeralPub: clientPub,
		DeviceKey:    "test-key", // must match a device in DB
		MachineID:    [16]byte{1, 2, 3},
	}
	reqData, _ := req.Encode()

	// Encode as QUIC-disguised packet
	pkt := &pkgudp.Packet{
		ConnID: 0,
		Type:   pkgudp.MsgHandshake,
		Data:   reqData,
	}
	// ... encrypt with device key and send to server:443/udp

	_ = clientPriv // used for DeriveSessionKey after response
	t.Log("handshake test placeholder — requires running server")
}
```

- [ ] **Step 2: Run unit tests across all modules**

Run: `make test`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add test/
git commit -m "test: add UDP transport integration test skeleton"
```

---

### Task 13: Electron Client — Transport Selector UI

**Files:**
- Modify: `client/src/main/preload.ts` — add transport IPC
- Modify: `client/src/main/index.ts` — add transport IPC handlers
- Modify: `client/src/renderer/App.tsx` — add transport indicator + settings

- [ ] **Step 1: Add IPC for transport mode**

In `client/src/main/preload.ts`, add to context bridge:
```typescript
transport: {
  getMode: () => ipcRenderer.invoke('transport-get'),
  setMode: (mode: string) => ipcRenderer.invoke('transport-set', mode),
}
```

In `client/src/main/index.ts`, add IPC handlers:
```typescript
ipcMain.handle('transport-get', async () => {
  try {
    const res = await fetch('http://127.0.0.1:9090/transport');
    return await res.json();
  } catch {
    return { mode: 'auto', active: 'tls' };
  }
});

ipcMain.handle('transport-set', async (_e, mode: string) => {
  try {
    await fetch('http://127.0.0.1:9090/transport', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode }),
    });
    return { ok: true };
  } catch {
    return { ok: false };
  }
});
```

- [ ] **Step 2: Add transport indicator to App.tsx**

Add a small badge next to the connection status showing "UDP" or "TLS". Add a transport mode selector (Auto/UDP/TLS) in settings or as a dropdown.

- [ ] **Step 3: Verify client builds**

Run: `cd client && npm run build`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add client/
git commit -m "feat(client): add transport mode selector and active transport indicator"
```

---

### Execution Order

Tasks 1-4 are independent (protocol layer) — can be parallelized.
Task 5 depends on pkg/proto types being defined (Tasks 1-4).
Tasks 6-7 (server) and Task 8-9 (client) can be parallelized after Task 5.
Task 10 (daemon integration) depends on Tasks 5, 8, 9.
Task 11 (server integration) depends on Tasks 6, 7.
Task 12 (tests) depends on Tasks 10, 11.
Task 13 (UI) depends on Task 10.

```
[1: Crypto] ──┐
[2: Packet] ──┤
[3: Handshake]┼──► [5: Interface] ──► [8: Client UDP] ──► [9: Auto] ──► [10: Daemon Integration] ──► [12: Tests]
[4: Stream] ──┘                  ──► [6: Session Mgr] ──► [7: Listener] ──► [11: Server Integration] ──┘
                                                                                                    ──► [13: UI]
```
