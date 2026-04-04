package arq

import (
	"testing"
)

func TestAckEncodeDecodeEmpty(t *testing.T) {
	a := AckData{CumAck: 10}
	buf := a.Encode()
	got, err := DecodeAckData(buf)
	if err != nil {
		t.Fatalf("DecodeAckData error: %v", err)
	}
	if got.CumAck != 10 {
		t.Errorf("CumAck: want 10, got %d", got.CumAck)
	}
	for i, b := range got.Bitmap {
		if b != 0 {
			t.Errorf("Bitmap[%d] = %d, want 0", i, b)
		}
	}
}

func TestAckEncodeDecodeBitmap(t *testing.T) {
	a := AckData{CumAck: 100}
	a.SetReceived(101)
	a.SetReceived(103)
	a.SetReceived(164)

	buf := a.Encode()
	got, err := DecodeAckData(buf)
	if err != nil {
		t.Fatalf("DecodeAckData error: %v", err)
	}
	if got.CumAck != 100 {
		t.Errorf("CumAck: want 100, got %d", got.CumAck)
	}
	if !got.IsReceived(101) {
		t.Error("expected 101 to be received")
	}
	if !got.IsReceived(103) {
		t.Error("expected 103 to be received")
	}
	if !got.IsReceived(164) {
		t.Error("expected 164 to be received")
	}
	if got.IsReceived(102) {
		t.Error("expected 102 to NOT be received")
	}
	if got.IsReceived(105) {
		t.Error("expected 105 to NOT be received")
	}
}

func TestAckBitmapOutOfRange(t *testing.T) {
	a := AckData{CumAck: 100}

	// At or below CumAck — should not set
	a.SetReceived(100)
	a.SetReceived(99)
	for i, b := range a.Bitmap {
		if b != 0 {
			t.Errorf("Bitmap[%d] = %d after SetReceived(<=CumAck), want 0", i, b)
		}
	}

	// pktNum = CumAck + 257 — out of 256-bit range, should not set
	a.SetReceived(357)
	for i, b := range a.Bitmap {
		if b != 0 {
			t.Errorf("Bitmap[%d] = %d after SetReceived(out of range), want 0", i, b)
		}
	}
}
