package arq

import (
	"testing"
	"time"
)

func TestSendBufferAddAndAck(t *testing.T) {
	sb := NewSendBuffer(16)

	snap := DeliverySnapshot{}
	sb.Add(1, []byte{1}, 0x01, 0, 1, []byte("a"), snap)
	sb.Add(2, []byte{2}, 0x01, 0, 2, []byte("b"), snap)
	sb.Add(3, []byte{3}, 0x01, 0, 3, []byte("c"), snap)

	if sb.Len() != 3 {
		t.Fatalf("expected Len=3, got %d", sb.Len())
	}

	removed := sb.AckCumulative(2)
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}
	if sb.Len() != 1 {
		t.Fatalf("expected Len=1 after cumulative ack, got %d", sb.Len())
	}

	pkt := sb.Get(3)
	if pkt == nil {
		t.Fatal("expected packet 3 to still be present")
	}
}

func TestSendBufferAckSelective(t *testing.T) {
	sb := NewSendBuffer(16)

	snap := DeliverySnapshot{}
	sb.Add(1, []byte{1}, 0x01, 0, 1, []byte("a"), snap)
	sb.Add(2, []byte{2}, 0x01, 0, 2, []byte("b"), snap)
	sb.Add(3, []byte{3}, 0x01, 0, 3, []byte("c"), snap)

	sb.AckSelective(3)

	pkt3 := sb.Get(3)
	if pkt3 == nil {
		t.Fatal("expected packet 3 to exist")
	}
	if !pkt3.Acked {
		t.Fatal("expected packet 3 to be marked acked")
	}

	pkt2 := sb.Get(2)
	if pkt2 == nil {
		t.Fatal("expected packet 2 to still be present")
	}
	if pkt2.Acked {
		t.Fatal("expected packet 2 to remain unacked")
	}
}

func TestSendBufferExpired(t *testing.T) {
	sb := NewSendBuffer(16)

	snap := DeliverySnapshot{}
	sb.Add(1, []byte{1}, 0x01, 0, 1, []byte("a"), snap)
	sb.Add(2, []byte{2}, 0x01, 0, 2, []byte("b"), snap)

	// Age packet 1 artificially.
	sb.mu.Lock()
	sb.packets[1].LastSentAt = time.Now().Add(-500 * time.Millisecond)
	sb.mu.Unlock()

	expired := sb.Expired(200 * time.Millisecond)
	if len(expired) != 1 {
		t.Fatalf("expected 1 expired packet, got %d", len(expired))
	}
	if expired[0].PktNum != 1 {
		t.Fatalf("expected expired packet 1, got %d", expired[0].PktNum)
	}
}

func TestSendBufferMaxRetransmit(t *testing.T) {
	sb := NewSendBuffer(16)

	sb.Add(1, []byte{1}, 0x01, 0, 1, []byte("a"), DeliverySnapshot{})

	sb.mu.Lock()
	sb.packets[1].Retransmits = maxRetransmits
	sb.mu.Unlock()

	if !sb.IsMaxRetransmits(1) {
		t.Fatal("expected IsMaxRetransmits(1) to be true")
	}
}

func TestSendBufferRTTSample(t *testing.T) {
	sb := NewSendBuffer(16)

	before := time.Now()
	sb.Add(1, []byte{1}, 0x01, 0, 1, []byte("a"), DeliverySnapshot{})
	time.Sleep(10 * time.Millisecond)

	pkt := sb.Get(1)
	if pkt == nil {
		t.Fatal("expected packet 1 to exist")
	}

	sample := time.Since(pkt.SentAt)
	if sample < 10*time.Millisecond {
		t.Fatalf("expected sample >= 10ms, got %v (SentAt=%v, before=%v)", sample, pkt.SentAt, before)
	}

	// IsRetransmit with Retransmits=0 → false.
	if pkt.IsRetransmit() {
		t.Fatal("expected IsRetransmit()=false for fresh packet")
	}

	// Set Retransmits=1 → true.
	sb.mu.Lock()
	sb.packets[1].Retransmits = 1
	sb.mu.Unlock()

	if !pkt.IsRetransmit() {
		t.Fatal("expected IsRetransmit()=true after setting Retransmits=1")
	}
}
