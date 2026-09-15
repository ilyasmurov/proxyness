package transport

import (
	"errors"
	"syscall"
	"testing"
)

func TestServerRingDedupsAndKeepsOrder(t *testing.T) {
	r := NewServerRing(" a:443 ", "", "b:443", "a:443")
	if r == nil || r.Len() != 2 {
		t.Fatalf("want 2 servers, got %v", r)
	}
	if got := r.All(); got[0] != "a:443" || got[1] != "b:443" {
		t.Fatalf("order lost: %v", got)
	}
	if NewServerRing("", "  ") != nil {
		t.Fatal("empty input must yield nil ring")
	}
}

func TestServerRingAdvanceWrapsAndSingleStaysPut(t *testing.T) {
	r := NewServerRing("a", "b", "c")
	for i, want := range []string{"b", "c", "a"} {
		next, moved := r.Advance()
		if !moved || next != want {
			t.Fatalf("advance %d: got %q moved=%v, want %q", i, next, moved, want)
		}
	}
	one := NewServerRing("only")
	if next, moved := one.Advance(); moved || next != "only" {
		t.Fatalf("single-server ring moved: %q %v", next, moved)
	}
}

func TestServerRingPrefer(t *testing.T) {
	r := NewServerRing("a", "b")
	if !r.Prefer("b") || r.Current() != "b" {
		t.Fatal("Prefer(b) did not make b current")
	}
	if r.Prefer("zzz") || r.Current() != "b" {
		t.Fatal("Prefer(unknown) must be a no-op")
	}
	if got := r.All(); got[0] != "b" || got[1] != "a" {
		t.Fatalf("All must start at current: %v", got)
	}
}

func TestServerRingNoteFailureRotatesOnServerErrorsOnly(t *testing.T) {
	r := NewServerRing("a", "b")
	// local link down → stay
	if next, moved := r.NoteFailure("a", syscall.ENETUNREACH); moved || next != "a" {
		t.Fatalf("ENETUNREACH must not rotate: %q %v", next, moved)
	}
	// nil error → stay
	if _, moved := r.NoteFailure("a", nil); moved {
		t.Fatal("nil error must not rotate")
	}
	// server-side failure → rotate
	if next, moved := r.NoteFailure("a", errors.New("i/o timeout")); !moved || next != "b" {
		t.Fatalf("timeout must rotate to b: %q %v", next, moved)
	}
}

func TestServerRingInvalidKeyIsFinalOnlyWhenEveryoneRejects(t *testing.T) {
	r := NewServerRing("a", "b")
	rej := errors.New("auth: invalid key")
	if next, moved := r.NoteFailure("a", rej); !moved || next != "b" {
		t.Fatalf("rejection must still move on: %q %v", next, moved)
	}
	if r.AllRejected() {
		t.Fatal("one rejection out of two is not final")
	}
	r.NoteFailure("b", rej)
	if !r.AllRejected() {
		t.Fatal("both rejected → final")
	}
	r.NoteSuccess()
	if r.AllRejected() {
		t.Fatal("a success clears rejections")
	}
	if !NewServerRing("solo").AllRejected() == false {
		// single server, nothing rejected yet
	}
}

// ringFake is a Transport whose Connect answers per server address.
type ringFake struct {
	autoFake
	answers map[string]error
	dialed  []string
}

func (f *ringFake) Connect(server, key string, machineID [16]byte) error {
	f.dialed = append(f.dialed, server)
	return f.answers[server]
}

func TestConnectAnyWalksToTheFirstLiveServer(t *testing.T) {
	ring := NewServerRing("dead:443", "live:443")
	fake := &ringFake{answers: map[string]error{"dead:443": errors.New("dial tcp: i/o timeout")}}
	tr, addr, err := ConnectAny(ring, func() Transport { return fake }, "k", [16]byte{})
	if err != nil || tr == nil || addr != "live:443" {
		t.Fatalf("want live:443, got addr=%q err=%v", addr, err)
	}
	if ring.Current() != "live:443" {
		t.Fatalf("ring must point at the server that connected, got %q", ring.Current())
	}
	if len(fake.dialed) != 2 || fake.dialed[0] != "dead:443" {
		t.Fatalf("dial order: %v", fake.dialed)
	}
}

func TestConnectAnyReturnsRetryableErrorWhenOnlyOneServerRejectsKey(t *testing.T) {
	ring := NewServerRing("stale:443", "dead:443")
	fake := &ringFake{answers: map[string]error{
		"stale:443": errors.New("auth: invalid key"),
		"dead:443":  errors.New("dial tcp: i/o timeout"),
	}}
	_, _, err := ConnectAny(ring, func() Transport { return fake }, "k", [16]byte{})
	if err == nil || IsInvalidKey(err) {
		t.Fatalf("mixed failures must surface the retryable one, got %v", err)
	}
	if ring.AllRejected() {
		t.Fatal("only one server rejected")
	}
}

func TestConnectAnyReportsInvalidKeyWhenEveryServerRejects(t *testing.T) {
	ring := NewServerRing("a:443", "b:443")
	rej := errors.New("auth: invalid key")
	fake := &ringFake{answers: map[string]error{"a:443": rej, "b:443": rej}}
	_, _, err := ConnectAny(ring, func() Transport { return fake }, "k", [16]byte{})
	if !IsInvalidKey(err) || !ring.AllRejected() {
		t.Fatalf("unanimous rejection must be final, got %v", err)
	}
}

func TestConnectAnyStopsAtNetworkUnreachable(t *testing.T) {
	ring := NewServerRing("a:443", "b:443")
	fake := &ringFake{answers: map[string]error{"a:443": syscall.ENETUNREACH, "b:443": nil}}
	_, addr, err := ConnectAny(ring, func() Transport { return fake }, "k", [16]byte{})
	if !IsNetworkUnreachable(err) || addr != "a:443" {
		t.Fatalf("ENETUNREACH must return unwrapped from the first server, got addr=%q err=%v", addr, err)
	}
	if len(fake.dialed) != 1 {
		t.Fatalf("must not try other servers while the link is down: %v", fake.dialed)
	}
}

func TestConnectAnySingleServerKeepsRawError(t *testing.T) {
	ring := NewServerRing("a:443")
	want := errors.New("machine id rejected")
	fake := &ringFake{answers: map[string]error{"a:443": want}}
	_, _, err := ConnectAny(ring, func() Transport { return fake }, "k", [16]byte{})
	if !errors.Is(err, want) {
		t.Fatalf("single server must return the raw error, got %v", err)
	}
}
