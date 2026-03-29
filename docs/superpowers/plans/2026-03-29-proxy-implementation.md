# SmurovProxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a TLS-based proxy for bypassing DPI with Go server/daemon and Electron GUI client.

**Architecture:** Go workspace with shared `pkg/` (auth, proto), separate `server/` and `daemon/` modules, Electron+React+Vite client. Daemon exposes SOCKS5 on localhost and HTTP API for the GUI. Server listens on 443 via TLS, indistinguishable from HTTPS.

**Tech Stack:** Go 1.22+, Electron, React, TypeScript, Vite, electron-builder

---

**Deviation from spec:** Auth and protocol packages are shared via Go workspace (`pkg/`) instead of duplicated in `server/internal/` and `daemon/internal/`. This avoids code duplication while keeping modules separate.

**Project structure:**
```
proxy/
├── go.work
├── pkg/                          # Shared Go packages
│   ├── go.mod
│   ├── auth/
│   │   ├── auth.go
│   │   └── auth_test.go
│   └── proto/
│       ├── proto.go
│       └── proto_test.go
├── server/
│   ├── go.mod
│   └── cmd/
│       └── main.go
├── daemon/
│   ├── go.mod
│   ├── cmd/
│   │   └── main.go
│   └── internal/
│       ├── socks5/
│       │   ├── socks5.go
│       │   └── socks5_test.go
│       ├── tunnel/
│       │   ├── tunnel.go
│       │   └── tunnel_test.go
│       └── api/
│           ├── api.go
│           └── api_test.go
├── client/
│   ├── package.json
│   ├── tsconfig.json
│   ├── electron-builder.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main/
│       │   ├── index.ts
│       │   └── daemon.ts
│       └── renderer/
│           ├── main.tsx
│           ├── App.tsx
│           ├── components/
│           │   ├── ConnectionButton.tsx
│           │   ├── StatusBar.tsx
│           │   └── Settings.tsx
│           └── hooks/
│               └── useDaemon.ts
└── Makefile
```

---

## Phase 1: Go Core

### Task 1: Go Workspace + Auth Package

**Files:**
- Create: `go.work`
- Create: `pkg/go.mod`
- Create: `pkg/auth/auth_test.go`
- Create: `pkg/auth/auth.go`

- [ ] **Step 1: Initialize Go workspace and module**

```bash
cd /Users/ilyasmurov/projects/smurov/proxy
mkdir -p pkg/auth
cd pkg && go mod init smurov-proxy/pkg
cd .. && go work init ./pkg
```

- [ ] **Step 2: Write auth tests**

Create `pkg/auth/auth_test.go`:

```go
package auth

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"
)

func TestGenerateKey(t *testing.T) {
	key := GenerateKey()
	if len(key) != 64 {
		t.Fatalf("expected 64 hex chars, got %d", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("key is not valid hex: %v", err)
	}

	key2 := GenerateKey()
	if key == key2 {
		t.Fatal("two generated keys should not be equal")
	}
}

func TestCreateAuthMessage(t *testing.T) {
	key := GenerateKey()
	msg, err := CreateAuthMessage(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg) != AuthMsgLen {
		t.Fatalf("expected %d bytes, got %d", AuthMsgLen, len(msg))
	}
	if msg[0] != Version {
		t.Fatalf("expected version %d, got %d", Version, msg[0])
	}
}

func TestValidateAuthMessage_Valid(t *testing.T) {
	key := GenerateKey()
	msg, err := CreateAuthMessage(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAuthMessage(key, msg); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateAuthMessage_WrongKey(t *testing.T) {
	key1 := GenerateKey()
	key2 := GenerateKey()
	msg, _ := CreateAuthMessage(key1)
	if err := ValidateAuthMessage(key2, msg); err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestValidateAuthMessage_ExpiredTimestamp(t *testing.T) {
	key := GenerateKey()
	msg, _ := CreateAuthMessage(key)
	// Set timestamp to 60 seconds ago
	ts := uint64(time.Now().Unix()) - 60
	binary.BigEndian.PutUint64(msg[1:9], ts)
	if err := ValidateAuthMessage(key, msg); err == nil {
		t.Fatal("expected error for expired timestamp")
	}
}

func TestValidateAuthMessage_BadLength(t *testing.T) {
	key := GenerateKey()
	if err := ValidateAuthMessage(key, []byte{0x01}); err == nil {
		t.Fatal("expected error for short message")
	}
}

func TestValidateAuthMessage_BadVersion(t *testing.T) {
	key := GenerateKey()
	msg, _ := CreateAuthMessage(key)
	msg[0] = 0xFF
	if err := ValidateAuthMessage(key, msg); err == nil {
		t.Fatal("expected error for wrong version")
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
cd pkg && go test ./auth/
```

Expected: compilation error — `auth.go` does not exist yet.

- [ ] **Step 4: Implement auth package**

Create `pkg/auth/auth.go`:

```go
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	Version      = 0x01
	AuthMsgLen   = 41 // 1 version + 8 timestamp + 32 HMAC
	MaxClockSkew = 30 // seconds
)

// GenerateKey returns a random 256-bit key as a hex string.
func GenerateKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return hex.EncodeToString(key)
}

// CreateAuthMessage builds a 41-byte auth message: version + timestamp + HMAC.
func CreateAuthMessage(key string) ([]byte, error) {
	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}

	msg := make([]byte, AuthMsgLen)
	msg[0] = Version
	binary.BigEndian.PutUint64(msg[1:9], uint64(time.Now().Unix()))

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(msg[1:9])
	copy(msg[9:], mac.Sum(nil))

	return msg, nil
}

// ValidateAuthMessage checks version, timestamp freshness, and HMAC.
func ValidateAuthMessage(key string, msg []byte) error {
	if len(msg) != AuthMsgLen {
		return fmt.Errorf("invalid message length: %d", len(msg))
	}
	if msg[0] != Version {
		return fmt.Errorf("unsupported version: %d", msg[0])
	}

	keyBytes, err := hex.DecodeString(key)
	if err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}

	ts := binary.BigEndian.Uint64(msg[1:9])
	now := uint64(time.Now().Unix())
	diff := int64(now) - int64(ts)
	if diff < 0 {
		diff = -diff
	}
	if diff > MaxClockSkew {
		return fmt.Errorf("timestamp expired: %d seconds skew", diff)
	}

	mac := hmac.New(sha256.New, keyBytes)
	mac.Write(msg[1:9])
	expected := mac.Sum(nil)

	if !hmac.Equal(msg[9:], expected) {
		return fmt.Errorf("HMAC mismatch")
	}

	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd pkg && go test ./auth/ -v
```

Expected: all 6 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add go.work pkg/
git commit -m "feat: add Go workspace and auth package with HMAC-SHA256"
```

---

### Task 2: Protocol Package

**Files:**
- Create: `pkg/proto/proto_test.go`
- Create: `pkg/proto/proto.go`

- [ ] **Step 1: Write proto tests**

Create `pkg/proto/proto_test.go`:

```go
package proto

import (
	"net"
	"testing"

	"smurov-proxy/pkg/auth"
)

func TestWriteReadAuth_Valid(t *testing.T) {
	key := auth.GenerateKey()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		if err := WriteAuth(c1, key); err != nil {
			t.Errorf("WriteAuth: %v", err)
		}
	}()

	if err := ReadAuth(c2, key); err != nil {
		t.Fatalf("ReadAuth: %v", err)
	}
}

func TestWriteReadAuth_WrongKey(t *testing.T) {
	key1 := auth.GenerateKey()
	key2 := auth.GenerateKey()
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteAuth(c1, key1)
	}()

	if err := ReadAuth(c2, key2); err == nil {
		t.Fatal("expected error for wrong key")
	}
}

func TestWriteReadResult(t *testing.T) {
	for _, ok := range []bool{true, false} {
		c1, c2 := net.Pipe()

		go func() {
			WriteResult(c1, ok)
			c1.Close()
		}()

		got, err := ReadResult(c2)
		c2.Close()
		if err != nil {
			t.Fatalf("ReadResult: %v", err)
		}
		if got != ok {
			t.Fatalf("expected %v, got %v", ok, got)
		}
	}
}

func TestWriteReadConnect_IPv4(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "93.184.216.34", 443)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "93.184.216.34" || port != 443 {
		t.Fatalf("got %s:%d, want 93.184.216.34:443", addr, port)
	}
}

func TestWriteReadConnect_Domain(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "example.com", 80)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "example.com" || port != 80 {
		t.Fatalf("got %s:%d, want example.com:80", addr, port)
	}
}

func TestWriteReadConnect_IPv6(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		WriteConnect(c1, "2001:db8::1", 8080)
	}()

	addr, port, err := ReadConnect(c2)
	if err != nil {
		t.Fatalf("ReadConnect: %v", err)
	}
	if addr != "2001:db8::1" || port != 8080 {
		t.Fatalf("got %s:%d, want 2001:db8::1:8080", addr, port)
	}
}

func TestRelay(t *testing.T) {
	c1, c2 := net.Pipe()
	c3, c4 := net.Pipe()

	go Relay(c2, c3)

	// Write from c1, read from c4
	go func() {
		c1.Write([]byte("hello"))
		c1.Close()
	}()

	buf := make([]byte, 5)
	n, _ := c4.Read(buf)
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q, want %q", string(buf[:n]), "hello")
	}
	c2.Close()
	c3.Close()
	c4.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd pkg && go test ./proto/
```

Expected: compilation error — `proto.go` does not exist yet.

- [ ] **Step 3: Implement proto package**

Create `pkg/proto/proto.go`:

```go
package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"smurov-proxy/pkg/auth"
)

const (
	AddrTypeIPv4   = 0x01
	AddrTypeDomain = 0x03
	AddrTypeIPv6   = 0x04

	ResultOK   = 0x01
	ResultFail = 0x00
)

// WriteAuth sends a 41-byte auth message over the connection.
func WriteAuth(conn net.Conn, key string) error {
	msg, err := auth.CreateAuthMessage(key)
	if err != nil {
		return err
	}
	_, err = conn.Write(msg)
	return err
}

// ReadAuth reads and validates a 41-byte auth message.
func ReadAuth(conn net.Conn, key string) error {
	msg := make([]byte, auth.AuthMsgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return err
	}
	return auth.ValidateAuthMessage(key, msg)
}

// WriteResult sends a 1-byte result (0x01=OK, 0x00=fail).
func WriteResult(conn net.Conn, ok bool) error {
	b := byte(ResultFail)
	if ok {
		b = ResultOK
	}
	_, err := conn.Write([]byte{b})
	return err
}

// ReadResult reads a 1-byte result.
func ReadResult(conn net.Conn) (bool, error) {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false, err
	}
	return buf[0] == ResultOK, nil
}

// WriteConnect sends address type + address + port.
func WriteConnect(conn net.Conn, addr string, port uint16) error {
	ip := net.ParseIP(addr)

	var buf []byte
	if ip4 := ip.To4(); ip4 != nil {
		buf = make([]byte, 1+4+2)
		buf[0] = AddrTypeIPv4
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
	} else if ip16 := ip.To16(); ip16 != nil {
		buf = make([]byte, 1+16+2)
		buf[0] = AddrTypeIPv6
		copy(buf[1:17], ip16)
		binary.BigEndian.PutUint16(buf[17:], port)
	} else {
		// Domain name
		buf = make([]byte, 1+1+len(addr)+2)
		buf[0] = AddrTypeDomain
		buf[1] = byte(len(addr))
		copy(buf[2:2+len(addr)], addr)
		binary.BigEndian.PutUint16(buf[2+len(addr):], port)
	}

	_, err := conn.Write(buf)
	return err
}

// ReadConnect reads address type + address + port.
func ReadConnect(conn net.Conn) (addr string, port uint16, err error) {
	typeBuf := make([]byte, 1)
	if _, err = io.ReadFull(conn, typeBuf); err != nil {
		return
	}

	switch typeBuf[0] {
	case AddrTypeIPv4:
		buf := make([]byte, 4)
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = net.IP(buf).String()
	case AddrTypeDomain:
		lenBuf := make([]byte, 1)
		if _, err = io.ReadFull(conn, lenBuf); err != nil {
			return
		}
		buf := make([]byte, lenBuf[0])
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = string(buf)
	case AddrTypeIPv6:
		buf := make([]byte, 16)
		if _, err = io.ReadFull(conn, buf); err != nil {
			return
		}
		addr = net.IP(buf).String()
	default:
		err = fmt.Errorf("unsupported address type: 0x%02x", typeBuf[0])
		return
	}

	portBuf := make([]byte, 2)
	if _, err = io.ReadFull(conn, portBuf); err != nil {
		return
	}
	port = binary.BigEndian.Uint16(portBuf)
	return
}

// Relay copies data bidirectionally between two connections.
// Returns when either direction hits an error or EOF.
func Relay(c1, c2 net.Conn) error {
	errc := make(chan error, 2)
	cp := func(dst, src net.Conn) {
		_, err := io.Copy(dst, src)
		errc <- err
	}
	go cp(c1, c2)
	go cp(c2, c1)
	return <-errc
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd pkg && go test ./proto/ -v
```

Expected: all 7 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/proto/
git commit -m "feat: add custom protocol package (auth, connect, relay)"
```

---

### Task 3: Server

**Files:**
- Create: `server/go.mod`
- Create: `server/cmd/main.go`
- Update: `go.work`

- [ ] **Step 1: Initialize server module**

```bash
mkdir -p server/cmd
cd server && go mod init smurov-proxy/server
```

- [ ] **Step 2: Add server to workspace and add dependency**

Update `go.work` to include server:

```bash
cd /Users/ilyasmurov/projects/smurov/proxy
go work use ./server
```

Add dependency in `server/go.mod`:

```bash
cd server && go mod edit -require smurov-proxy/pkg@v0.0.0
cd .. && go work sync
```

- [ ] **Step 3: Implement server**

Create `server/cmd/main.go`:

```go
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"time"

	"smurov-proxy/pkg/proto"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	key := flag.String("key", "", "shared secret key (hex)")
	certFile := flag.String("cert", "cert.pem", "TLS certificate file")
	keyFile := flag.String("keyfile", "key.pem", "TLS private key file")
	flag.Parse()

	if *key == "" {
		log.Fatal("-key is required")
	}

	if err := ensureCert(*certFile, *keyFile); err != nil {
		log.Fatalf("cert: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", *addr, tlsCfg)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("server listening on %s", *addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, *key)
	}
}

func handleConn(conn net.Conn, key string) {
	defer conn.Close()

	// Phase 1: Auth
	if err := proto.ReadAuth(conn, key); err != nil {
		proto.WriteResult(conn, false)
		log.Printf("auth failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	proto.WriteResult(conn, true)

	// Phase 2: Connect
	addr, port, err := proto.ReadConnect(conn)
	if err != nil {
		log.Printf("connect read: %v", err)
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", addr, port), 10*time.Second)
	if err != nil {
		proto.WriteResult(conn, false)
		log.Printf("dial %s:%d: %v", addr, port, err)
		return
	}
	defer target.Close()
	proto.WriteResult(conn, true)

	// Phase 3: Relay
	proto.Relay(conn, target)
}

func ensureCert(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); err == nil {
		return nil
	}

	log.Println("generating self-signed TLS certificate...")

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"SmurovProxy"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()

	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return err
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes})
	keyOut.Close()

	log.Printf("wrote %s and %s", certFile, keyFile)
	return nil
}
```

- [ ] **Step 4: Verify it compiles**

```bash
cd server && go build ./cmd/
```

Expected: builds without errors.

- [ ] **Step 5: Commit**

```bash
git add go.work server/
git commit -m "feat: add proxy server with TLS and self-signed cert generation"
```

---

### Task 4: Daemon — SOCKS5 Package

**Files:**
- Create: `daemon/go.mod`
- Create: `daemon/internal/socks5/socks5_test.go`
- Create: `daemon/internal/socks5/socks5.go`
- Update: `go.work`

- [ ] **Step 1: Initialize daemon module**

```bash
mkdir -p daemon/cmd daemon/internal/socks5 daemon/internal/tunnel daemon/internal/api
cd daemon && go mod init smurov-proxy/daemon
cd /Users/ilyasmurov/projects/smurov/proxy && go work use ./daemon
cd daemon && go mod edit -require smurov-proxy/pkg@v0.0.0
cd .. && go work sync
```

- [ ] **Step 2: Write SOCKS5 tests**

Create `daemon/internal/socks5/socks5_test.go`:

```go
package socks5

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestHandshake_IPv4(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		// Version + 1 method (no auth)
		c1.Write([]byte{0x05, 0x01, 0x00})
		// Read method selection reply
		reply := make([]byte, 2)
		io.ReadFull(c1, reply)
		// CONNECT to 93.184.216.34:443
		req := []byte{0x05, 0x01, 0x00, 0x01, 93, 184, 216, 34, 0x01, 0xBB}
		c1.Write(req)
	}()

	cr, err := Handshake(c2)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if cr.Addr != "93.184.216.34" {
		t.Fatalf("addr: got %s, want 93.184.216.34", cr.Addr)
	}
	if cr.Port != 443 {
		t.Fatalf("port: got %d, want 443", cr.Port)
	}
}

func TestHandshake_Domain(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x05, 0x01, 0x00})
		reply := make([]byte, 2)
		io.ReadFull(c1, reply)
		// CONNECT to example.com:80
		domain := "example.com"
		req := make([]byte, 4+1+len(domain)+2)
		req[0] = 0x05
		req[1] = 0x01
		req[2] = 0x00
		req[3] = 0x03
		req[4] = byte(len(domain))
		copy(req[5:], domain)
		binary.BigEndian.PutUint16(req[5+len(domain):], 80)
		c1.Write(req)
	}()

	cr, err := Handshake(c2)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if cr.Addr != "example.com" {
		t.Fatalf("addr: got %s, want example.com", cr.Addr)
	}
	if cr.Port != 80 {
		t.Fatalf("port: got %d, want 80", cr.Port)
	}
}

func TestHandshake_BadVersion(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		c1.Write([]byte{0x04, 0x01, 0x00}) // SOCKS4
	}()

	if _, err := Handshake(c2); err == nil {
		t.Fatal("expected error for SOCKS4")
	}
}

func TestSendSuccess(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		SendSuccess(c1)
		c1.Close()
	}()

	reply := make([]byte, 10)
	io.ReadFull(c2, reply)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("expected success reply, got %v", reply)
	}
}

func TestSendFailure(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	go func() {
		SendFailure(c1)
		c1.Close()
	}()

	reply := make([]byte, 10)
	io.ReadFull(c2, reply)
	if reply[0] != 0x05 || reply[1] != 0x01 {
		t.Fatalf("expected failure reply, got %v", reply)
	}
}
```

- [ ] **Step 3: Run tests to verify they fail**

```bash
cd daemon && go test ./internal/socks5/
```

Expected: compilation error.

- [ ] **Step 4: Implement SOCKS5 package**

Create `daemon/internal/socks5/socks5.go`:

```go
package socks5

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

const (
	version    = 0x05
	cmdConnect = 0x01
	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04
	noAuth     = 0x00
)

// ConnectRequest holds the parsed SOCKS5 CONNECT destination.
type ConnectRequest struct {
	Addr string
	Port uint16
}

// Handshake performs the SOCKS5 handshake and returns the CONNECT request.
func Handshake(conn net.Conn) (*ConnectRequest, error) {
	// Read version + nmethods
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	if buf[0] != version {
		return nil, fmt.Errorf("unsupported SOCKS version: %d", buf[0])
	}

	// Read methods (we ignore them — always pick no-auth)
	methods := make([]byte, buf[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return nil, err
	}

	// Reply: no auth
	if _, err := conn.Write([]byte{version, noAuth}); err != nil {
		return nil, err
	}

	// Read connect request header: VER CMD RSV ATYP
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[0] != version {
		return nil, fmt.Errorf("bad version in request: %d", header[0])
	}
	if header[1] != cmdConnect {
		return nil, fmt.Errorf("unsupported command: %d", header[1])
	}

	// Read address
	var addr string
	switch header[3] {
	case atypIPv4:
		ipBuf := make([]byte, 4)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return nil, err
		}
		addr = net.IP(ipBuf).String()
	case atypDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return nil, err
		}
		domBuf := make([]byte, lenBuf[0])
		if _, err := io.ReadFull(conn, domBuf); err != nil {
			return nil, err
		}
		addr = string(domBuf)
	case atypIPv6:
		ipBuf := make([]byte, 16)
		if _, err := io.ReadFull(conn, ipBuf); err != nil {
			return nil, err
		}
		addr = net.IP(ipBuf).String()
	default:
		return nil, fmt.Errorf("unsupported address type: 0x%02x", header[3])
	}

	// Read port
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return nil, err
	}

	return &ConnectRequest{
		Addr: addr,
		Port: binary.BigEndian.Uint16(portBuf),
	}, nil
}

// SendSuccess sends a SOCKS5 success reply.
func SendSuccess(conn net.Conn) error {
	reply := []byte{version, 0x00, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}

// SendFailure sends a SOCKS5 failure reply.
func SendFailure(conn net.Conn) error {
	reply := []byte{version, 0x01, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	_, err := conn.Write(reply)
	return err
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
cd daemon && go test ./internal/socks5/ -v
```

Expected: all 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add go.work daemon/
git commit -m "feat: add daemon module with SOCKS5 handshake"
```

---

### Task 5: Daemon — Tunnel Package

**Files:**
- Create: `daemon/internal/tunnel/tunnel_test.go`
- Create: `daemon/internal/tunnel/tunnel.go`

- [ ] **Step 1: Write tunnel tests**

Create `daemon/internal/tunnel/tunnel_test.go`:

```go
package tunnel

import (
	"testing"
)

func TestNew(t *testing.T) {
	tun := New()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
	if tun.Uptime() != 0 {
		t.Fatalf("expected 0 uptime, got %d", tun.Uptime())
	}
}

func TestStartStop(t *testing.T) {
	tun := New()

	// Start on a random port (port 0 = OS picks one)
	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tun.GetStatus() != Connected {
		t.Fatalf("expected connected, got %s", tun.GetStatus())
	}

	tun.Stop()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
}

func TestDoubleStart(t *testing.T) {
	tun := New()

	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	err = tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestUptime(t *testing.T) {
	tun := New()
	err := tun.Start("127.0.0.1:0", "127.0.0.1:9999", "deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	if tun.Uptime() < 0 {
		t.Fatal("uptime should be >= 0")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd daemon && go test ./internal/tunnel/
```

Expected: compilation error.

- [ ] **Step 3: Implement tunnel package**

Create `daemon/internal/tunnel/tunnel.go`:

```go
package tunnel

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"smurov-proxy/daemon/internal/socks5"
	"smurov-proxy/pkg/proto"
)

type Status string

const (
	Disconnected Status = "disconnected"
	Connected    Status = "connected"
)

type Tunnel struct {
	mu         sync.Mutex
	status     Status
	serverAddr string
	key        string
	listener   net.Listener
	startTime  time.Time
}

func New() *Tunnel {
	return &Tunnel{status: Disconnected}
}

func (t *Tunnel) Start(listenAddr, serverAddr, key string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.status != Disconnected {
		return fmt.Errorf("tunnel already %s", t.status)
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	t.listener = ln
	t.serverAddr = serverAddr
	t.key = key
	t.status = Connected
	t.startTime = time.Now()

	go t.acceptLoop()
	return nil
}

func (t *Tunnel) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.listener != nil {
		t.listener.Close()
		t.listener = nil
	}
	t.status = Disconnected
}

func (t *Tunnel) GetStatus() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Tunnel) Uptime() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status != Connected {
		return 0
	}
	return int64(time.Since(t.startTime).Seconds())
}

func (t *Tunnel) acceptLoop() {
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.handleSOCKS(conn)
	}
}

func (t *Tunnel) handleSOCKS(conn net.Conn) {
	defer conn.Close()

	req, err := socks5.Handshake(conn)
	if err != nil {
		log.Printf("socks5 handshake: %v", err)
		return
	}

	// Connect to proxy server via TLS
	tlsConn, err := tls.Dial("tcp", t.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		socks5.SendFailure(conn)
		log.Printf("tls dial %s: %v", t.serverAddr, err)
		return
	}
	defer tlsConn.Close()

	// Auth
	if err := proto.WriteAuth(tlsConn, t.key); err != nil {
		socks5.SendFailure(conn)
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		return
	}

	// Connect
	if err := proto.WriteConnect(tlsConn, req.Addr, req.Port); err != nil {
		socks5.SendFailure(conn)
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		socks5.SendFailure(conn)
		return
	}

	socks5.SendSuccess(conn)
	proto.Relay(conn, tlsConn)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd daemon && go test ./internal/tunnel/ -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tunnel/
git commit -m "feat: add tunnel package with SOCKS5→TLS proxy chain"
```

---

### Task 6: Daemon — HTTP API

**Files:**
- Create: `daemon/internal/api/api_test.go`
- Create: `daemon/internal/api/api.go`

- [ ] **Step 1: Write API tests**

Create `daemon/internal/api/api_test.go`:

```go
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smurov-proxy/daemon/internal/tunnel"
)

func TestHealthEndpoint(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStatusEndpoint_Disconnected(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("GET", "/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "disconnected" {
		t.Fatalf("expected disconnected, got %s", resp.Status)
	}
}

func TestConnectEndpoint_BadJSON(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("POST", "/connect", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDisconnectEndpoint(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:1080")

	req := httptest.NewRequest("POST", "/disconnect", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestConnectDisconnectFlow(t *testing.T) {
	tun := tunnel.New()
	srv := New(tun, "127.0.0.1:0") // port 0 to avoid conflicts

	// Connect
	body := `{"server":"127.0.0.1:9999","key":"aabbccdd"}`
	req := httptest.NewRequest("POST", "/connect", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("connect: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	// Status should be connected
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp StatusResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "connected" {
		t.Fatalf("expected connected, got %s", resp.Status)
	}

	// Disconnect
	req = httptest.NewRequest("POST", "/disconnect", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("disconnect: expected 200, got %d", w.Code)
	}

	// Status should be disconnected
	req = httptest.NewRequest("GET", "/status", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "disconnected" {
		t.Fatalf("expected disconnected, got %s", resp.Status)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd daemon && go test ./internal/api/
```

Expected: compilation error.

- [ ] **Step 3: Implement API package**

Create `daemon/internal/api/api.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"smurov-proxy/daemon/internal/tunnel"
)

type Server struct {
	tunnel     *tunnel.Tunnel
	listenAddr string
}

type ConnectRequest struct {
	ServerAddr string `json:"server"`
	Key        string `json:"key"`
}

type StatusResponse struct {
	Status string `json:"status"`
	Uptime int64  `json:"uptime"`
}

func New(t *tunnel.Tunnel, listenAddr string) *Server {
	return &Server{tunnel: t, listenAddr: listenAddr}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	return mux
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req ConnectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := s.tunnel.Start(s.listenAddr, req.ServerAddr, req.Key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.tunnel.Stop()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatusResponse{
		Status: string(s.tunnel.GetStatus()),
		Uptime: s.tunnel.Uptime(),
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd daemon && go test ./internal/api/ -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/api/
git commit -m "feat: add daemon HTTP API (connect, disconnect, status, health)"
```

---

### Task 7: Daemon — Main Entry Point

**Files:**
- Create: `daemon/cmd/main.go`

- [ ] **Step 1: Implement daemon main**

Create `daemon/cmd/main.go`:

```go
package main

import (
	"flag"
	"log"
	"net/http"

	"smurov-proxy/daemon/internal/api"
	"smurov-proxy/daemon/internal/tunnel"
)

func main() {
	serverAddr := flag.String("server", "", "proxy server address (host:port)")
	key := flag.String("key", "", "shared secret key (hex)")
	listenAddr := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	apiAddr := flag.String("api", "127.0.0.1:9090", "HTTP API listen address")
	flag.Parse()

	tun := tunnel.New()

	// If server and key provided, connect immediately
	if *serverAddr != "" && *key != "" {
		if err := tun.Start(*listenAddr, *serverAddr, *key); err != nil {
			log.Fatalf("start tunnel: %v", err)
		}
		log.Printf("tunnel connected to %s, SOCKS5 on %s", *serverAddr, *listenAddr)
	}

	srv := api.New(tun, *listenAddr)
	log.Printf("API listening on %s", *apiAddr)
	if err := http.ListenAndServe(*apiAddr, srv.Handler()); err != nil {
		log.Fatalf("api: %v", err)
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd daemon && go build ./cmd/
```

Expected: builds without errors.

- [ ] **Step 3: Commit**

```bash
git add daemon/cmd/
git commit -m "feat: add daemon entry point with CLI flags"
```

---

## Phase 2: Integration

### Task 8: End-to-End Test

**Files:**
- Create: `test/e2e_test.go`
- Create: `test/go.mod`
- Update: `go.work`

- [ ] **Step 1: Initialize test module**

```bash
mkdir -p test
cd test && go mod init smurov-proxy/test
cd /Users/ilyasmurov/projects/smurov/proxy && go work use ./test
cd test && go mod edit -require smurov-proxy/pkg@v0.0.0
cd .. && go work sync
```

- [ ] **Step 2: Write e2e test**

Create `test/e2e_test.go`:

```go
package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
)

// startTLSServer starts a proxy server on a random port for testing.
func startTLSServer(t *testing.T, key string) (addr string, cleanup func()) {
	t.Helper()

	// Generate self-signed cert in memory
	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  privKey,
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if err := proto.ReadAuth(c, key); err != nil {
					proto.WriteResult(c, false)
					return
				}
				proto.WriteResult(c, true)

				addr, port, err := proto.ReadConnect(c)
				if err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", addr, port), 5*time.Second)
				if err != nil {
					proto.WriteResult(c, false)
					return
				}
				defer target.Close()
				proto.WriteResult(c, true)
				proto.Relay(c, target)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func TestEndToEnd(t *testing.T) {
	key := auth.GenerateKey()

	// Start a target HTTP server
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from target"))
	}))
	defer target.Close()

	// Start proxy server
	proxyAddr, cleanup := startTLSServer(t, key)
	defer cleanup()

	// Simulate what the daemon does: connect to proxy, request target
	tlsConn, err := tls.Dial("tcp", proxyAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer tlsConn.Close()

	// Auth
	if err := proto.WriteAuth(tlsConn, key); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		t.Fatalf("auth failed: ok=%v err=%v", ok, err)
	}

	// Parse target address
	host, portStr, _ := net.SplitHostPort(target.Listener.Addr().String())
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	// Connect
	if err := proto.WriteConnect(tlsConn, host, port); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		t.Fatalf("connect failed: ok=%v err=%v", ok, err)
	}

	// Send HTTP request through the tunnel
	fmt.Fprintf(tlsConn, "GET / HTTP/1.0\r\nHost: localhost\r\n\r\n")
	resp, err := io.ReadAll(tlsConn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(resp) == 0 {
		t.Fatal("empty response")
	}
	body := string(resp)
	if !contains(body, "hello from target") {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestEndToEnd_BadKey(t *testing.T) {
	key := auth.GenerateKey()
	wrongKey := auth.GenerateKey()

	proxyAddr, cleanup := startTLSServer(t, key)
	defer cleanup()

	tlsConn, err := tls.Dial("tcp", proxyAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer tlsConn.Close()

	proto.WriteAuth(tlsConn, wrongKey)
	ok, err := proto.ReadResult(tlsConn)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if ok {
		t.Fatal("expected auth failure with wrong key")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run e2e tests**

```bash
cd test && go test -v -timeout 30s
```

Expected: both tests PASS.

- [ ] **Step 4: Commit**

```bash
git add go.work test/
git commit -m "test: add end-to-end proxy integration tests"
```

---

## Phase 3: Electron Client

### Task 9: Electron Project Setup

**Files:**
- Create: `client/package.json`
- Create: `client/tsconfig.json`
- Create: `client/vite.config.ts`
- Create: `client/electron-builder.json`
- Create: `client/index.html`
- Create: `client/src/renderer/main.tsx`

- [ ] **Step 1: Create package.json**

Create `client/package.json`:

```json
{
  "name": "smurov-proxy",
  "version": "1.0.0",
  "private": true,
  "main": "dist-electron/main/index.js",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build && tsc -p tsconfig.electron.json",
    "preview": "vite preview"
  },
  "dependencies": {
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  },
  "devDependencies": {
    "@types/react": "^19.0.0",
    "@types/react-dom": "^19.0.0",
    "@vitejs/plugin-react": "^4.3.0",
    "electron": "^33.0.0",
    "electron-builder": "^25.0.0",
    "typescript": "^5.6.0",
    "vite": "^6.0.0",
    "vite-plugin-electron": "^0.28.0",
    "vite-plugin-electron-renderer": "^0.14.0"
  }
}
```

- [ ] **Step 2: Create tsconfig.json**

Create `client/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "ESNext",
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "jsx": "react-jsx",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "outDir": "dist"
  },
  "include": ["src/renderer"]
}
```

Create `client/tsconfig.electron.json`:

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "module": "CommonJS",
    "moduleResolution": "node",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "outDir": "dist-electron"
  },
  "include": ["src/main"]
}
```

- [ ] **Step 3: Create vite.config.ts**

Create `client/vite.config.ts`:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import electron from "vite-plugin-electron";
import renderer from "vite-plugin-electron-renderer";

export default defineConfig({
  plugins: [
    react(),
    electron([
      {
        entry: "src/main/index.ts",
        vite: {
          build: {
            outDir: "dist-electron/main",
          },
        },
      },
    ]),
    renderer(),
  ],
});
```

- [ ] **Step 4: Create electron-builder.json**

Create `client/electron-builder.json`:

```json
{
  "appId": "com.smurov.proxy",
  "productName": "SmurovProxy",
  "directories": {
    "output": "release"
  },
  "files": [
    "dist/**/*",
    "dist-electron/**/*"
  ],
  "extraResources": [
    {
      "from": "resources/",
      "to": "resources/",
      "filter": ["daemon-*"]
    }
  ],
  "mac": {
    "target": ["dmg"],
    "icon": null
  },
  "win": {
    "target": ["nsis"],
    "icon": null
  }
}
```

- [ ] **Step 5: Create index.html and renderer entry**

Create `client/index.html`:

```html
<!DOCTYPE html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>SmurovProxy</title>
    <style>
      * { margin: 0; padding: 0; box-sizing: border-box; }
      body {
        font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        background: #1a1a2e;
        color: #eee;
        min-height: 100vh;
      }
      #root { padding: 24px; }
    </style>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/renderer/main.tsx"></script>
  </body>
</html>
```

Create `client/src/renderer/main.tsx`:

```tsx
import { createRoot } from "react-dom/client";
import { App } from "./App";

createRoot(document.getElementById("root")!).render(<App />);
```

Create placeholder `client/src/renderer/App.tsx`:

```tsx
export function App() {
  return <div>SmurovProxy</div>;
}
```

- [ ] **Step 6: Install dependencies and verify**

```bash
cd client && npm install
```

Expected: installs without errors.

- [ ] **Step 7: Commit**

```bash
git add client/
git commit -m "feat: scaffold Electron project with Vite and React"
```

---

### Task 10: React UI Components

**Files:**
- Create: `client/src/renderer/hooks/useDaemon.ts`
- Create: `client/src/renderer/components/StatusBar.tsx`
- Create: `client/src/renderer/components/Settings.tsx`
- Create: `client/src/renderer/components/ConnectionButton.tsx`
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Create useDaemon hook**

Create `client/src/renderer/hooks/useDaemon.ts`:

```ts
import { useState, useEffect, useCallback } from "react";

const API_BASE = "http://127.0.0.1:9090";

interface DaemonStatus {
  status: "connected" | "disconnected";
  uptime: number;
}

export function useDaemon() {
  const [status, setStatus] = useState<DaemonStatus>({
    status: "disconnected",
    uptime: 0,
  });
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const fetchStatus = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/status`);
      if (res.ok) {
        setStatus(await res.json());
        setError(null);
      }
    } catch {
      setError("Daemon not running");
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 2000);
    return () => clearInterval(interval);
  }, [fetchStatus]);

  const connect = useCallback(
    async (server: string, key: string) => {
      setLoading(true);
      setError(null);
      try {
        const res = await fetch(`${API_BASE}/connect`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ server, key }),
        });
        if (!res.ok) {
          setError(await res.text());
        } else {
          await fetchStatus();
        }
      } catch {
        setError("Failed to connect");
      } finally {
        setLoading(false);
      }
    },
    [fetchStatus]
  );

  const disconnect = useCallback(async () => {
    setLoading(true);
    try {
      await fetch(`${API_BASE}/disconnect`, { method: "POST" });
      await fetchStatus();
    } catch {
      setError("Failed to disconnect");
    } finally {
      setLoading(false);
    }
  }, [fetchStatus]);

  return { status, error, loading, connect, disconnect };
}
```

- [ ] **Step 2: Create StatusBar component**

Create `client/src/renderer/components/StatusBar.tsx`:

```tsx
interface Props {
  status: string;
  uptime: number;
  error: string | null;
}

export function StatusBar({ status, uptime, error }: Props) {
  const color = status === "connected" ? "#4caf50" : "#f44336";
  const label = status.charAt(0).toUpperCase() + status.slice(1);

  return (
    <div style={{ marginBottom: 20 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <div
          style={{
            width: 12,
            height: 12,
            borderRadius: "50%",
            background: color,
          }}
        />
        <span style={{ fontSize: 18, fontWeight: 600 }}>{label}</span>
        {status === "connected" && (
          <span style={{ color: "#aaa", fontSize: 14 }}>
            {formatUptime(uptime)}
          </span>
        )}
      </div>
      {error && (
        <div style={{ color: "#f44336", marginTop: 8, fontSize: 13 }}>
          {error}
        </div>
      )}
    </div>
  );
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return [h, m, s].map((v) => String(v).padStart(2, "0")).join(":");
}
```

- [ ] **Step 3: Create Settings component**

Create `client/src/renderer/components/Settings.tsx`:

```tsx
interface Props {
  server: string;
  secretKey: string;
  onServerChange: (v: string) => void;
  onKeyChange: (v: string) => void;
  disabled: boolean;
}

export function Settings({
  server,
  secretKey,
  onServerChange,
  onKeyChange,
  disabled,
}: Props) {
  const inputStyle: React.CSSProperties = {
    width: "100%",
    padding: "10px 12px",
    background: "#16213e",
    border: "1px solid #333",
    borderRadius: 6,
    color: "#eee",
    fontSize: 14,
    marginTop: 4,
  };

  return (
    <div style={{ marginBottom: 20 }}>
      <label style={{ display: "block", marginBottom: 12 }}>
        <span style={{ fontSize: 13, color: "#aaa" }}>Server address</span>
        <input
          style={inputStyle}
          value={server}
          onChange={(e) => onServerChange(e.target.value)}
          placeholder="ip:port"
          disabled={disabled}
        />
      </label>
      <label style={{ display: "block" }}>
        <span style={{ fontSize: 13, color: "#aaa" }}>Secret key</span>
        <input
          style={inputStyle}
          type="password"
          value={secretKey}
          onChange={(e) => onKeyChange(e.target.value)}
          placeholder="hex key"
          disabled={disabled}
        />
      </label>
    </div>
  );
}
```

- [ ] **Step 4: Create ConnectionButton component**

Create `client/src/renderer/components/ConnectionButton.tsx`:

```tsx
interface Props {
  connected: boolean;
  loading: boolean;
  onConnect: () => void;
  onDisconnect: () => void;
}

export function ConnectionButton({
  connected,
  loading,
  onConnect,
  onDisconnect,
}: Props) {
  const bg = connected ? "#f44336" : "#4caf50";
  const label = loading
    ? "..."
    : connected
      ? "Disconnect"
      : "Connect";

  return (
    <button
      onClick={connected ? onDisconnect : onConnect}
      disabled={loading}
      style={{
        width: "100%",
        padding: "12px 0",
        background: bg,
        color: "#fff",
        border: "none",
        borderRadius: 8,
        fontSize: 16,
        fontWeight: 600,
        cursor: loading ? "wait" : "pointer",
        opacity: loading ? 0.7 : 1,
      }}
    >
      {label}
    </button>
  );
}
```

- [ ] **Step 5: Wire up App.tsx**

Replace `client/src/renderer/App.tsx`:

```tsx
import { useState } from "react";
import { useDaemon } from "./hooks/useDaemon";
import { StatusBar } from "./components/StatusBar";
import { Settings } from "./components/Settings";
import { ConnectionButton } from "./components/ConnectionButton";

const STORAGE_KEY = "smurov-proxy-settings";

function loadSettings() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) return JSON.parse(raw);
  } catch {}
  return { server: "", key: "" };
}

function saveSettings(server: string, key: string) {
  localStorage.setItem(STORAGE_KEY, JSON.stringify({ server, key }));
}

export function App() {
  const saved = loadSettings();
  const [server, setServer] = useState(saved.server);
  const [key, setKey] = useState(saved.key);
  const { status, error, loading, connect, disconnect } = useDaemon();

  const isConnected = status.status === "connected";

  const handleConnect = () => {
    saveSettings(server, key);
    connect(server, key);
  };

  return (
    <div style={{ maxWidth: 360, margin: "0 auto" }}>
      <h1 style={{ fontSize: 20, marginBottom: 20, fontWeight: 700 }}>
        SmurovProxy
      </h1>
      <StatusBar status={status.status} uptime={status.uptime} error={error} />
      <Settings
        server={server}
        secretKey={key}
        onServerChange={setServer}
        onKeyChange={setKey}
        disabled={isConnected}
      />
      <ConnectionButton
        connected={isConnected}
        loading={loading}
        onConnect={handleConnect}
        onDisconnect={disconnect}
      />
    </div>
  );
}
```

- [ ] **Step 6: Verify build**

```bash
cd client && npx tsc --noEmit
```

Expected: no type errors.

- [ ] **Step 7: Commit**

```bash
git add client/src/
git commit -m "feat: add React UI with connection controls and status"
```

---

### Task 11: Electron Main Process + Tray

**Files:**
- Create: `client/src/main/index.ts`
- Create: `client/src/main/daemon.ts`

- [ ] **Step 1: Create daemon process manager**

Create `client/src/main/daemon.ts`:

```ts
import { ChildProcess, spawn } from "child_process";
import path from "path";
import { app } from "electron";

let daemonProcess: ChildProcess | null = null;

function getDaemonPath(): string {
  const resourcesPath = app.isPackaged
    ? process.resourcesPath
    : path.join(__dirname, "../../resources");

  const platform = process.platform;
  const arch = process.arch;

  if (platform === "win32") {
    return path.join(resourcesPath, "resources", "daemon-windows.exe");
  }
  return path.join(resourcesPath, "resources", `daemon-${platform}-${arch}`);
}

export function startDaemon(): void {
  if (daemonProcess) return;

  const daemonPath = getDaemonPath();
  daemonProcess = spawn(daemonPath, ["-api", "127.0.0.1:9090", "-listen", "127.0.0.1:1080"], {
    stdio: "pipe",
  });

  daemonProcess.stdout?.on("data", (data: Buffer) => {
    console.log(`[daemon] ${data.toString().trim()}`);
  });

  daemonProcess.stderr?.on("data", (data: Buffer) => {
    console.error(`[daemon] ${data.toString().trim()}`);
  });

  daemonProcess.on("exit", (code) => {
    console.log(`[daemon] exited with code ${code}`);
    daemonProcess = null;
  });
}

export function stopDaemon(): void {
  if (daemonProcess) {
    daemonProcess.kill();
    daemonProcess = null;
  }
}
```

- [ ] **Step 2: Create main process with tray**

Create `client/src/main/index.ts`:

```ts
import { app, BrowserWindow, Tray, Menu, nativeImage } from "electron";
import path from "path";
import { startDaemon, stopDaemon } from "./daemon";

let mainWindow: BrowserWindow | null = null;
let tray: Tray | null = null;

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 400,
    height: 500,
    resizable: false,
    webPreferences: {
      nodeIntegration: false,
      contextIsolation: true,
    },
  });

  if (process.env.VITE_DEV_SERVER_URL) {
    mainWindow.loadURL(process.env.VITE_DEV_SERVER_URL);
  } else {
    mainWindow.loadFile(path.join(__dirname, "../dist/index.html"));
  }

  // Minimize to tray instead of closing
  mainWindow.on("close", (e) => {
    if (mainWindow && !app.isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    }
  });
}

function createTray() {
  // Create a simple 16x16 tray icon
  const icon = nativeImage.createEmpty();
  tray = new Tray(icon);
  tray.setToolTip("SmurovProxy");

  const contextMenu = Menu.buildFromTemplate([
    {
      label: "Show",
      click: () => mainWindow?.show(),
    },
    { type: "separator" },
    {
      label: "Quit",
      click: () => {
        (app as any).isQuitting = true;
        app.quit();
      },
    },
  ]);

  tray.setContextMenu(contextMenu);

  tray.on("double-click", () => {
    mainWindow?.show();
  });
}

app.whenReady().then(() => {
  startDaemon();
  createWindow();
  createTray();
});

app.on("before-quit", () => {
  stopDaemon();
});

app.on("window-all-closed", () => {
  // Don't quit on macOS — keep in tray
  if (process.platform !== "darwin") {
    app.quit();
  }
});

app.on("activate", () => {
  mainWindow?.show();
});
```

- [ ] **Step 3: Verify compilation**

```bash
cd client && npx tsc -p tsconfig.electron.json --noEmit
```

Expected: no type errors.

- [ ] **Step 4: Commit**

```bash
git add client/src/main/
git commit -m "feat: add Electron main process with daemon management and tray"
```

---

## Phase 4: Build

### Task 12: Makefile

**Files:**
- Create: `Makefile`
- Create: `.gitignore`

- [ ] **Step 1: Create Makefile**

Create `Makefile`:

```makefile
.PHONY: build-server build-daemon build-client test clean

# Server (Linux for VPS)
build-server:
	cd server && GOOS=linux GOARCH=amd64 go build -o ../dist/server-linux ./cmd

# Go daemon (all platforms, output to client/resources for Electron bundling)
build-daemon:
	mkdir -p client/resources
	cd daemon && GOOS=darwin GOARCH=arm64 go build -o ../client/resources/daemon-darwin-arm64 ./cmd
	cd daemon && GOOS=darwin GOARCH=amd64 go build -o ../client/resources/daemon-darwin-amd64 ./cmd
	cd daemon && GOOS=windows GOARCH=amd64 go build -o ../client/resources/daemon-windows.exe ./cmd

# Electron GUI
build-client: build-daemon
	cd client && npm run build && npx electron-builder

# Run all Go tests
test:
	cd pkg && go test ./...
	cd daemon && go test ./...
	cd test && go test -v -timeout 30s

# Dev: run Electron in dev mode (daemon must be started separately)
dev:
	cd client && npm run dev

clean:
	rm -rf dist/ client/dist/ client/dist-electron/ client/release/ client/resources/daemon-*
```

- [ ] **Step 2: Create .gitignore**

Create `.gitignore`:

```
# Go
dist/
*.exe

# Node
client/node_modules/
client/dist/
client/dist-electron/
client/release/
client/resources/daemon-*

# TLS certs
cert.pem
key.pem

# OS
.DS_Store
Thumbs.db
```

- [ ] **Step 3: Verify Go tests pass**

```bash
make test
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add Makefile .gitignore
git commit -m "feat: add Makefile and .gitignore"
```
