package transport

import (
	"errors"
	"sync"
	"testing"
)

// autoFake is a minimal Transport used to drive AutoTransport state without
// touching the network.
type autoFake struct {
	mu     sync.Mutex
	mode   string
	closed int
}

func (f *autoFake) Connect(server, key string, machineID [16]byte) error { return nil }
func (f *autoFake) OpenStream(streamType byte, addr string, port uint16) (Stream, error) {
	return nil, errors.New("autoFake: no streams")
}
func (f *autoFake) Mode() string { return f.mode }
func (f *autoFake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}
func (f *autoFake) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// TryUpgradeToUDP must be a no-op unless we're actually sitting on the TLS
// fallback — retrying UDP while already on UDP would tear down a healthy
// session for nothing.
func TestAutoTransportUpgradeSkippedWhenNotOnTLS(t *testing.T) {
	for _, mode := range []string{ModeUDP, ModeAuto} {
		t.Run(mode, func(t *testing.T) {
			a := NewAutoTransport()
			var fake *autoFake
			if mode != ModeAuto {
				fake = &autoFake{mode: mode}
				a.active = fake
			}

			err := a.TryUpgradeToUDP("127.0.0.1:1", "key", [16]byte{})
			if !errors.Is(err, errNotOnTLS) {
				t.Fatalf("TryUpgradeToUDP() error = %v, want errNotOnTLS", err)
			}
			if fake != nil && fake.closeCount() != 0 {
				t.Fatalf("active transport was closed (%d times), want untouched", fake.closeCount())
			}
		})
	}
}

// A failed upgrade must leave the working TLS transport in place — the whole
// point is that a probe failure costs nothing.
func TestAutoTransportUpgradeKeepsTLSOnFailure(t *testing.T) {
	a := NewAutoTransport()
	fake := &autoFake{mode: ModeTLS}
	a.active = fake

	// 127.0.0.1:1 has nothing listening, so the UDP handshake can't complete.
	if err := a.TryUpgradeToUDP("127.0.0.1:1", "key", [16]byte{}); err == nil {
		t.Fatal("TryUpgradeToUDP() = nil, want error against a dead server")
	}
	if a.Mode() != ModeTLS {
		t.Fatalf("Mode() = %q, want %q — TLS must survive a failed upgrade", a.Mode(), ModeTLS)
	}
	if fake.closeCount() != 0 {
		t.Fatalf("TLS transport closed %d times, want 0", fake.closeCount())
	}
}

// active is read by every proxy goroutine and written by the health loop —
// run under -race to catch unsynchronised access.
func TestAutoTransportConcurrentAccess(t *testing.T) {
	a := NewAutoTransport()
	a.active = &autoFake{mode: ModeTLS}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = a.Mode()
				_, _ = a.OpenStream(0x01, "example.com", 443)
				_ = a.Alive()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			a.swapActive(&autoFake{mode: ModeUDP})
		}
	}()
	wg.Wait()
}

// Close on a transport whose Connect never dialled must not panic —
// TryUpgradeToUDP closes the candidate on every failure path.
func TestUDPTransportCloseBeforeConnect(t *testing.T) {
	if err := NewUDPTransport().Close(); err != nil {
		t.Fatalf("Close() on unconnected transport = %v, want nil", err)
	}
}
