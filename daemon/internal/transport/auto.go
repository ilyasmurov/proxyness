package transport

import (
	"fmt"
	"io"
	"log"
	"time"

	pkgudp "proxyness/pkg/udp"
)

const (
	// probeMinBytes: any data beyond the connect result (1 byte) proves the
	// UDP data path works. Even a single HTTP header line is enough.
	probeMinBytes = 16
	probeTimeout  = 5 * time.Second
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

// probeUDP opens a test TCP stream through the UDP transport and downloads
// real data to verify bulk throughput works. TSPU may pass small handshakes
// while throttling/dropping bulk UDP — a HEAD + 32 bytes won't catch that.
// We GET http://1.1.1.1/ (returns ~15 KB of HTML) and require at least
// probeMinBytes (10 KB) within probeTimeout (5s).
func (a *AutoTransport) probeUDP(udp *UDPTransport) error {
	stream, err := udp.OpenStream(pkgudp.StreamTypeTCP, "1.1.1.1", 80)
	if err != nil {
		return fmt.Errorf("open probe stream: %w", err)
	}
	defer stream.Close()

	_, err = stream.Write([]byte("GET / HTTP/1.1\r\nHost: 1.1.1.1\r\nConnection: close\r\n\r\n"))
	if err != nil {
		return fmt.Errorf("write probe: %w", err)
	}

	type probeResult struct {
		n   int
		err error
	}
	ch := make(chan probeResult, 1)
	go func() {
		total := 0
		buf := make([]byte, 4096)
		for total < probeMinBytes {
			n, rerr := stream.Read(buf)
			total += n
			if rerr != nil {
				// EOF after enough bytes is fine (Connection: close)
				if total >= probeMinBytes && rerr == io.EOF {
					ch <- probeResult{total, nil}
					return
				}
				ch <- probeResult{total, rerr}
				return
			}
		}
		ch <- probeResult{total, nil}
	}()

	select {
	case res := <-ch:
		if res.err != nil {
			return fmt.Errorf("probe read: %w (got %d bytes)", res.err, res.n)
		}
		log.Printf("[transport] UDP probe: received %d bytes OK", res.n)
		return nil
	case <-time.After(probeTimeout):
		return fmt.Errorf("probe timeout: could not read %d bytes in %s (UDP bulk data blocked?)", probeMinBytes, probeTimeout)
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
