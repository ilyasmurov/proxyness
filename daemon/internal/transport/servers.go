package transport

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
)

// ServerRing is the ordered list of exit servers one session may dial.
//
// The client hands the daemon every server it knows, preferred first. The
// daemon dials the current one and, when a (re)connect fails for a reason
// that blames the server rather than the local link, moves on to the next.
// A dead exit is therefore routed around by the same reconnect path that
// already rides out Wi-Fi flaps — no user action, no client release.
//
// One address means the ring never moves, which is exactly what a manual
// pick in the client's server switch is meant to do.
//
// "invalid key" is tracked per server: a secondary whose device table lags
// the primary may reject a freshly issued key while the primary accepts it,
// so a rejection only counts as final once every server has said so.
type ServerRing struct {
	mu       sync.Mutex
	addrs    []string
	cur      int
	rejected map[string]bool
}

// NewServerRing builds a ring from addrs in the given order, dropping blanks
// and duplicates. Returns nil when nothing usable is left.
func NewServerRing(addrs ...string) *ServerRing {
	r := &ServerRing{rejected: map[string]bool{}}
	seen := map[string]bool{}
	for _, a := range addrs {
		a = strings.TrimSpace(a)
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		r.addrs = append(r.addrs, a)
	}
	if len(r.addrs) == 0 {
		return nil
	}
	return r
}

// Len is the number of distinct servers in the ring.
func (r *ServerRing) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.addrs)
}

// Current is the server the next dial should go to.
func (r *ServerRing) Current() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.addrs[r.cur]
}

// All lists the servers in dial order, current first.
func (r *ServerRing) All() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.addrs))
	for i := range r.addrs {
		out = append(out, r.addrs[(r.cur+i)%len(r.addrs)])
	}
	return out
}

// Advance moves to the next server and returns it. With a single server it
// stays put and reports moved=false.
func (r *ServerRing) Advance() (next string, moved bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.addrs) < 2 {
		return r.addrs[r.cur], false
	}
	r.cur = (r.cur + 1) % len(r.addrs)
	return r.addrs[r.cur], true
}

// Prefer makes addr the current server. Unknown addresses are ignored.
func (r *ServerRing) Prefer(addr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, a := range r.addrs {
		if a == addr {
			r.cur = i
			return true
		}
	}
	return false
}

// NoteFailure records that a dial to addr failed with err and decides
// whether the next attempt should go elsewhere. It returns the server the
// next attempt will dial and whether that is a different one.
//
//   - ENETUNREACH means the local link is down: every server is equally
//     unreachable, so the ring stays put and the caller's route-refresh
//     logic keeps its consecutive-failure count intact.
//   - "invalid key" marks addr as having rejected the key (see AllRejected)
//     and still moves on — the other server may know the key.
//   - anything else (timeout, refused, handshake failure) moves on.
func (r *ServerRing) NoteFailure(addr string, err error) (next string, moved bool) {
	if err == nil || IsNetworkUnreachable(err) {
		return r.Current(), false
	}
	if IsInvalidKey(err) {
		r.mu.Lock()
		r.rejected[addr] = true
		r.mu.Unlock()
	}
	return r.Advance()
}

// NoteSuccess clears the per-server key rejections: a server that accepted
// the key proves the key is fine, so earlier rejections were just lag.
func (r *ServerRing) NoteSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.rejected {
		delete(r.rejected, k)
	}
}

// AllRejected reports whether every server in the ring has answered
// "invalid key" since the last successful connect. Only then is the key
// really revoked; with fewer, the survivor may simply not have synced yet.
func (r *ServerRing) AllRejected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.addrs {
		if !r.rejected[a] {
			return false
		}
	}
	return true
}

// IsInvalidKey reports whether err is the server's "invalid key" rejection —
// the one failure that means the device was revoked, not that the path is
// bad. Kept as a string match because the error crosses a plain error chain
// from proto.ReadResult.
func IsInvalidKey(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid key")
}

// ConnectAny dials the ring's servers in order, starting at Current, until
// one accepts. On success the ring's current server is the one that
// connected. The returned address is where the transport is connected to.
//
// ENETUNREACH returns at once and unwrapped: the caller has a route-cleanup
// path keyed on it, and no other server helps while the link is down. If
// every server fails, the error is the last non-rejection failure (so the
// client keeps retrying); only when every server rejected the key does the
// bare "invalid key" come back and the client stops.
func ConnectAny(ring *ServerRing, factory func() Transport, key string, machineID [16]byte) (Transport, string, error) {
	if ring == nil {
		return nil, "", errors.New("no server address")
	}
	n := ring.Len()
	var lastErr, lastReject error
	for i := 0; i < n; i++ {
		addr := ring.Current()
		tr := factory()
		err := tr.Connect(addr, key, machineID)
		if err == nil {
			ring.NoteSuccess()
			return tr, addr, nil
		}
		tr.Close()
		if IsNetworkUnreachable(err) {
			return nil, addr, err
		}
		if IsInvalidKey(err) {
			lastReject = err
		} else {
			lastErr = err
		}
		if n > 1 {
			log.Printf("[transport] %s: %v — trying next server", addr, err)
		}
		ring.NoteFailure(addr, err)
	}
	if lastErr != nil {
		if n == 1 {
			return nil, ring.Current(), lastErr
		}
		return nil, "", fmt.Errorf("all %d servers failed: %w", n, lastErr)
	}
	return nil, "", lastReject
}
