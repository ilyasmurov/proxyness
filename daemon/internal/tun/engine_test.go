package tun

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	dstats "proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/transport"
)

// wakeFakeTransport is a minimal transport.Transport that records Close calls
// and exposes a DoneChan that fires on the first Close — enough to assert that
// WakeReconnect closed the live transport (which is what triggers the health
// loop's D1 rebuild path).
type wakeFakeTransport struct {
	mu     sync.Mutex
	closed bool
	closeN int
	done   chan struct{}
}

func newWakeFakeTransport() *wakeFakeTransport {
	return &wakeFakeTransport{done: make(chan struct{})}
}

func (f *wakeFakeTransport) Connect(server, key string, machineID [16]byte) error { return nil }
func (f *wakeFakeTransport) OpenStream(streamType byte, addr string, port uint16) (transport.Stream, error) {
	return nil, errors.New("wakeFakeTransport: no streams")
}
func (f *wakeFakeTransport) Mode() string              { return "tls" }
func (f *wakeFakeTransport) DoneChan() <-chan struct{} { return f.done }
func (f *wakeFakeTransport) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeN++
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	return nil
}
func (f *wakeFakeTransport) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeN
}

func TestEngineWakeReconnectClosesTransportWhenActive(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	ft := newWakeFakeTransport()
	e.mu.Lock()
	e.status = StatusActive
	e.transport = ft
	e.mu.Unlock()

	e.WakeReconnect()

	// Closing the transport is what fires DoneChan → healthLoop D1.
	select {
	case <-ft.DoneChan():
	case <-time.After(time.Second):
		t.Fatalf("WakeReconnect should close the active transport (DoneChan never fired)")
	}
}

func TestEngineWakeReconnectNoopWhenInactive(t *testing.T) {
	e := NewEngine(dstats.NewRateMeter())
	ft := newWakeFakeTransport()
	e.mu.Lock()
	e.status = StatusInactive
	e.transport = ft
	e.mu.Unlock()

	e.WakeReconnect()

	if ft.closeCount() != 0 {
		t.Fatalf("WakeReconnect must not touch the transport when inactive, got %d Close calls", ft.closeCount())
	}
}

func TestEngineWakeReconnectNoopWhenReconnecting(t *testing.T) {
	// Already reconnecting → D1/D3 owns recovery; a wake must not re-trigger.
	e := NewEngine(dstats.NewRateMeter())
	ft := newWakeFakeTransport()
	e.mu.Lock()
	e.status = StatusReconnecting
	e.transport = ft
	e.mu.Unlock()

	e.WakeReconnect()

	if ft.closeCount() != 0 {
		t.Fatalf("WakeReconnect must be a no-op while already reconnecting, got %d Close calls", ft.closeCount())
	}
}

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
	e.trackConn(a, "test")

	e.setReconnecting()

	a.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	if _, err := a.Read(buf); err == nil {
		t.Fatalf("expected tracked conn to be closed")
	}
}

func TestEngineMachineIDRejectedForcesRebuildWhenActive(t *testing.T) {
	// "machine id rejected" on a stream open must NOT stop the engine —
	// it closes the transport so healthLoop D1 rebuilds it, keeping the
	// engine in the reconnect ladder instead of dying to a fatal error.
	e := NewEngine(dstats.NewRateMeter())
	ft := newWakeFakeTransport()
	e.mu.Lock()
	e.status = StatusActive
	e.transport = ft
	e.mu.Unlock()

	e.machineIDRejected("TCP", "example.com", 443)

	select {
	case <-ft.DoneChan():
	case <-time.After(time.Second):
		t.Fatalf("machineIDRejected should close the active transport (DoneChan never fired)")
	}
	if e.GetStatus() == StatusInactive {
		t.Fatalf("machineIDRejected must not stop the engine")
	}
}

func TestEngineMachineIDRejectedNoopWhenReconnecting(t *testing.T) {
	// D1/D3 already own recovery — a concurrent stream rejection must not
	// re-close the fresh transport being built.
	e := NewEngine(dstats.NewRateMeter())
	ft := newWakeFakeTransport()
	e.mu.Lock()
	e.status = StatusReconnecting
	e.transport = ft
	e.mu.Unlock()

	e.machineIDRejected("UDP", "example.com", 443)

	if ft.closeCount() != 0 {
		t.Fatalf("machineIDRejected must be a no-op while reconnecting, got %d Close calls", ft.closeCount())
	}
}

// TestNextRefreshAfter covers the mid-budget RefreshRoutes schedule. The
// "system offline" case is the one that matters: on 2026-08-04 a 37s outage
// spent 15 of those seconds asleep between a failed refresh at attempt 7 and
// the next one at attempt 12, while the gateway had already come back.
func TestNextRefreshAfter(t *testing.T) {
	tests := []struct {
		name        string
		consecutive int
		refreshErr  error
		want        int
	}{
		{
			name:        "refresh succeeded — back off the regular gap",
			consecutive: 2,
			refreshErr:  nil,
			want:        2 + fastRetryRefreshEvery,
		},
		{
			name:        "helper says system offline — retry on the next attempt",
			consecutive: 2,
			refreshErr:  errors.New("helper: no default gateway (system offline)"),
			want:        2 + fastRetryOfflineRefreshEvery,
		},
		{
			name:        "still offline later in the budget",
			consecutive: 7,
			refreshErr:  errors.New("helper: no default gateway (system offline)"),
			want:        7 + fastRetryOfflineRefreshEvery,
		},
		{
			name:        "other helper failure — regular gap, don't hammer it",
			consecutive: 2,
			refreshErr:  errors.New("helper: no TUN device"),
			want:        2 + fastRetryRefreshEvery,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nextRefreshAfter(tc.consecutive, tc.refreshErr)
			if got != tc.want {
				t.Errorf("nextRefreshAfter(%d, %v) = %d, want %d", tc.consecutive, tc.refreshErr, got, tc.want)
			}
		})
	}
}
