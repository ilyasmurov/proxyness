package proto

import (
	"bytes"
	"testing"
)

func TestUDPFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		addr    string
		port    uint16
		payload []byte
	}{
		{"ipv4", "1.2.3.4", 53, []byte("hello")},
		{"ipv6", "::1", 443, []byte("world")},
		{"domain", "example.com", 8080, []byte{0x01, 0x02, 0x03}},
		{"empty payload", "10.0.0.1", 1234, []byte{}},
		{"large payload", "192.168.1.1", 5000, bytes.Repeat([]byte("x"), 1400)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteUDPFrame(&buf, tt.addr, tt.port, tt.payload); err != nil {
				t.Fatalf("write: %v", err)
			}

			addr, port, payload, err := ReadUDPFrame(&buf)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if addr != tt.addr {
				t.Errorf("addr: got %q, want %q", addr, tt.addr)
			}
			if port != tt.port {
				t.Errorf("port: got %d, want %d", port, tt.port)
			}
			if !bytes.Equal(payload, tt.payload) {
				t.Errorf("payload: got %d bytes, want %d", len(payload), len(tt.payload))
			}
		})
	}
}

func TestMultipleUDPFrames(t *testing.T) {
	var buf bytes.Buffer

	WriteUDPFrame(&buf, "1.1.1.1", 53, []byte("query1"))
	WriteUDPFrame(&buf, "8.8.8.8", 53, []byte("query2"))

	addr1, port1, p1, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if addr1 != "1.1.1.1" || port1 != 53 || string(p1) != "query1" {
		t.Errorf("frame 1 mismatch")
	}

	addr2, port2, p2, err := ReadUDPFrame(&buf)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if addr2 != "8.8.8.8" || port2 != 53 || string(p2) != "query2" {
		t.Errorf("frame 2 mismatch")
	}
}
