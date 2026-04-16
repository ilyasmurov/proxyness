package transport

import (
	"fmt"
	"log"
	"time"

	pkgudp "proxyness/pkg/udp"
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
	// Try UDP first (UDPTransport.Connect replaces the port internally)
	udp := NewUDPTransport()
	done := make(chan error, 1)
	go func() {
		done <- udp.Connect(server, key, machineID)
	}()

	select {
	case err := <-done:
		if err == nil {
			// Handshake succeeded — probe actual data flow by opening a
			// test TCP stream to a known fast host. TSPU/ISP may allow the
			// small handshake packets through while blocking bulk UDP data
			// (observed 2026-04-16: YouTube unreachable on UDP despite
			// successful handshake + stream open/close in server logs).
			if probeErr := a.probeUDP(udp); probeErr != nil {
				log.Printf("[transport] UDP probe failed: %v, falling back to TLS", probeErr)
				udp.Close()
			} else {
				a.active = udp
				log.Printf("[transport] connected via UDP (probe OK)")
				return nil
			}
		} else {
			log.Printf("[transport] UDP failed: %v, falling back to TLS", err)
		}
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

// DoneChan exposes the underlying transport's done channel so the engine's
// health loop can react to session death (e.g., UDP dead-session detection).
// Without this, transportDone() in engine.go falls through the interface
// assertion and blocks forever on a nil channel, leaving the engine unaware
// that its transport has died (common after macOS sleep/wake).
func (a *AutoTransport) DoneChan() <-chan struct{} {
	if a.active == nil {
		return nil
	}
	if d, ok := a.active.(interface{ DoneChan() <-chan struct{} }); ok {
		return d.DoneChan()
	}
	return nil
}

// probeUDP opens a test TCP stream through the UDP transport to verify that
// data actually flows in both directions. Returns nil on success.
func (a *AutoTransport) probeUDP(udp *UDPTransport) error {
	// Open a TCP stream to Cloudflare — fast, globally available, tiny response.
	stream, err := udp.OpenStream(pkgudp.StreamTypeTCP, "1.1.1.1", 80)
	if err != nil {
		return fmt.Errorf("open probe stream: %w", err)
	}
	defer stream.Close()

	// Send minimal HTTP request and try to read some response bytes.
	_, err = stream.Write([]byte("HEAD / HTTP/1.1\r\nHost: 1.1.1.1\r\nConnection: close\r\n\r\n"))
	if err != nil {
		return fmt.Errorf("write probe: %w", err)
	}

	// Read with timeout — we only need a few bytes to confirm data flows back.
	buf := make([]byte, 32)
	readDone := make(chan error, 1)
	go func() {
		_, rerr := stream.Read(buf)
		readDone <- rerr
	}()

	select {
	case err := <-readDone:
		if err != nil {
			return fmt.Errorf("read probe: %w", err)
		}
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("probe read timeout (no data received in 5s)")
	}
}

// FallbackToTLS closes the current (presumably broken UDP) transport and
// reconnects via TLS. Called by engine/tunnel D3 when streams fail on Auto+UDP.
func (a *AutoTransport) FallbackToTLS(server, key string, machineID [16]byte) error {
	if a.active != nil {
		a.active.Close()
		a.active = nil
	}
	tls := NewTLSTransport()
	if err := tls.Connect(server, key, machineID); err != nil {
		return fmt.Errorf("TLS fallback: %w", err)
	}
	a.active = tls
	log.Printf("[transport] fell back to TLS after UDP data failure")
	return nil
}

// Alive reports whether the underlying transport is still usable.
func (a *AutoTransport) Alive() bool {
	if a.active == nil {
		return false
	}
	if al, ok := a.active.(interface{ Alive() bool }); ok {
		return al.Alive()
	}
	return true
}
