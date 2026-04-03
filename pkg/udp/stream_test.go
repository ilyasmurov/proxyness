package udp

import (
	"testing"
)

func TestStreamOpenEncodeDecode(t *testing.T) {
	msg := &StreamOpenMsg{
		StreamType: StreamTypeTCP,
		Addr:       "example.com",
		Port:       443,
	}

	data := msg.Encode()

	decoded, err := DecodeStreamOpen(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.StreamType != StreamTypeTCP {
		t.Fatalf("type: got %d", decoded.StreamType)
	}
	if decoded.Addr != "example.com" {
		t.Fatalf("addr: got %q", decoded.Addr)
	}
	if decoded.Port != 443 {
		t.Fatalf("port: got %d", decoded.Port)
	}
}

func TestStreamOpenIPv4(t *testing.T) {
	msg := &StreamOpenMsg{
		StreamType: StreamTypeUDP,
		Addr:       "1.2.3.4",
		Port:       8080,
	}

	data := msg.Encode()
	decoded, err := DecodeStreamOpen(data)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Addr != "1.2.3.4" {
		t.Fatalf("addr: got %q", decoded.Addr)
	}
	if decoded.Port != 8080 {
		t.Fatalf("port: got %d", decoded.Port)
	}
}
