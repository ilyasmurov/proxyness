# TUN Transparent Proxy Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add TUN-based transparent proxy that captures all TCP/UDP traffic (including Telegram, Discord) with split tunneling per-app, while keeping SOCKS5 as fallback.

**Architecture:** gVisor netstack processes IP packets from TUN device in userspace, surfaces TCP/UDP connections, routes them through TLS tunnel to server (proxy) or directly to internet (bypass) based on per-app rules. Privileged helper creates TUN device and manages routes. Server extended with UDP relay protocol.

**Tech Stack:** Go (gVisor netstack, wireguard/tun), Electron/React (client UI), wintun.dll (Windows TUN)

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `pkg/proto/udp.go` | UDP frame encode/decode for TLS transport |
| `pkg/proto/udp_test.go` | Tests for UDP framing |
| `pkg/proto/msgtype.go` | Message type constants (TCP=0x01, UDP=0x02) |
| `server/internal/proxy/tcp.go` | TCP proxy handler (extracted from cmd/main.go) |
| `server/internal/proxy/udp.go` | UDP relay handler |
| `server/internal/proxy/proxy.go` | Shared auth + device lookup, dispatch by msg type |
| `daemon/internal/tun/device.go` | TUN device + gVisor netstack setup |
| `daemon/internal/tun/engine.go` | Main loop: intercept TCP/UDP, route proxy/bypass |
| `daemon/internal/tun/rules.go` | Split tunneling rules (proxy all except / proxy only) |
| `daemon/internal/tun/procinfo_darwin.go` | macOS PID lookup via sysctl |
| `daemon/internal/tun/procinfo_windows.go` | Windows PID lookup via iphlpapi |
| `daemon/internal/tun/procinfo.go` | Shared interface for PID lookup |
| `daemon/internal/tun/device_test.go` | Tests for netstack setup |
| `daemon/internal/tun/rules_test.go` | Tests for rules engine |
| `helper/cmd/main.go` | Helper binary entry point |
| `helper/cmd/main_darwin.go` | macOS: create utun, set routes, IPC via unix socket |
| `helper/cmd/main_windows.go` | Windows: create wintun, set routes, IPC via named pipe |
| `helper/go.mod` | Helper Go module |
| `client/src/renderer/components/ModeSelector.tsx` | TUN/SOCKS5 mode toggle |
| `client/src/renderer/components/AppRules.tsx` | Split tunneling app list UI |

### Modified files

| File | Changes |
|------|---------|
| `go.work` | Add `./helper` module |
| `daemon/go.mod` | Add gVisor, wireguard/tun dependencies |
| `daemon/internal/api/api.go` | Add `/tun/*` endpoints |
| `daemon/cmd/main.go` | Wire TUN engine into daemon |
| `server/cmd/main.go` | Replace inline handleProxy with proxy package |
| `client/src/main/index.ts` | Add TUN IPC handlers |
| `client/src/main/preload.ts` | Expose TUN bridge to renderer |
| `client/src/main/daemon.ts` | Bundle helper binary path |
| `client/src/renderer/App.tsx` | Add mode selector, app rules UI |
| `client/electron-builder.json` | Add helper + wintun to extraResources |
| `Makefile` | Add build-helper target |

---

## Phase 1: Server UDP Relay

### Task 1: UDP frame protocol (`pkg/proto/`)

**Files:**
- Create: `pkg/proto/msgtype.go`
- Create: `pkg/proto/udp.go`
- Create: `pkg/proto/udp_test.go`

- [ ] **Step 1: Create message type constants**

Create `pkg/proto/msgtype.go`:

```go
package proto

const (
	MsgTypeTCP = 0x01
	MsgTypeUDP = 0x02
)

// WriteMsgType sends a 1-byte message type.
func WriteMsgType(conn interface{ Write([]byte) (int, error) }, t byte) error {
	_, err := conn.Write([]byte{t})
	return err
}

// ReadMsgType reads a 1-byte message type.
func ReadMsgType(conn interface{ Read([]byte) (int, error) }) (byte, error) {
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	return buf[0], err
}
```

- [ ] **Step 2: Create UDP frame encoder/decoder**

Create `pkg/proto/udp.go`:

```go
package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// UDP frame format: [2 bytes length][addr_type + addr + port][payload]
// Max UDP datagram: 65535 bytes

// WriteUDPFrame writes a single UDP datagram as a framed message.
func WriteUDPFrame(w io.Writer, addr string, port uint16, payload []byte) error {
	addrBytes := encodeAddr(addr, port)
	frameLen := uint16(len(addrBytes) + len(payload))

	buf := make([]byte, 2+len(addrBytes)+len(payload))
	binary.BigEndian.PutUint16(buf[0:2], frameLen)
	copy(buf[2:2+len(addrBytes)], addrBytes)
	copy(buf[2+len(addrBytes):], payload)

	_, err := w.Write(buf)
	return err
}

// ReadUDPFrame reads a single framed UDP datagram.
func ReadUDPFrame(r io.Reader) (addr string, port uint16, payload []byte, err error) {
	lenBuf := make([]byte, 2)
	if _, err = io.ReadFull(r, lenBuf); err != nil {
		return
	}
	frameLen := binary.BigEndian.Uint16(lenBuf)
	if frameLen == 0 {
		err = fmt.Errorf("empty UDP frame")
		return
	}

	frame := make([]byte, frameLen)
	if _, err = io.ReadFull(r, frame); err != nil {
		return
	}

	// Parse addr from frame
	var addrLen int
	addr, port, addrLen, err = decodeAddr(frame)
	if err != nil {
		return
	}
	payload = frame[addrLen:]
	return
}

func encodeAddr(addr string, port uint16) []byte {
	ip := net.ParseIP(addr)
	if ip4 := ip.To4(); ip4 != nil {
		buf := make([]byte, 1+4+2)
		buf[0] = AddrTypeIPv4
		copy(buf[1:5], ip4)
		binary.BigEndian.PutUint16(buf[5:], port)
		return buf
	} else if ip16 := ip.To16(); ip16 != nil {
		buf := make([]byte, 1+16+2)
		buf[0] = AddrTypeIPv6
		copy(buf[1:17], ip16)
		binary.BigEndian.PutUint16(buf[17:], port)
		return buf
	}
	buf := make([]byte, 1+1+len(addr)+2)
	buf[0] = AddrTypeDomain
	buf[1] = byte(len(addr))
	copy(buf[2:2+len(addr)], addr)
	binary.BigEndian.PutUint16(buf[2+len(addr):], port)
	return buf
}

func decodeAddr(data []byte) (addr string, port uint16, totalLen int, err error) {
	if len(data) < 1 {
		err = fmt.Errorf("empty addr data")
		return
	}
	switch data[0] {
	case AddrTypeIPv4:
		if len(data) < 7 {
			err = fmt.Errorf("short IPv4 addr")
			return
		}
		addr = net.IP(data[1:5]).String()
		port = binary.BigEndian.Uint16(data[5:7])
		totalLen = 7
	case AddrTypeDomain:
		if len(data) < 2 {
			err = fmt.Errorf("short domain addr")
			return
		}
		dLen := int(data[1])
		if len(data) < 2+dLen+2 {
			err = fmt.Errorf("short domain data")
			return
		}
		addr = string(data[2 : 2+dLen])
		port = binary.BigEndian.Uint16(data[2+dLen : 4+dLen])
		totalLen = 4 + dLen
	case AddrTypeIPv6:
		if len(data) < 19 {
			err = fmt.Errorf("short IPv6 addr")
			return
		}
		addr = net.IP(data[1:17]).String()
		port = binary.BigEndian.Uint16(data[17:19])
		totalLen = 19
	default:
		err = fmt.Errorf("unknown addr type: 0x%02x", data[0])
	}
	return
}
```

- [ ] **Step 3: Write tests for UDP framing**

Create `pkg/proto/udp_test.go`:

```go
package proto

import (
	"bytes"
	"testing"
)

func TestUDPFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		port    uint16
		payload []byte
	}{
		{"ipv4", "1.2.3.4", 53, []byte("hello")},
		{"ipv6", "::1", 443, []byte("world")},
		{"domain", "example.com", 8080, []byte{0x01, 0x02, 0x03}},
		{"empty payload", "10.0.0.1", 1234, []byte{}},
		{"large payload", "192.168.1.1", 5000, bytes.Repeat([]byte("x"), 1400)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteUDPFrame(&buf, tt.addr, tt.port, tt.payload); err != nil {
				t.Fatalf("write: %v", err)
			}

			addr, port, payload, err := ReadUDPFrame(&buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if addr != tt.addr {
				t.Errorf("addr: got %q, want %q", addr, tt.addr)
			}
			if port != tt.port {
				t.Errorf("port: got %d, want %d", port, tt.port)
			}
			if !bytes.Equal(payload, tt.payload) {
				t.Errorf("payload: got %d bytes, want %d", len(payload), len(tt.payload))
			}
		})
	}
}

func TestMultipleUDPFrames(t *testing.T) {
	var buf bytes.Buffer

	WriteUDPFrame(&buf, "1.1.1.1", 53, []byte("query1"))
	WriteUDPFrame(&buf, "8.8.8.8", 53, []byte("query2"))

	addr1, port1, p1, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if addr1 != "1.1.1.1" || port1 != 53 || string(p1) != "query1" {
		t.Errorf("frame 1 mismatch")
	}

	addr2, port2, p2, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if addr2 != "8.8.8.8" || port2 != 53 || string(p2) != "query2" {
		t.Errorf("frame 2 mismatch")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd pkg && go test ./proto/ -v`
Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add pkg/proto/msgtype.go pkg/proto/udp.go pkg/proto/udp_test.go
git commit -m "feat(proto): add UDP frame protocol and message type constants"
```

---

### Task 2: Server proxy package — extract TCP + add UDP handler

**Files:**
- Create: `server/internal/proxy/proxy.go`
- Create: `server/internal/proxy/tcp.go`
- Create: `server/internal/proxy/udp.go`
- Modify: `server/cmd/main.go`

- [ ] **Step 1: Create proxy package with shared auth dispatch**

Create `server/internal/proxy/proxy.go`:

```go
package proxy

import (
	"io"
	"log"
	"net"
	"time"

	"proxyness/pkg/auth"
	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
	"proxyness/server/internal/stats"
)

type Handler struct {
	DB      *db.DB
	Tracker *stats.Tracker
}

func (h *Handler) Handle(conn net.Conn) {
	defer conn.Close()

	keys, err := h.DB.GetActiveKeys()
	if err != nil || len(keys) == 0 {
		log.Printf("no active keys: %v", err)
		return
	}

	msg := make([]byte, auth.AuthMsgLen)
	if _, err := io.ReadFull(conn, msg); err != nil {
		return
	}
	matchedKey, err := auth.ValidateAuthMessageMulti(keys, msg)
	if err != nil {
		proto.WriteResult(conn, false)
		log.Printf("auth failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	proto.WriteResult(conn, true)

	device, err := h.DB.GetDeviceByKey(matchedKey)
	if err != nil {
		log.Printf("device lookup: %v", err)
		return
	}

	// Read message type
	msgType, err := proto.ReadMsgType(conn)
	if err != nil {
		log.Printf("read msg type: %v", err)
		return
	}

	switch msgType {
	case proto.MsgTypeTCP:
		h.handleTCP(conn, device)
	case proto.MsgTypeUDP:
		h.handleUDP(conn, device)
	default:
		log.Printf("unknown msg type: 0x%02x", msgType)
	}
}

func recordTraffic(database *db.DB, deviceID int64, bytesIn, bytesOut int64) {
	hour := time.Now().Truncate(time.Hour)
	database.RecordTraffic(deviceID, hour, bytesIn, bytesOut, 1)
}
```

- [ ] **Step 2: Extract TCP handler**

Create `server/internal/proxy/tcp.go`:

```go
package proxy

import (
	"fmt"
	"log"
	"net"
	"time"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

func (h *Handler) handleTCP(conn net.Conn, device *db.Device) {
	destAddr, port, err := proto.ReadConnect(conn)
	if err != nil {
		log.Printf("connect read: %v", err)
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", destAddr, port), 10*time.Second)
	if err != nil {
		proto.WriteResult(conn, false)
		return
	}
	defer target.Close()
	proto.WriteResult(conn, true)

	connID := h.Tracker.Add(device.ID, device.Name, device.UserName)
	proto.CountingRelay(conn, target, func(in, out int64) {
		h.Tracker.AddBytes(connID, in, out)
	})

	info := h.Tracker.Remove(connID)
	if info != nil {
		recordTraffic(h.DB, device.ID, info.BytesIn, info.BytesOut)
	}
}
```

- [ ] **Step 3: Add UDP relay handler**

Create `server/internal/proxy/udp.go`:

```go
package proxy

import (
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"proxyness/pkg/proto"
	"proxyness/server/internal/db"
)

const udpTimeout = 60 * time.Second
const udpBufSize = 65535

func (h *Handler) handleUDP(conn net.Conn, device *db.Device) {
	connID := h.Tracker.Add(device.ID, device.Name, device.UserName)
	var totalIn, totalOut int64
	var mu sync.Mutex

	// Map of target addr -> *net.UDPConn for reusing UDP sockets
	udpConns := make(map[string]*net.UDPConn)
	var connsMu sync.Mutex

	defer func() {
		connsMu.Lock()
		for _, uc := range udpConns {
			uc.Close()
		}
		connsMu.Unlock()

		h.Tracker.Remove(connID)
		mu.Lock()
		recordTraffic(h.DB, device.ID, totalIn, totalOut)
		mu.Unlock()
	}()

	// Read frames from TLS, send as UDP datagrams
	// Simultaneously read UDP responses and send as frames back
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			addr, port, payload, err := proto.ReadUDPFrame(conn)
			if err != nil {
				return
			}

			mu.Lock()
			totalOut += int64(len(payload))
			mu.Unlock()
			h.Tracker.AddBytes(connID, 0, int64(len(payload)))

			target := fmt.Sprintf("%s:%d", addr, port)

			connsMu.Lock()
			uc, exists := udpConns[target]
			if !exists {
				raddr, err := net.ResolveUDPAddr("udp", target)
				if err != nil {
					connsMu.Unlock()
					log.Printf("resolve udp %s: %v", target, err)
					continue
				}
				uc, err = net.DialUDP("udp", nil, raddr)
				if err != nil {
					connsMu.Unlock()
					log.Printf("dial udp %s: %v", target, err)
					continue
				}
				udpConns[target] = uc

				// Read responses from this UDP socket
				go func(uc *net.UDPConn, addr string, port uint16) {
					buf := make([]byte, udpBufSize)
					for {
						uc.SetReadDeadline(time.Now().Add(udpTimeout))
						n, err := uc.Read(buf)
						if err != nil {
							return
						}
						mu.Lock()
						totalIn += int64(n)
						mu.Unlock()
						h.Tracker.AddBytes(connID, int64(n), 0)

						if err := proto.WriteUDPFrame(conn, addr, port, buf[:n]); err != nil {
							return
						}
					}
				}(uc, addr, port)
			}
			connsMu.Unlock()

			uc.SetWriteDeadline(time.Now().Add(udpTimeout))
			if _, err := uc.Write(payload); err != nil {
				log.Printf("udp write to %s: %v", target, err)
			}
		}
	}()

	<-done
}
```

- [ ] **Step 4: Update server main to use proxy package**

Modify `server/cmd/main.go` — replace the inline `handleProxy` function. Change the import and `ListenerMux` call:

Replace the `handleProxy` function and update the mux creation:

```go
// Add import:
import "proxyness/server/internal/proxy"

// In main(), replace:
//   m := mux.NewListenerMux(ln,
//       func(conn net.Conn) { handleProxy(conn, database, tracker) },
//       adminHandler,
//   )
// With:
	proxyHandler := &proxy.Handler{DB: database, Tracker: tracker}
	m := mux.NewListenerMux(ln,
		func(conn net.Conn) { proxyHandler.Handle(conn) },
		adminHandler,
	)

// Delete the entire handleProxy function from main.go
```

Also remove unused imports from main.go that were only used by handleProxy: `"io"`, `"proxyness/pkg/auth"`, `"proxyness/pkg/proto"`, `"proxyness/server/internal/stats"`.

- [ ] **Step 5: Update daemon tunnel to send message type byte**

Modify `daemon/internal/tunnel/tunnel.go` — in `handleSOCKS`, after `WriteAuth` + `ReadResult`, add a message type byte before `WriteConnect`:

In the `handleSOCKS` method, after the auth result check and before `WriteConnect`, add:

```go
	// After: ok, err = proto.ReadResult(tlsConn) ... check

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		socks5.SendFailure(conn)
		log.Printf("[tunnel] msg type write failed: %v", err)
		return
	}

	// Before: if err := proto.WriteConnect(tlsConn, req.Addr, req.Port)
```

- [ ] **Step 6: Run tests**

Run: `make test`
Expected: All existing tests pass (server and daemon now use msg type byte).

- [ ] **Step 7: Commit**

```bash
git add server/internal/proxy/ daemon/internal/tunnel/tunnel.go server/cmd/main.go pkg/proto/msgtype.go
git commit -m "feat(server): extract proxy handlers, add UDP relay support"
```

---

## Phase 2: Helper Binary

### Task 3: Helper module — macOS utun + route management

**Files:**
- Create: `helper/go.mod`
- Create: `helper/cmd/main.go`
- Create: `helper/cmd/main_darwin.go`
- Modify: `go.work`

- [ ] **Step 1: Create helper module**

Create `helper/go.mod`:

```
module proxyness/helper

go 1.22

require golang.zx2c4.com/wireguard/tun v0.0.0-20231211153847-f252b8b10d22
```

Run: `cd helper && go mod tidy`

- [ ] **Step 2: Add helper to workspace**

Modify `go.work` — add `./helper`:

```
go 1.24

use (
	./daemon
	./helper
	./pkg
	./server
	./test
)
```

- [ ] **Step 3: Create helper entry point**

Create `helper/cmd/main.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
)

type Request struct {
	Action string `json:"action"` // "create" or "destroy"
}

type Response struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	TUNName  string `json:"tun_name,omitempty"`
	TUNFd    int    `json:"tun_fd,omitempty"`
}

func main() {
	log.SetPrefix("[helper] ")
	log.Printf("starting on %s/%s", runtime.GOOS, runtime.GOARCH)

	ln, err := listenIPC()
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	log.Printf("listening for connections")
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn)
	}
}

func handleConn(conn net.Conn) {
	defer conn.Close()

	var req Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		writeResponse(conn, Response{Error: fmt.Sprintf("decode: %v", err)})
		return
	}

	log.Printf("request: %s", req.Action)

	switch req.Action {
	case "create":
		resp := createTUN()
		writeResponse(conn, resp)
	case "destroy":
		resp := destroyTUN()
		writeResponse(conn, resp)
	default:
		writeResponse(conn, Response{Error: fmt.Sprintf("unknown action: %s", req.Action)})
	}
}

func writeResponse(conn net.Conn, resp Response) {
	if resp.Error != "" {
		resp.OK = false
	} else {
		resp.OK = true
	}
	json.NewEncoder(conn).Encode(resp)
}

// Platform-specific: listenIPC, createTUN, destroyTUN
```

- [ ] **Step 4: Create macOS implementation**

Create `helper/cmd/main_darwin.go`:

```go
//go:build darwin

package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"

	"golang.zx2c4.com/wireguard/tun"
)

const socketPath = "/var/run/proxyness-helper.sock"

var tunDevice tun.Device
var tunName string

func listenIPC() (net.Listener, error) {
	os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	os.Chmod(socketPath, 0666)
	return ln, nil
}

func createTUN() Response {
	if tunDevice != nil {
		return Response{TUNName: tunName, Error: "TUN already exists"}
	}

	dev, err := tun.CreateTUN("utun", 1500)
	if err != nil {
		return Response{Error: fmt.Sprintf("create tun: %v", err)}
	}

	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return Response{Error: fmt.Sprintf("get tun name: %v", err)}
	}

	tunDevice = dev
	tunName = name
	log.Printf("created TUN device: %s", name)

	// Assign IP to TUN interface
	run("ifconfig", name, "10.0.85.1", "10.0.85.1", "up")

	// Add default route through TUN
	// Save original default gateway first
	origGW := getDefaultGateway()
	if origGW != "" {
		// Route to VPN server via original gateway (so TLS tunnel doesn't loop)
		// This will be set by daemon which knows the server address
		// Add default route via TUN
		run("route", "add", "-net", "0.0.0.0/1", "-interface", name)
		run("route", "add", "-net", "128.0.0.0/1", "-interface", name)
	}

	return Response{TUNName: name}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	// Remove routes
	run("route", "delete", "-net", "0.0.0.0/1")
	run("route", "delete", "-net", "128.0.0.0/1")

	tunDevice.Close()
	tunDevice = nil
	tunName = ""
	log.Printf("destroyed TUN device")
	return Response{}
}

func getDefaultGateway() string {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
	}
	return ""
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
```

- [ ] **Step 5: Run build test**

Run: `cd helper && go build ./cmd/`
Expected: Compiles on macOS.

- [ ] **Step 6: Commit**

```bash
git add helper/ go.work
git commit -m "feat(helper): add privileged helper binary for TUN device management (macOS)"
```

---

### Task 4: Helper — Windows implementation

**Files:**
- Create: `helper/cmd/main_windows.go`

- [ ] **Step 1: Create Windows implementation**

Create `helper/cmd/main_windows.go`:

```go
//go:build windows

package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"

	"golang.zx2c4.com/wireguard/tun"
)

const pipeName = `\\.\pipe\proxyness-helper`

var tunDevice tun.Device
var tunName string

func listenIPC() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:9091")
	// Note: for production, use named pipe via
	// github.com/Microsoft/go-winio. TCP on localhost is simpler
	// and sufficient for single-user desktop app.
}

func createTUN() Response {
	if tunDevice != nil {
		return Response{TUNName: tunName, Error: "TUN already exists"}
	}

	dev, err := tun.CreateTUN("Proxyness", 1500)
	if err != nil {
		return Response{Error: fmt.Sprintf("create tun: %v", err)}
	}

	name, err := dev.Name()
	if err != nil {
		dev.Close()
		return Response{Error: fmt.Sprintf("get tun name: %v", err)}
	}

	tunDevice = dev
	tunName = name
	log.Printf("created TUN device: %s", name)

	// Configure IP on TUN adapter
	run("netsh", "interface", "ip", "set", "address", name, "static", "10.0.85.1", "255.255.255.0")

	// Add routes through TUN
	run("route", "add", "0.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")
	run("route", "add", "128.0.0.0", "mask", "128.0.0.0", "10.0.85.1", "metric", "5")

	return Response{TUNName: name}
}

func destroyTUN() Response {
	if tunDevice == nil {
		return Response{Error: "no TUN device"}
	}

	run("route", "delete", "0.0.0.0", "mask", "128.0.0.0")
	run("route", "delete", "128.0.0.0", "mask", "128.0.0.0")

	tunDevice.Close()
	tunDevice = nil
	tunName = ""
	log.Printf("destroyed TUN device")
	return Response{}
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("run %s %v: %v: %s", name, args, err, out)
	}
}
```

- [ ] **Step 2: Cross-compile test**

Run: `cd helper && GOOS=windows GOARCH=amd64 go build -o ../dist/helper-windows.exe ./cmd/`
Expected: Compiles.

- [ ] **Step 3: Commit**

```bash
git add helper/cmd/main_windows.go
git commit -m "feat(helper): add Windows TUN device management"
```

---

## Phase 3: Daemon TUN Engine

### Task 5: Add gVisor dependency to daemon

**Files:**
- Modify: `daemon/go.mod`

- [ ] **Step 1: Add gVisor and wireguard/tun dependencies**

Run:
```bash
cd daemon && go get gvisor.dev/gvisor@latest
cd daemon && go get golang.zx2c4.com/wireguard/tun@latest
cd daemon && go mod tidy
```

- [ ] **Step 2: Verify build**

Run: `cd daemon && go build ./...`
Expected: Compiles.

- [ ] **Step 3: Commit**

```bash
git add daemon/go.mod daemon/go.sum
git commit -m "chore(daemon): add gVisor and wireguard/tun dependencies"
```

---

### Task 6: TUN device + gVisor netstack setup

**Files:**
- Create: `daemon/internal/tun/device.go`
- Create: `daemon/internal/tun/device_test.go`

- [ ] **Step 1: Write test for netstack creation**

Create `daemon/internal/tun/device_test.go`:

```go
package tun

import (
	"testing"
)

func TestNewStack(t *testing.T) {
	s, ep, err := newStack(1500)
	if err != nil {
		t.Fatalf("newStack: %v", err)
	}
	defer s.Close()

	if ep == nil {
		t.Fatal("endpoint is nil")
	}

	// Verify stack has NIC
	nics := s.NICInfo()
	if len(nics) == 0 {
		t.Fatal("no NICs in stack")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd daemon && go test ./internal/tun/ -v -run TestNewStack`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement device.go**

Create `daemon/internal/tun/device.go`:

```go
package tun

import (
	"fmt"

	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const channelSize = 512

// newStack creates a gVisor network stack with a channel-based link endpoint.
// The channel endpoint is used for testing. In production, a TUN-based endpoint
// bridges real packets.
func newStack(mtu uint32) (*stack.Stack, *channel.Endpoint, error) {
	ep := channel.New(channelSize, mtu, "")

	s := stack.New(stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
	})

	nicID := tcpip.NICID(1)
	if err := s.CreateNIC(nicID, ep); err != nil {
		return nil, nil, fmt.Errorf("create NIC: %v", err)
	}

	// Accept packets to any destination (transparent proxy)
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)

	// Route all traffic to this NIC
	s.SetRouteTable([]tcpip.Route{
		{Destination: header.IPv4EmptySubnet, NIC: nicID},
		{Destination: header.IPv6EmptySubnet, NIC: nicID},
	})

	return s, ep, nil
}
```

- [ ] **Step 4: Run test**

Run: `cd daemon && go test ./internal/tun/ -v -run TestNewStack`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/device.go daemon/internal/tun/device_test.go
git commit -m "feat(tun): gVisor netstack creation with channel endpoint"
```

---

### Task 7: Split tunneling rules engine

**Files:**
- Create: `daemon/internal/tun/rules.go`
- Create: `daemon/internal/tun/rules_test.go`

- [ ] **Step 1: Write rules tests**

Create `daemon/internal/tun/rules_test.go`:

```go
package tun

import "testing"

func TestRulesProxyAll(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyAllExcept)
	r.SetApps([]string{"/usr/bin/curl"})

	if !r.ShouldProxy("/usr/bin/firefox") {
		t.Error("firefox should be proxied in proxy-all mode")
	}
	if r.ShouldProxy("/usr/bin/curl") {
		t.Error("curl should be excluded")
	}
	if !r.ShouldProxy("unknown-app") {
		t.Error("unknown should be proxied")
	}
}

func TestRulesProxyOnly(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyOnly)
	r.SetApps([]string{"/Applications/Telegram.app", "/Applications/Discord.app"})

	if !r.ShouldProxy("/Applications/Telegram.app") {
		t.Error("telegram should be proxied")
	}
	if !r.ShouldProxy("/Applications/Discord.app") {
		t.Error("discord should be proxied")
	}
	if r.ShouldProxy("/usr/bin/curl") {
		t.Error("curl should not be proxied")
	}
}

func TestRulesDefaultProxyAll(t *testing.T) {
	r := NewRules()
	if !r.ShouldProxy("anything") {
		t.Error("default mode should proxy everything")
	}
}

func TestRulesJSON(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyOnly)
	r.SetApps([]string{"app1", "app2"})

	data := r.ToJSON()
	r2 := NewRules()
	if err := r2.FromJSON(data); err != nil {
		t.Fatalf("from json: %v", err)
	}
	if r2.GetMode() != ModeProxyOnly {
		t.Error("mode mismatch")
	}
	if !r2.ShouldProxy("app1") {
		t.Error("app1 should be proxied")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd daemon && go test ./internal/tun/ -v -run TestRules`
Expected: FAIL — Rules type not defined.

- [ ] **Step 3: Implement rules.go**

Create `daemon/internal/tun/rules.go`:

```go
package tun

import (
	"encoding/json"
	"strings"
	"sync"
)

type Mode string

const (
	ModeProxyAllExcept Mode = "proxy_all_except"
	ModeProxyOnly      Mode = "proxy_only"
)

type Rules struct {
	mu   sync.RWMutex
	mode Mode
	apps map[string]bool // app path -> in list
}

type rulesJSON struct {
	Mode Mode     `json:"mode"`
	Apps []string `json:"apps"`
}

func NewRules() *Rules {
	return &Rules{
		mode: ModeProxyAllExcept,
		apps: make(map[string]bool),
	}
}

func (r *Rules) SetMode(m Mode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = m
}

func (r *Rules) GetMode() Mode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mode
}

func (r *Rules) SetApps(apps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apps = make(map[string]bool, len(apps))
	for _, a := range apps {
		r.apps[strings.ToLower(a)] = true
	}
}

func (r *Rules) GetApps() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]string, 0, len(r.apps))
	for a := range r.apps {
		apps = append(apps, a)
	}
	return apps
}

// ShouldProxy returns true if traffic from the given app path should go through the proxy.
func (r *Rules) ShouldProxy(appPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inList := r.apps[strings.ToLower(appPath)]

	switch r.mode {
	case ModeProxyAllExcept:
		return !inList // proxy everything except listed apps
	case ModeProxyOnly:
		return inList // proxy only listed apps
	default:
		return true // default: proxy all
	}
}

func (r *Rules) ToJSON() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]string, 0, len(r.apps))
	for a := range r.apps {
		apps = append(apps, a)
	}
	data, _ := json.Marshal(rulesJSON{
		Mode: r.mode,
		Apps: apps,
	})
	return data
}

func (r *Rules) FromJSON(data []byte) error {
	var rj rulesJSON
	if err := json.Unmarshal(data, &rj); err != nil {
		return err
	}
	r.SetMode(rj.Mode)
	r.SetApps(rj.Apps)
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd daemon && go test ./internal/tun/ -v -run TestRules`
Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/rules.go daemon/internal/tun/rules_test.go
git commit -m "feat(tun): split tunneling rules engine"
```

---

### Task 8: Process info — PID lookup by source port

**Files:**
- Create: `daemon/internal/tun/procinfo.go`
- Create: `daemon/internal/tun/procinfo_darwin.go`
- Create: `daemon/internal/tun/procinfo_windows.go`

- [ ] **Step 1: Create shared interface**

Create `daemon/internal/tun/procinfo.go`:

```go
package tun

// ProcessInfo looks up process information from network connections.
type ProcessInfo interface {
	// FindProcess returns the executable path for the process that owns
	// the given local TCP or UDP port. Returns empty string if not found.
	FindProcess(network string, localPort uint16) (path string, err error)
}
```

- [ ] **Step 2: Create macOS implementation**

Create `daemon/internal/tun/procinfo_darwin.go`:

```go
//go:build darwin

package tun

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type darwinProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &darwinProcessInfo{}
}

func (d *darwinProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	var sysctlName string
	switch network {
	case "tcp":
		sysctlName = "net.inet.tcp.pcblist_n"
	case "udp":
		sysctlName = "net.inet.udp.pcblist_n"
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}

	data, err := unix.SysctlRaw(sysctlName)
	if err != nil {
		return "", fmt.Errorf("sysctl %s: %w", sysctlName, err)
	}

	// Parse xinpgen header (24 bytes)
	if len(data) < 24 {
		return "", nil
	}

	itemSize := int(binary.LittleEndian.Uint32(data[0:4]))
	if itemSize == 0 {
		return "", nil
	}

	// Walk entries
	for offset := itemSize; offset+itemSize <= len(data); offset += itemSize {
		entry := data[offset : offset+itemSize]
		if len(entry) < 188 {
			continue
		}

		// Local port at offset 18 (network byte order)
		lport := binary.BigEndian.Uint16(entry[18:20])
		if lport != localPort {
			continue
		}

		// PID at offset 172 (xsocket_n.so_last_pid)
		pid := binary.LittleEndian.Uint32(entry[172:176])
		if pid == 0 {
			continue
		}

		path, err := getExecPath(pid)
		if err != nil {
			continue
		}
		return path, nil
	}

	return "", nil
}

func getExecPath(pid uint32) (string, error) {
	const (
		procPIDPathInfo     = 0xb
		procPIDPathInfoSize = 1024
		procCallNumPIDInfo  = 0x2
	)

	buf := make([]byte, procPIDPathInfoSize)
	_, _, errno := syscall.Syscall6(
		syscall.SYS_PROC_INFO,
		procCallNumPIDInfo,
		uintptr(pid),
		procPIDPathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		procPIDPathInfoSize,
	)
	if errno != 0 {
		return "", fmt.Errorf("proc_info: %v", errno)
	}

	path := string(buf)
	if idx := strings.IndexByte(path, 0); idx >= 0 {
		path = path[:idx]
	}
	return path, nil
}
```

- [ ] **Step 3: Create Windows implementation**

Create `daemon/internal/tun/procinfo_windows.go`:

```go
//go:build windows

package tun

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessInfo struct{}

func newProcessInfo() ProcessInfo {
	return &windowsProcessInfo{}
}

var (
	iphlpapi            = windows.NewLazySystemDLL("iphlpapi.dll")
	getExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	getExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

func (w *windowsProcessInfo) FindProcess(network string, localPort uint16) (string, error) {
	switch network {
	case "tcp":
		return w.findTCP(localPort)
	case "udp":
		return w.findUDP(localPort)
	default:
		return "", fmt.Errorf("unsupported network: %s", network)
	}
}

func (w *windowsProcessInfo) findTCP(localPort uint16) (string, error) {
	var size uint32
	// First call to get required size
	getExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 5, 0)

	buf := make([]byte, size)
	ret, _, _ := getExtendedTcpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		windows.AF_INET,
		5, // TCP_TABLE_OWNER_PID_ALL
		0,
	)
	if ret != 0 {
		return "", fmt.Errorf("GetExtendedTcpTable: %d", ret)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 24 // MIB_TCPROW_OWNER_PID
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		entry := buf[offset : offset+entrySize]
		// dwLocalPort at offset 8 (network byte order in low 16 bits)
		port := binary.BigEndian.Uint16(entry[8:10])
		if port == localPort {
			pid := binary.LittleEndian.Uint32(entry[20:24])
			return getProcessPath(pid)
		}
	}
	return "", nil
}

func (w *windowsProcessInfo) findUDP(localPort uint16) (string, error) {
	var size uint32
	getExtendedUdpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, windows.AF_INET, 1, 0)

	buf := make([]byte, size)
	ret, _, _ := getExtendedUdpTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0,
		windows.AF_INET,
		1, // UDP_TABLE_OWNER_PID
		0,
	)
	if ret != 0 {
		return "", fmt.Errorf("GetExtendedUdpTable: %d", ret)
	}

	numEntries := binary.LittleEndian.Uint32(buf[0:4])
	const entrySize = 12 // MIB_UDPROW_OWNER_PID
	for i := uint32(0); i < numEntries; i++ {
		offset := 4 + i*entrySize
		entry := buf[offset : offset+entrySize]
		port := binary.BigEndian.Uint16(entry[4:6])
		if port == localPort {
			pid := binary.LittleEndian.Uint32(entry[8:12])
			return getProcessPath(pid)
		}
	}
	return "", nil
}

func getProcessPath(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, syscall.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}
```

- [ ] **Step 4: Build check**

Run:
```bash
cd daemon && go build ./internal/tun/
cd daemon && GOOS=windows GOARCH=amd64 go build ./internal/tun/
```
Expected: Both compile.

- [ ] **Step 5: Commit**

```bash
git add daemon/internal/tun/procinfo.go daemon/internal/tun/procinfo_darwin.go daemon/internal/tun/procinfo_windows.go
git commit -m "feat(tun): PID lookup from source port (macOS sysctl, Windows iphlpapi)"
```

---

### Task 9: TUN engine — main TCP/UDP intercept loop

**Files:**
- Create: `daemon/internal/tun/engine.go`

- [ ] **Step 1: Implement engine**

Create `daemon/internal/tun/engine.go`:

```go
package tun

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"

	"proxyness/pkg/proto"
)

type Status string

const (
	StatusInactive Status = "inactive"
	StatusActive   Status = "active"
)

type Engine struct {
	mu         sync.Mutex
	status     Status
	serverAddr string
	key        string
	rules      *Rules
	procInfo   ProcessInfo
	stack      *stack.Stack
	helperAddr string // unix socket or tcp addr to helper
}

func NewEngine() *Engine {
	return &Engine{
		status: StatusInactive,
		rules:  NewRules(),
	}
}

func (e *Engine) GetStatus() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) GetRules() *Rules {
	return e.rules
}

type StartRequest struct {
	ServerAddr string `json:"server"`
	Key        string `json:"key"`
	HelperAddr string `json:"helper_addr"`
}

func (e *Engine) Start(req StartRequest) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status == StatusActive {
		return fmt.Errorf("TUN already active")
	}

	// Request helper to create TUN device
	if err := e.requestHelper(req.HelperAddr, "create"); err != nil {
		return fmt.Errorf("helper create: %w", err)
	}

	// Create gVisor stack
	s, _, err := newStack(1500)
	if err != nil {
		e.requestHelper(req.HelperAddr, "destroy")
		return fmt.Errorf("create stack: %w", err)
	}

	e.stack = s
	e.serverAddr = req.ServerAddr
	e.key = req.Key
	e.helperAddr = req.HelperAddr
	e.procInfo = newProcessInfo()

	// Set up TCP forwarder
	tcpFwd := tcp.NewForwarder(s, 0, 2048, func(r *tcp.ForwarderRequest) {
		e.handleTCP(r)
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// Set up UDP forwarder
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) bool {
		return e.handleUDP(r)
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	e.status = StatusActive
	log.Printf("[tun] engine started, server=%s", req.ServerAddr)
	return nil
}

func (e *Engine) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.status == StatusInactive {
		return nil
	}

	if e.stack != nil {
		e.stack.Close()
		e.stack = nil
	}

	e.requestHelper(e.helperAddr, "destroy")
	e.status = StatusInactive
	log.Printf("[tun] engine stopped")
	return nil
}

func (e *Engine) handleTCP(r *tcp.ForwarderRequest) {
	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	// Look up process
	appPath, _ := e.procInfo.FindProcess("tcp", srcPort)

	shouldProxy := e.rules.ShouldProxy(appPath)
	if appPath != "" {
		log.Printf("[tun] TCP %s:%d from %s (proxy=%v)", dstAddr, dstPort, appPath, shouldProxy)
	}

	var wq waiter.Queue
	ep, tcpErr := r.CreateEndpoint(&wq)
	if tcpErr != nil {
		r.Complete(true)
		return
	}
	defer r.Complete(false)

	conn := gonet.NewTCPConn(&wq, ep)
	defer conn.Close()

	if shouldProxy {
		e.proxyTCP(conn, dstAddr, dstPort)
	} else {
		e.bypassTCP(conn, dstAddr, dstPort)
	}
}

func (e *Engine) proxyTCP(local net.Conn, dstAddr string, dstPort uint16) {
	tlsConn, err := tls.Dial("tcp", e.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[tun] tls dial failed: %v", err)
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeTCP); err != nil {
		return
	}
	if err := proto.WriteConnect(tlsConn, dstAddr, dstPort); err != nil {
		return
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	proto.Relay(local, tlsConn)
}

func (e *Engine) bypassTCP(local net.Conn, dstAddr string, dstPort uint16) {
	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", dstAddr, dstPort), 10*time.Second)
	if err != nil {
		return
	}
	defer target.Close()
	proto.Relay(local, target)
}

func (e *Engine) handleUDP(r *udp.ForwarderRequest) bool {
	id := r.ID()
	dstAddr := id.LocalAddress.String()
	dstPort := id.LocalPort
	srcPort := id.RemotePort

	appPath, _ := e.procInfo.FindProcess("udp", srcPort)
	shouldProxy := e.rules.ShouldProxy(appPath)

	var wq waiter.Queue
	ep, udpErr := r.CreateEndpoint(&wq)
	if udpErr != nil {
		return false
	}

	conn := gonet.NewUDPConn(&wq, ep)

	if shouldProxy {
		go e.proxyUDP(conn, dstAddr, dstPort)
	} else {
		go e.bypassUDP(conn, dstAddr, dstPort)
	}
	return true
}

func (e *Engine) proxyUDP(local net.Conn, dstAddr string, dstPort uint16) {
	defer local.Close()

	tlsConn, err := tls.Dial("tcp", e.serverAddr, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer tlsConn.Close()

	if err := proto.WriteAuth(tlsConn, e.key); err != nil {
		return
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		return
	}

	if err := proto.WriteMsgType(tlsConn, proto.MsgTypeUDP); err != nil {
		return
	}
	if err := proto.WriteConnect(tlsConn, dstAddr, dstPort); err != nil {
		return
	}

	// Relay: local UDP <-> TLS framed UDP
	done := make(chan struct{}, 2)

	// local -> TLS
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			if err := proto.WriteUDPFrame(tlsConn, dstAddr, dstPort, buf[:n]); err != nil {
				return
			}
		}
	}()

	// TLS -> local
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			_, _, payload, err := proto.ReadUDPFrame(tlsConn)
			if err != nil {
				return
			}
			if _, err := local.Write(payload); err != nil {
				return
			}
		}
	}()

	<-done
}

func (e *Engine) bypassUDP(local net.Conn, dstAddr string, dstPort uint16) {
	defer local.Close()

	raddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", dstAddr, dstPort))
	if err != nil {
		return
	}
	remote, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			local.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := local.Read(buf)
			if err != nil {
				return
			}
			remote.Write(buf[:n])
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 65535)
		for {
			remote.SetReadDeadline(time.Now().Add(60 * time.Second))
			n, err := remote.Read(buf)
			if err != nil {
				return
			}
			local.Write(buf[:n])
		}
	}()
	<-done
}

func (e *Engine) requestHelper(addr, action string) error {
	conn, err := dialHelper(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	req := map[string]string{"action": action}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("helper: %s", resp.Error)
	}
	return nil
}

func dialHelper(addr string) (net.Conn, error) {
	// Try unix socket first (macOS), then TCP (Windows)
	if conn, err := net.DialTimeout("unix", addr, 2*time.Second); err == nil {
		return conn, nil
	}
	return net.DialTimeout("tcp", addr, 2*time.Second)
}

// Ensure io import is used
var _ = io.EOF
```

- [ ] **Step 2: Verify build**

Run: `cd daemon && go build ./internal/tun/`
Expected: Compiles.

- [ ] **Step 3: Commit**

```bash
git add daemon/internal/tun/engine.go
git commit -m "feat(tun): TUN engine with TCP/UDP intercept, proxy/bypass routing"
```

---

### Task 10: Daemon API — TUN endpoints

**Files:**
- Modify: `daemon/internal/api/api.go`
- Modify: `daemon/cmd/main.go`

- [ ] **Step 1: Add TUN endpoints to API**

Modify `daemon/internal/api/api.go` — add TUN engine to Server struct and new handlers:

Add import: `"proxyness/daemon/internal/tun"`

Update `Server` struct:

```go
type Server struct {
	tunnel     *tunnel.Tunnel
	tunEngine  *tun.Engine
	listenAddr string
}
```

Update `New`:

```go
func New(t *tunnel.Tunnel, te *tun.Engine, listenAddr string) *Server {
	return &Server{tunnel: t, tunEngine: te, listenAddr: listenAddr}
}
```

Add routes to `Handler()`:

```go
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /connect", s.handleConnect)
	mux.HandleFunc("POST /disconnect", s.handleDisconnect)
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /proxy.pac", s.handlePAC)
	// TUN endpoints
	mux.HandleFunc("POST /tun/start", s.handleTUNStart)
	mux.HandleFunc("POST /tun/stop", s.handleTUNStop)
	mux.HandleFunc("GET /tun/status", s.handleTUNStatus)
	mux.HandleFunc("POST /tun/rules", s.handleTUNRulesUpdate)
	mux.HandleFunc("GET /tun/rules", s.handleTUNRulesGet)
	return mux
}
```

Add TUN handlers:

```go
func (s *Server) handleTUNStart(w http.ResponseWriter, r *http.Request) {
	var req tun.StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.tunEngine.Start(req); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNStop(w http.ResponseWriter, r *http.Request) {
	if err := s.tunEngine.Stop(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": string(s.tunEngine.GetStatus()),
	})
}

func (s *Server) handleTUNRulesUpdate(w http.ResponseWriter, r *http.Request) {
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.tunEngine.GetRules().FromJSON(body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTUNRulesGet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(s.tunEngine.GetRules().ToJSON())
}
```

- [ ] **Step 2: Update daemon main to wire TUN engine**

Modify `daemon/cmd/main.go`:

```go
package main

import (
	"flag"
	"log"
	"net/http"

	"proxyness/daemon/internal/api"
	"proxyness/daemon/internal/tun"
	"proxyness/daemon/internal/tunnel"
)

func main() {
	serverAddr := flag.String("server", "", "proxy server address (host:port)")
	key := flag.String("key", "", "shared secret key (hex)")
	listenAddr := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	apiAddr := flag.String("api", "127.0.0.1:9090", "HTTP API listen address")
	flag.Parse()

	tun := tunnel.New()
	tunEngine := tun.NewEngine()

	if *serverAddr != "" && *key != "" {
		if err := tun.Start(*listenAddr, *serverAddr, *key); err != nil {
			log.Fatalf("start tunnel: %v", err)
		}
		log.Printf("tunnel connected to %s, SOCKS5 on %s", *serverAddr, *listenAddr)
	}

	srv := api.New(tun, tunEngine, *listenAddr)
	log.Printf("API listening on %s", *apiAddr)
	if err := http.ListenAndServe(*apiAddr, srv.Handler()); err != nil {
		log.Fatalf("api: %v", err)
	}
}
```

Note: there is a naming conflict between `tunnel` and `tun` packages. The variable `tun` shadows the import. Fix by renaming the tunnel variable:

```go
	tnl := tunnel.New()
	tunEngine := tun.NewEngine()

	if *serverAddr != "" && *key != "" {
		if err := tnl.Start(*listenAddr, *serverAddr, *key); err != nil {
			log.Fatalf("start tunnel: %v", err)
		}
		log.Printf("tunnel connected to %s, SOCKS5 on %s", *serverAddr, *listenAddr)
	}

	srv := api.New(tnl, tunEngine, *listenAddr)
```

- [ ] **Step 3: Verify build**

Run: `cd daemon && go build ./...`
Expected: Compiles.

- [ ] **Step 4: Commit**

```bash
git add daemon/internal/api/api.go daemon/cmd/main.go
git commit -m "feat(daemon): add TUN API endpoints and wire TUN engine"
```

---

## Phase 4: Client UI

### Task 11: Electron — TUN IPC bridge

**Files:**
- Modify: `client/src/main/index.ts`
- Modify: `client/src/main/preload.ts`

- [ ] **Step 1: Add TUN IPC handlers to main process**

In `client/src/main/index.ts`, inside `setupAutoUpdater()` (or after it), add:

```typescript
  ipcMain.on("tun-start", (_e, server: string, key: string) => {
    fetch("http://127.0.0.1:9090/tun/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        server,
        key,
        helper_addr: process.platform === "darwin"
          ? "/var/run/proxyness-helper.sock"
          : "127.0.0.1:9091",
      }),
    }).catch(() => {});
  });

  ipcMain.on("tun-stop", () => {
    fetch("http://127.0.0.1:9090/tun/stop", { method: "POST" }).catch(() => {});
  });

  ipcMain.handle("tun-status", async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/tun/status");
      return await res.json();
    } catch {
      return { status: "inactive" };
    }
  });

  ipcMain.handle("tun-rules-get", async () => {
    try {
      const res = await fetch("http://127.0.0.1:9090/tun/rules");
      return await res.json();
    } catch {
      return { mode: "proxy_all_except", apps: [] };
    }
  });

  ipcMain.on("tun-rules-set", (_e, rules: any) => {
    fetch("http://127.0.0.1:9090/tun/rules", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(rules),
    }).catch(() => {});
  });
```

- [ ] **Step 2: Expose TUN bridge in preload**

In `client/src/main/preload.ts`, add:

```typescript
contextBridge.exposeInMainWorld("tunProxy", {
  start: (server: string, key: string) => ipcRenderer.send("tun-start", server, key),
  stop: () => ipcRenderer.send("tun-stop"),
  getStatus: () => ipcRenderer.invoke("tun-status"),
  getRules: () => ipcRenderer.invoke("tun-rules-get"),
  setRules: (rules: any) => ipcRenderer.send("tun-rules-set", rules),
});
```

- [ ] **Step 3: Commit**

```bash
git add client/src/main/index.ts client/src/main/preload.ts
git commit -m "feat(client): add TUN IPC bridge (start/stop/status/rules)"
```

---

### Task 12: Mode selector and App rules components

**Files:**
- Create: `client/src/renderer/components/ModeSelector.tsx`
- Create: `client/src/renderer/components/AppRules.tsx`

- [ ] **Step 1: Create ModeSelector component**

Create `client/src/renderer/components/ModeSelector.tsx`:

```tsx
import { useState } from "react";

export type ProxyMode = "tun" | "socks5";

interface Props {
  mode: ProxyMode;
  onChange: (mode: ProxyMode) => void;
  disabled?: boolean;
}

export function ModeSelector({ mode, onChange, disabled }: Props) {
  return (
    <div style={{ display: "flex", gap: 8, marginBottom: 16 }}>
      <button
        onClick={() => onChange("tun")}
        disabled={disabled}
        style={{
          flex: 1,
          padding: "8px 0",
          background: mode === "tun" ? "#1a3a5c" : "transparent",
          border: `1px solid ${mode === "tun" ? "#3b82f6" : "#333"}`,
          borderRadius: 8,
          color: mode === "tun" ? "#fff" : "#888",
          fontSize: 13,
          cursor: disabled ? "default" : "pointer",
          opacity: disabled ? 0.5 : 1,
        }}
      >
        Full (TUN)
      </button>
      <button
        onClick={() => onChange("socks5")}
        disabled={disabled}
        style={{
          flex: 1,
          padding: "8px 0",
          background: mode === "socks5" ? "#1a3a5c" : "transparent",
          border: `1px solid ${mode === "socks5" ? "#3b82f6" : "#333"}`,
          borderRadius: 8,
          color: mode === "socks5" ? "#fff" : "#888",
          fontSize: 13,
          cursor: disabled ? "default" : "pointer",
          opacity: disabled ? 0.5 : 1,
        }}
      >
        Browser only (SOCKS5)
      </button>
    </div>
  );
}
```

- [ ] **Step 2: Create AppRules component**

Create `client/src/renderer/components/AppRules.tsx`:

```tsx
import { useState, useEffect } from "react";

declare global {
  interface Window {
    tunProxy?: {
      start: (server: string, key: string) => void;
      stop: () => void;
      getStatus: () => Promise<{ status: string }>;
      getRules: () => Promise<{ mode: string; apps: string[] }>;
      setRules: (rules: { mode: string; apps: string[] }) => void;
    };
  }
}

type RuleMode = "proxy_all_except" | "proxy_only";

interface Props {
  visible: boolean;
}

export function AppRules({ visible }: Props) {
  const [mode, setMode] = useState<RuleMode>("proxy_all_except");
  const [apps, setApps] = useState<string[]>([]);
  const [newApp, setNewApp] = useState("");

  useEffect(() => {
    if (!visible) return;
    window.tunProxy?.getRules().then((rules) => {
      setMode((rules.mode as RuleMode) || "proxy_all_except");
      setApps(rules.apps || []);
    });
  }, [visible]);

  const save = (m: RuleMode, a: string[]) => {
    window.tunProxy?.setRules({ mode: m, apps: a });
  };

  const handleModeChange = (m: RuleMode) => {
    setMode(m);
    save(m, apps);
  };

  const addApp = () => {
    const trimmed = newApp.trim();
    if (!trimmed || apps.includes(trimmed)) return;
    const updated = [...apps, trimmed];
    setApps(updated);
    setNewApp("");
    save(mode, updated);
  };

  const removeApp = (app: string) => {
    const updated = apps.filter((a) => a !== app);
    setApps(updated);
    save(mode, updated);
  };

  if (!visible) return null;

  return (
    <div style={{ marginTop: 16, padding: 12, background: "#111827", borderRadius: 8, border: "1px solid #333" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 8 }}>Split Tunneling</div>

      <div style={{ display: "flex", gap: 8, marginBottom: 12 }}>
        <button
          onClick={() => handleModeChange("proxy_all_except")}
          style={{
            flex: 1, padding: "6px 0", fontSize: 11,
            background: mode === "proxy_all_except" ? "#1a3a5c" : "transparent",
            border: `1px solid ${mode === "proxy_all_except" ? "#3b82f6" : "#333"}`,
            borderRadius: 6, color: mode === "proxy_all_except" ? "#fff" : "#888",
            cursor: "pointer",
          }}
        >
          Proxy all except...
        </button>
        <button
          onClick={() => handleModeChange("proxy_only")}
          style={{
            flex: 1, padding: "6px 0", fontSize: 11,
            background: mode === "proxy_only" ? "#1a3a5c" : "transparent",
            border: `1px solid ${mode === "proxy_only" ? "#3b82f6" : "#333"}`,
            borderRadius: 6, color: mode === "proxy_only" ? "#fff" : "#888",
            cursor: "pointer",
          }}
        >
          Proxy only...
        </button>
      </div>

      {apps.map((app) => (
        <div key={app} style={{ display: "flex", justifyContent: "space-between", alignItems: "center", padding: "4px 0", fontSize: 12, color: "#ccc" }}>
          <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 260 }}>{app.split("/").pop()}</span>
          <button
            onClick={() => removeApp(app)}
            style={{ background: "transparent", border: "none", color: "#666", cursor: "pointer", fontSize: 14 }}
          >
            x
          </button>
        </div>
      ))}

      <div style={{ display: "flex", gap: 6, marginTop: 8 }}>
        <input
          value={newApp}
          onChange={(e) => setNewApp(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && addApp()}
          placeholder="App path..."
          style={{
            flex: 1, padding: "6px 8px", background: "#16213e",
            border: "1px solid #333", borderRadius: 6, color: "#eee", fontSize: 12,
          }}
        />
        <button
          onClick={addApp}
          style={{
            padding: "6px 12px", background: "#3b82f6", color: "#fff",
            border: "none", borderRadius: 6, fontSize: 12, cursor: "pointer",
          }}
        >
          Add
        </button>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/components/ModeSelector.tsx client/src/renderer/components/AppRules.tsx
git commit -m "feat(client): add ModeSelector and AppRules components"
```

---

### Task 13: Integrate TUN mode into App.tsx

**Files:**
- Modify: `client/src/renderer/App.tsx`

- [ ] **Step 1: Update App.tsx with mode selector and TUN logic**

Add imports at top:

```tsx
import { ModeSelector, ProxyMode } from "./components/ModeSelector";
import { AppRules } from "./components/AppRules";
```

Add state for mode:

```tsx
const [proxyMode, setProxyMode] = useState<ProxyMode>(
  () => (localStorage.getItem("proxyness-mode") as ProxyMode) || "tun"
);
```

Add mode persistence handler:

```tsx
const handleModeChange = (m: ProxyMode) => {
  setProxyMode(m);
  localStorage.setItem("proxyness-mode", m);
};
```

Update connect logic — replace direct `connect(SERVER, key)` calls. The `connectWithKey` function becomes:

```tsx
const connectWithKey = (k: string) => {
  const trimmed = k.trim();
  if (!trimmed) return;
  localStorage.setItem(STORAGE_KEY, trimmed);
  setKey(trimmed);
  setShowSetup(false);
  if (proxyMode === "tun") {
    (window as any).tunProxy?.start(SERVER, trimmed);
  } else {
    connect(SERVER, trimmed);
  }
};
```

Update auto-connect:

```tsx
useEffect(() => {
  if (!autoConnected.current && key && !isConnected && !loading) {
    autoConnected.current = true;
    if (proxyMode === "tun") {
      (window as any).tunProxy?.start(SERVER, key);
    } else {
      connect(SERVER, key);
    }
  }
}, [key, isConnected, loading, connect, proxyMode]);
```

Update disconnect in `handleReset`:

```tsx
const handleReset = () => {
  if (proxyMode === "tun") {
    (window as any).tunProxy?.stop();
  } else {
    disconnect();
  }
  localStorage.removeItem(STORAGE_KEY);
  setKey("");
  setShowSetup(true);
  autoConnected.current = false;
};
```

In the JSX, add `ModeSelector` before `ConnectionButton` and `AppRules` after:

```tsx
{!showSetup && (
  <>
    <ModeSelector mode={proxyMode} onChange={handleModeChange} disabled={isConnected} />
    <ConnectionButton
      connected={isConnected}
      loading={loading}
      onConnect={() => {
        if (proxyMode === "tun") {
          (window as any).tunProxy?.start(SERVER, key);
        } else {
          connect(SERVER, key);
        }
      }}
      onDisconnect={() => {
        if (proxyMode === "tun") {
          (window as any).tunProxy?.stop();
        } else {
          disconnect();
        }
      }}
    />
    <AppRules visible={proxyMode === "tun"} />
    <button onClick={handleReset} /* ... existing styles ... */>
      Change key
    </button>
  </>
)}
```

- [ ] **Step 2: Verify TypeScript build**

Run: `cd client && npm run build`
Expected: Compiles.

- [ ] **Step 3: Commit**

```bash
git add client/src/renderer/App.tsx
git commit -m "feat(client): integrate TUN mode with mode selector and split tunneling UI"
```

---

## Phase 5: Build & Distribution

### Task 14: Makefile + electron-builder + CI

**Files:**
- Modify: `Makefile`
- Modify: `client/electron-builder.json`

- [ ] **Step 1: Add build-helper target to Makefile**

Add to `Makefile`:

```makefile
# Privileged helper (all platforms, output to client/resources)
build-helper:
	mkdir -p client/resources
	cd helper && GOOS=darwin GOARCH=arm64 go build -o ../client/resources/helper-darwin-arm64 ./cmd
	cd helper && GOOS=darwin GOARCH=amd64 go build -o ../client/resources/helper-darwin-amd64 ./cmd
	cd helper && GOOS=windows GOARCH=amd64 go build -o ../client/resources/helper-windows.exe ./cmd
```

Update `build-client` dependency:

```makefile
build-client: build-daemon build-helper
	cd client && npm run build && npx electron-builder
```

Update `clean`:

```makefile
clean:
	rm -rf dist/ client/dist/ client/dist-electron/ client/release/ client/resources/daemon-* client/resources/helper-*
```

- [ ] **Step 2: Update electron-builder.json**

Add helper to extraResources:

```json
"extraResources": [
  {
    "from": "resources/",
    "to": "resources/",
    "filter": ["daemon-*", "helper-*"]
  }
]
```

- [ ] **Step 3: Verify full build**

Run: `make build-helper`
Expected: Helper binaries appear in `client/resources/`.

- [ ] **Step 4: Commit**

```bash
git add Makefile client/electron-builder.json
git commit -m "feat(build): add helper build target, bundle helper in electron app"
```

---

### Task 15: Update daemon.ts to launch helper

**Files:**
- Modify: `client/src/main/daemon.ts`

- [ ] **Step 1: Add helper launch logic**

Add helper path resolution and start/stop functions to `daemon.ts`:

```typescript
let helperProcess: ChildProcess | null = null;

function getHelperPath(): string {
  const resourcesPath = app.isPackaged
    ? path.join(process.resourcesPath, "resources")
    : path.join(__dirname, "../../resources");

  const platform = process.platform;
  const arch = process.arch;

  if (platform === "win32") {
    return path.join(resourcesPath, "helper-windows.exe");
  }
  return path.join(resourcesPath, `helper-${platform}-${arch}`);
}

export function startHelper(): void {
  if (helperProcess) return;

  const helperPath = getHelperPath();
  helperProcess = spawn(helperPath, [], {
    stdio: "pipe",
  });

  helperProcess.stdout?.on("data", (data: Buffer) => {
    console.log(`[helper] ${data.toString().trim()}`);
  });

  helperProcess.stderr?.on("data", (data: Buffer) => {
    console.error(`[helper] ${data.toString().trim()}`);
  });

  helperProcess.on("exit", (code) => {
    console.log(`[helper] exited with code ${code}`);
    helperProcess = null;
  });
}

export function stopHelper(): void {
  if (helperProcess) {
    helperProcess.kill();
    helperProcess = null;
  }
}
```

- [ ] **Step 2: Update index.ts to start/stop helper**

In `client/src/main/index.ts`, import and call:

```typescript
import { startDaemon, stopDaemon, startHelper, stopHelper } from "./daemon";
```

In `app.whenReady()`:

```typescript
app.whenReady().then(() => {
  startDaemon();
  startHelper();
  createWindow();
  createTray();
  setupAutoUpdater();
});
```

In `app.on("before-quit")`:

```typescript
app.on("before-quit", () => {
  disableSystemProxy();
  stopDaemon();
  stopHelper();
});
```

- [ ] **Step 3: Commit**

```bash
git add client/src/main/daemon.ts client/src/main/index.ts
git commit -m "feat(client): launch helper binary alongside daemon"
```

---

### Task 16: Final integration test

- [ ] **Step 1: Run all Go tests**

Run: `make test`
Expected: All pass.

- [ ] **Step 2: Build everything**

Run: `make build-server && make build-daemon && make build-helper`
Expected: All binaries produced.

- [ ] **Step 3: Build client**

Run: `cd client && npm run build`
Expected: TypeScript compiles.

- [ ] **Step 4: Commit any remaining changes**

```bash
git add -A
git commit -m "chore: final integration fixes"
```

- [ ] **Step 5: Push**

```bash
git push origin main
```
