package transport

import (
	"net"
	"testing"
	"time"
)

// TestUDPStreamCloseUnblocksRead is a regression test for a goroutine leak
// where udpStream.Close() removed the stream from the transport's map and
// sent MsgStreamClose, but never closed s.done — so any goroutine blocked in
// Read() would wait forever (s.recvCh will never receive because the stream
// is gone from the map; s.done is never closed). Observed in the wild as
// ~9500 leaked goroutines after ~12h of normal proxy traffic, with CPU
// pegged at 200% in runtime.gcBgMarkWorker.
func TestUDPStreamCloseUnblocksRead(t *testing.T) {
	// Create a UDP socket pair so sendPacketDirect doesn't panic on nil conn.
	c1, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	conn := c1.(*net.UDPConn)

	tr := &UDPTransport{
		streams:    make(map[uint32]*udpStream),
		sessionKey: make([]byte, 32), // dummy key
		conn:       conn,
		connID:     1,
		done:       make(chan struct{}),
	}

	s := &udpStream{
		t:      tr,
		id:     42,
		recvCh: make(chan []byte, 1),
		done:   make(chan struct{}),
	}
	tr.mu.Lock()
	tr.streams[s.id] = s
	tr.mu.Unlock()

	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 64)
		_, err := s.Read(buf)
		readDone <- err
	}()

	// Give Read time to block in its select.
	time.Sleep(20 * time.Millisecond)

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-readDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Read did not unblock within 500ms after Close — goroutine leaked")
	}
}
