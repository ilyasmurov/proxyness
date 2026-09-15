package transport

import (
	"net"
	"strings"
	"testing"
	"time"
)

// A server that accepts the TCP connection and then says nothing is what an
// ISP data-blackhole looks like from the daemon's side (SkyNet → Aeza,
// 2026-09-15). Connect must give up within tlsSetupTimeout so the server ring
// can move on to the next exit instead of hanging on the first.
func TestTLSConnectGivesUpOnASilentServer(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // hold it open, never write
		}
	}()

	old := tlsSetupTimeout
	tlsSetupTimeout = 300 * time.Millisecond
	defer func() { tlsSetupTimeout = old }()

	start := time.Now()
	err = NewTLSTransport().Connect(ln.Addr().String(), strings.Repeat("0", 64), [16]byte{})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error from a silent server")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Connect hung for %v, want ≈%v", elapsed, tlsSetupTimeout)
	}
	if !strings.Contains(err.Error(), "tls handshake") {
		t.Fatalf("unexpected error: %v", err)
	}
}
