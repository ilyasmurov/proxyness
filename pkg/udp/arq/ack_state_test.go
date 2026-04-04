package arq

import (
	"testing"
)

func TestAckStateInOrder(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	as.RecordReceived(2)
	as.RecordReceived(3)
	ack := as.BuildAck()
	if ack.CumAck != 3 {
		t.Errorf("CumAck: want 3, got %d", ack.CumAck)
	}
}

func TestAckStateGap(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	as.RecordReceived(3)
	as.RecordReceived(4)
	ack := as.BuildAck()
	if ack.CumAck != 1 {
		t.Errorf("CumAck: want 1, got %d", ack.CumAck)
	}
	if !ack.IsReceived(3) {
		t.Error("expected 3 to be in bitmap")
	}
	if !ack.IsReceived(4) {
		t.Error("expected 4 to be in bitmap")
	}
	if ack.IsReceived(2) {
		t.Error("expected 2 to NOT be in bitmap")
	}
}

func TestAckStateGapFill(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	as.RecordReceived(3)
	as.RecordReceived(2)
	ack := as.BuildAck()
	if ack.CumAck != 3 {
		t.Errorf("CumAck: want 3, got %d", ack.CumAck)
	}
}

func TestAckStateNeedsImmediateAck(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	if as.NeedsImmediateAck() {
		t.Error("expected false after in-order receive")
	}
	as.RecordReceived(3) // gap: 2 missing
	if !as.NeedsImmediateAck() {
		t.Error("expected true after gap detected")
	}
}

func TestAckStateDelayedAck(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	if as.NeedsDelayedAck() {
		t.Error("expected false after 1 packet")
	}
	as.RecordReceived(2)
	if !as.NeedsDelayedAck() {
		t.Error("expected true after 2 packets")
	}
	as.AckSent()
	if as.NeedsDelayedAck() {
		t.Error("expected false after AckSent()")
	}
}

func TestAckStateDupCount(t *testing.T) {
	as := NewAckState()
	as.RecordReceived(1)
	as.RecordReceived(3)
	as.RecordReceived(4)
	as.RecordReceived(5)
	if as.DupCount() != 3 {
		t.Errorf("DupCount: want 3, got %d", as.DupCount())
	}
	as.RecordReceived(2) // fills gap, cumAck advances to 5
	if as.DupCount() != 0 {
		t.Errorf("DupCount after gap fill: want 0, got %d", as.DupCount())
	}
}

func TestAckStateCleanup(t *testing.T) {
	as := NewAckState()
	for i := uint32(1); i <= 100; i++ {
		as.RecordReceived(i)
	}
	as.mu.Lock()
	size := len(as.received)
	as.mu.Unlock()
	if size != 0 {
		t.Errorf("received map size: want 0, got %d", size)
	}
}
