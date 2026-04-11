package transport

import (
	"errors"
	"strings"
	"syscall"
)

// IsNetworkUnreachable reports whether err represents a local "no route to
// destination" condition — typically what `net.Dial` returns after the OS
// tears down interfaces (WiFi drop, laptop sleep, VPN interface gone). This
// is distinct from server-side failures (refused, timeout) and from host
// unreachable (EHOSTUNREACH — route exists but host is down).
//
// Used by the health loops in engine.go and tunnel.go to distinguish
// "retry budget exhausted because local network is gone" (recoverable,
// enter slow-poll waiting mode) from "retry budget exhausted because the
// server is unreachable" (stop the engine, surface error to the user).
func IsNetworkUnreachable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	// Fallback for errors that don't expose the syscall errno through the
	// wrapping chain (e.g., errors constructed from strings, or non-OpError
	// wrappers that drop the underlying Unwrap).
	return strings.Contains(err.Error(), "network is unreachable")
}
