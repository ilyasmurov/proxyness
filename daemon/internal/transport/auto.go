package transport

import (
	"fmt"
	"log"
	"time"
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
	// Try UDP first
	udp := NewUDPTransport()
	done := make(chan error, 1)
	go func() {
		done <- udp.Connect(server, key, machineID)
	}()

	select {
	case err := <-done:
		if err == nil {
			a.active = udp
			log.Printf("[transport] connected via UDP")
			return nil
		}
		log.Printf("[transport] UDP failed: %v, falling back to TLS", err)
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
