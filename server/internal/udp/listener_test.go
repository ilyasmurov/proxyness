package udp

import (
	"net"
	"sync"
	"testing"
	"time"

	pkgudp "proxyness/pkg/udp"
)

// fakePacketConn records every datagram written to it. ReadFrom is never
// called: these tests drive handlePacket directly instead of Serve.
type fakePacketConn struct {
	mu     sync.Mutex
	writes [][]byte
}

func (f *fakePacketConn) ReadFrom([]byte) (int, net.Addr, error) { return 0, nil, net.ErrClosed }
func (f *fakePacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), b...))
	return len(b), nil
}
func (f *fakePacketConn) Close() error                     { return nil }
func (f *fakePacketConn) LocalAddr() net.Addr              { return &net.UDPAddr{IP: net.IPv4zero, Port: 8443} }
func (f *fakePacketConn) SetDeadline(time.Time) error      { return nil }
func (f *fakePacketConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakePacketConn) SetWriteDeadline(time.Time) error { return nil }

func (f *fakePacketConn) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakePacketConn) last() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes[len(f.writes)-1]
}

func testKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return key
}

func decodeSessionClose(t *testing.T, data []byte, key []byte, token uint32) {
	t.Helper()
	pkt, err := pkgudp.DecodePacket(data, key)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if pkt.Type != pkgudp.MsgSessionClose || pkt.ConnID != token {
		t.Fatalf("expected MsgSessionClose for session %d, got type=0x%02x conn=%d", token, pkt.Type, pkt.ConnID)
	}
}

// A session whose ARQ hit the retransmit limit is torn down, the client is
// told to reconnect, and — because that client was unreachable at the time —
// its late packets keep getting the same answer for a while (PRXNS-18).
func TestKilledSessionNotifiesClientAndLeavesTombstone(t *testing.T) {
	conn := &fakePacketConn{}
	l := NewListener(conn, nil, nil)
	key := testKey()
	token := l.sessions.Create(key, 7)
	sess, _ := l.sessions.Get(token)
	client := &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 5555}
	sess.mu.Lock()
	sess.ClientAddr = client
	sess.mu.Unlock()

	l.killDeadSession(sess)

	if _, ok := l.sessions.Get(token); ok {
		t.Fatal("dead session must be removed")
	}
	if conn.count() != 1 {
		t.Fatalf("expected one MsgSessionClose to the client, got %d datagrams", conn.count())
	}
	decodeSessionClose(t, conn.last(), key, token)

	// A late packet for the dead session gets the same answer …
	late, err := pkgudp.EncodePacket(&pkgudp.Packet{ConnID: token, Type: pkgudp.MsgKeepalive}, key)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	l.handlePacket(late, client)
	if conn.count() != 2 {
		t.Fatalf("expected a tombstone reply, got %d datagrams", conn.count())
	}
	decodeSessionClose(t, conn.last(), key, token)

	// … but not more often than tombstoneReplyInterval …
	l.handlePacket(late, client)
	if conn.count() != 2 {
		t.Fatalf("tombstone replies must be rate-limited, got %d datagrams", conn.count())
	}

	// … and not at all once the tombstone has expired.
	l.tombMu.Lock()
	l.tombstones[token].expires = time.Now().Add(-time.Second)
	l.tombMu.Unlock()
	l.expireTombstones()
	l.handlePacket(late, client)
	if conn.count() != 2 {
		t.Fatalf("expired tombstone must stay silent, got %d datagrams", conn.count())
	}
}

func TestUnknownSessionWithoutTombstoneIsSilent(t *testing.T) {
	conn := &fakePacketConn{}
	l := NewListener(conn, nil, nil)
	data, err := pkgudp.EncodePacket(&pkgudp.Packet{ConnID: 12345, Type: pkgudp.MsgKeepalive}, testKey())
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	l.handlePacket(data, &net.UDPAddr{IP: net.IPv4(10, 0, 0, 2), Port: 1})
	if conn.count() != 0 {
		t.Fatalf("a packet for an unknown session must be ignored, got %d datagrams", conn.count())
	}
}
