package arq

import (
	"sync"
	"testing"
	"time"

	pkgudp "smurov-proxy/pkg/udp"
)

// mockSender captures sent datagrams for inspection.
type mockSender struct {
	mu   sync.Mutex
	sent [][]byte
}

func (m *mockSender) send(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.sent = append(m.sent, cp)
	return nil
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func TestControllerSendAndReceiveAck(t *testing.T) {
	sender := &mockSender{}
	var delivered [][]byte
	var deliverMu sync.Mutex

	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		cp := make([]byte, len(data))
		copy(cp, data)
		delivered = append(delivered, cp)
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	if err := ctrl.CreateRecvBuffer(1); err != nil {
		t.Fatalf("CreateRecvBuffer: %v", err)
	}

	// Send 3 packets
	for i := 0; i < 3; i++ {
		err := ctrl.Send(pkgudp.MsgStreamData, 1, uint32(i), []byte("hello"))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	if sender.count() != 3 {
		t.Fatalf("sent: got %d, want 3", sender.count())
	}

	// Simulate ACK for all 3 (cumAck=3, pktNums were 1,2,3)
	ack := &AckData{CumAck: 3}
	ctrl.HandleAck(ack.Encode())

	time.Sleep(10 * time.Millisecond)
	if ctrl.sendBuf.Len() != 0 {
		t.Fatalf("sendBuf: got %d, want 0", ctrl.sendBuf.Len())
	}
}

func TestControllerReceiveInOrder(t *testing.T) {
	sender := &mockSender{}
	var delivered []string
	var deliverMu sync.Mutex

	key := make([]byte, 32)
	ctrl := New(0xABCD, key, sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		delivered = append(delivered, string(data))
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	if err := ctrl.CreateRecvBuffer(1); err != nil {
		t.Fatalf("CreateRecvBuffer: %v", err)
	}

	for i := 0; i < 3; i++ {
		ctrl.HandleData(&pkgudp.Packet{
			Type:     pkgudp.MsgStreamData,
			PktNum:   uint32(i + 1),
			StreamID: 1,
			Seq:      uint32(i),
			Data:     []byte("msg"),
		})
	}

	deliverMu.Lock()
	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	deliverMu.Unlock()
}

func TestControllerReceiveOutOfOrder(t *testing.T) {
	sender := &mockSender{}
	var delivered []uint32
	var deliverMu sync.Mutex

	key := make([]byte, 32)
	ctrl := New(0xABCD, key, sender.send, func(streamID uint32, data []byte) {
		deliverMu.Lock()
		delivered = append(delivered, uint32(data[0]))
		deliverMu.Unlock()
	})
	defer ctrl.Close()

	if err := ctrl.CreateRecvBuffer(1); err != nil {
		t.Fatalf("CreateRecvBuffer: %v", err)
	}

	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 3, StreamID: 1, Seq: 2, Data: []byte{2},
	})
	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 1, StreamID: 1, Seq: 0, Data: []byte{0},
	})
	ctrl.HandleData(&pkgudp.Packet{
		Type: pkgudp.MsgStreamData, PktNum: 2, StreamID: 1, Seq: 1, Data: []byte{1},
	})

	deliverMu.Lock()
	if len(delivered) != 3 {
		t.Fatalf("delivered: got %d, want 3", len(delivered))
	}
	for i, v := range delivered {
		if v != uint32(i) {
			t.Fatalf("delivered[%d] = %d, want %d", i, v, i)
		}
	}
	deliverMu.Unlock()
}

func TestControllerCwndBackpressure(t *testing.T) {
	sender := &mockSender{}
	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(uint32, []byte) {})
	defer ctrl.Close()

	for i := 0; i < fixedCwnd; i++ {
		err := ctrl.Send(pkgudp.MsgStreamData, 1, uint32(i), []byte("x"))
		if err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
	}

	done := make(chan error, 1)
	go func() {
		done <- ctrl.Send(pkgudp.MsgStreamData, 1, uint32(fixedCwnd), []byte("x"))
	}()

	select {
	case <-done:
		t.Fatal("send should have blocked (cwnd full)")
	case <-time.After(50 * time.Millisecond):
	}

	ack := &AckData{CumAck: 1}
	ctrl.HandleAck(ack.Encode())

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("send after ack: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("send should have unblocked after ACK")
	}
}

func TestControllerRetransmit(t *testing.T) {
	sender := &mockSender{}
	ctrl := New(0xABCD, make([]byte, 32), sender.send, func(uint32, []byte) {})
	defer ctrl.Close()

	ctrl.Send(pkgudp.MsgStreamData, 1, 0, []byte("data"))
	initialCount := sender.count()

	p := ctrl.sendBuf.FirstUnacked()
	if p == nil {
		t.Fatal("should have an unacked packet")
	}
	p.LastSentAt = time.Now().Add(-5 * time.Second)

	ctrl.RetransmitTick()

	time.Sleep(10 * time.Millisecond)
	if sender.count() <= initialCount {
		t.Fatal("retransmit should have sent a new packet")
	}
}

func TestControllerMaxStreams(t *testing.T) {
	sender := &mockSender{}
	cfg := DefaultConfig()
	cfg.MaxStreams = 2

	ctrl := NewWithConfig(0xABCD, make([]byte, 32), sender.send, func(uint32, []byte) {}, cfg)
	defer ctrl.Close()

	if err := ctrl.CreateRecvBuffer(1); err != nil {
		t.Fatalf("stream 1: %v", err)
	}
	if err := ctrl.CreateRecvBuffer(2); err != nil {
		t.Fatalf("stream 2: %v", err)
	}
	if err := ctrl.CreateRecvBuffer(3); err == nil {
		t.Fatal("expected error for stream 3 exceeding max streams")
	}

	// Re-creating existing stream should succeed (idempotent)
	if err := ctrl.CreateRecvBuffer(1); err != nil {
		t.Fatalf("re-create stream 1: %v", err)
	}
}
