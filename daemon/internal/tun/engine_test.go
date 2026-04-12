package tun

import (
	"net"
	"testing"
	"time"

	dstats "proxyness/daemon/internal/stats"
)

func TestEngineSetReconnectingOnlyFromActive(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	// Initial: StatusInactive — must be no-op
	e.setReconnecting()
	if e.GetStatus() != StatusInactive {
		t.Fatalf("expected StatusInactive, got %s", e.GetStatus())
	}

	// Force Active
	e.mu.Lock()
	e.status = StatusActive
	e.mu.Unlock()

	e.setReconnecting()
	if e.GetStatus() != StatusReconnecting {
		t.Fatalf("expected StatusReconnecting, got %s", e.GetStatus())
	}

	// Idempotent
	e.setReconnecting()
	if e.GetStatus() != StatusReconnecting {
		t.Fatalf("setReconnecting should be idempotent, got %s", e.GetStatus())
	}
}

func TestEngineSetConnectedOnlyFromReconnecting(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())

	// From Inactive: no-op
	e.setConnected()
	if e.GetStatus() != StatusInactive {
		t.Fatalf("setConnected from Inactive must be no-op, got %s", e.GetStatus())
	}

	// From Active: no-op
	e.mu.Lock()
	e.status = StatusActive
	e.mu.Unlock()
	e.setConnected()
	if e.GetStatus() != StatusActive {
		t.Fatalf("setConnected from Active must be no-op, got %s", e.GetStatus())
	}

	// From Reconnecting: → Active
	e.mu.Lock()
	e.status = StatusReconnecting
	e.mu.Unlock()
	e.setConnected()
	if e.GetStatus() != StatusActive {
		t.Fatalf("expected StatusActive after recovery, got %s", e.GetStatus())
	}
}

func TestEngineSetReconnectingClosesAllConns(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	e.mu.Lock()
	e.status = StatusActive
	e.mu.Unlock()

	a, b := net.Pipe()
	defer b.Close()
	e.trackConn(a)

	e.setReconnecting()

	a.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := a.Read(buf); err == nil {
		t.Fatalf("expected tracked conn to be closed")
	}
}
