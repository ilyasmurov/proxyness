package udp

import (
	"crypto/rand"
	"testing"
)

func TestPacketEncodeDecodeData(t *testing.T) {
	sessionKey := make([]byte, 32)
	rand.Read(sessionKey)

	pkt := &Packet{
		ConnID:   0x12345678,
		Type:     MsgStreamData,
		StreamID: 42,
		Seq:      17,
		Data:     []byte("hello"),
	}

	encoded, err := EncodePacket(pkt, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	// First byte should have QUIC flag (0x40 set)
	if encoded[0]&0x40 == 0 {
		t.Fatal("QUIC flag not set")
	}

	decoded, err := DecodePacket(encoded, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.ConnID != pkt.ConnID {
		t.Fatalf("connID: got %d, want %d", decoded.ConnID, pkt.ConnID)
	}
	if decoded.Type != pkt.Type {
		t.Fatalf("type: got %d, want %d", decoded.Type, pkt.Type)
	}
	if decoded.StreamID != pkt.StreamID {
		t.Fatalf("streamID: got %d, want %d", decoded.StreamID, pkt.StreamID)
	}
	if decoded.Seq != pkt.Seq {
		t.Fatalf("seq: got %d, want %d", decoded.Seq, pkt.Seq)
	}
	if string(decoded.Data) != "hello" {
		t.Fatalf("data: got %q", decoded.Data)
	}
}

func TestPacketKeepalive(t *testing.T) {
	sessionKey := make([]byte, 32)
	rand.Read(sessionKey)

	pkt := &Packet{
		ConnID:   0xDEADBEEF,
		Type:     MsgKeepalive,
		StreamID: 0,
	}

	encoded, err := EncodePacket(pkt, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodePacket(encoded, sessionKey)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Type != MsgKeepalive {
		t.Fatalf("type: got %d, want MsgKeepalive", decoded.Type)
	}
}

func TestPacketHandshake(t *testing.T) {
	pkt := &Packet{
		ConnID: 0, // handshake
		Type:   MsgHandshake,
		Data:   []byte("handshake-payload"),
	}

	deviceKey := make([]byte, 32)
	rand.Read(deviceKey)

	encoded, err := EncodePacket(pkt, deviceKey)
	if err != nil {
		t.Fatal(err)
	}

	decoded, err := DecodePacket(encoded, deviceKey)
	if err != nil {
		t.Fatal(err)
	}

	if decoded.Type != MsgHandshake {
		t.Fatalf("type: got %d, want %d", decoded.Type, MsgHandshake)
	}
	if string(decoded.Data) != "handshake-payload" {
		t.Fatalf("data: got %q", decoded.Data)
	}
}

func TestNackEncodeDecode(t *testing.T) {
	seqs := []uint32{5, 7, 12, 100}
	data := EncodeNack(seqs)

	decoded, err := DecodeNack(data)
	if err != nil {
		t.Fatal(err)
	}

	if len(decoded) != len(seqs) {
		t.Fatalf("count: got %d, want %d", len(decoded), len(seqs))
	}
	for i, s := range decoded {
		if s != seqs[i] {
			t.Fatalf("seq[%d]: got %d, want %d", i, s, seqs[i])
		}
	}
}
