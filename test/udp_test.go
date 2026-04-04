package test

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	pkgudp "smurov-proxy/pkg/udp"
)

// TestUDPHandshakeLocal tests the full UDP handshake flow between a mock
// client and a mock server in-process (no network, no DB).
func TestUDPHandshakeLocal(t *testing.T) {
	// Simulate: client sends handshake, server responds, both derive same session key.

	// Client generates ephemeral key
	clientPriv, clientPub, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	// Server generates ephemeral key
	serverPriv, serverPub, err := pkgudp.GenerateEphemeralKey()
	if err != nil {
		t.Fatal(err)
	}

	// Both sides derive session key
	clientKey, err := pkgudp.DeriveSessionKey(clientPriv, serverPub)
	if err != nil {
		t.Fatalf("client derive: %v", err)
	}
	serverKey, err := pkgudp.DeriveSessionKey(serverPriv, clientPub)
	if err != nil {
		t.Fatalf("server derive: %v", err)
	}

	// Keys must match
	if len(clientKey) != 32 || len(serverKey) != 32 {
		t.Fatalf("key lengths: client=%d server=%d", len(clientKey), len(serverKey))
	}
	for i := range clientKey {
		if clientKey[i] != serverKey[i] {
			t.Fatal("session keys do not match")
		}
	}
}

// TestUDPPacketRoundTrip tests encoding and decoding a packet over a real UDP socket.
func TestUDPPacketRoundTrip(t *testing.T) {
	// Create a UDP socket pair
	serverConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer serverConn.Close()

	clientConn, err := net.Dial("udp", serverConn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	// Shared session key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	// Client sends a StreamData packet
	pkt := &pkgudp.Packet{
		ConnID:   0xABCD1234,
		Type:     pkgudp.MsgStreamData,
		PktNum:   1,
		StreamID: 42,
		Data:     []byte("hello from client"),
	}
	encoded, err := pkgudp.EncodePacket(pkt, key)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := clientConn.Write(encoded); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Server reads and decodes
	buf := make([]byte, 2048)
	serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := serverConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Extract ConnID without decryption
	if n < 5 {
		t.Fatal("packet too short")
	}
	connID := binary.BigEndian.Uint32(buf[1:5])
	if connID != 0xABCD1234 {
		t.Fatalf("connID: got 0x%08X", connID)
	}

	decoded, err := pkgudp.DecodePacket(buf[:n], key)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Type != pkgudp.MsgStreamData {
		t.Fatalf("type: got 0x%02x", decoded.Type)
	}
	if decoded.StreamID != 42 {
		t.Fatalf("streamID: got %d", decoded.StreamID)
	}
	if string(decoded.Data) != "hello from client" {
		t.Fatalf("data: got %q", decoded.Data)
	}
}

// TestUDPStreamOpenEncodeDecode tests stream open message over UDP.
func TestUDPStreamOpenEncodeDecode(t *testing.T) {
	msg := &pkgudp.StreamOpenMsg{
		StreamType: pkgudp.StreamTypeTCP,
		Addr:       "example.com",
		Port:       443,
	}
	data := msg.Encode()

	decoded, err := pkgudp.DecodeStreamOpen(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.StreamType != pkgudp.StreamTypeTCP {
		t.Fatalf("type: got %d", decoded.StreamType)
	}
	if decoded.Addr != "example.com" {
		t.Fatalf("addr: got %q", decoded.Addr)
	}
	if decoded.Port != 443 {
		t.Fatalf("port: got %d", decoded.Port)
	}
}
