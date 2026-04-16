package arq

import (
	"testing"
)

func TestRecvBufferInOrder(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(16, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(0, []byte("a"))
	rb.Insert(1, []byte("b"))
	rb.Insert(2, []byte("c"))

	if len(delivered) != 3 {
		t.Fatalf("expected 3 delivered, got %d", len(delivered))
	}
	for i, seq := range []uint32{0, 1, 2} {
		if delivered[i] != seq {
			t.Errorf("delivered[%d] = %d, want %d", i, delivered[i], seq)
		}
	}
}

func TestRecvBufferOutOfOrder(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(16, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(2, []byte("c"))
	rb.Insert(0, []byte("a"))
	rb.Insert(1, []byte("b"))

	if len(delivered) != 3 {
		t.Fatalf("expected 3 delivered, got %d", len(delivered))
	}
	for i, seq := range []uint32{0, 1, 2} {
		if delivered[i] != seq {
			t.Errorf("delivered[%d] = %d, want %d", i, delivered[i], seq)
		}
	}
}

func TestRecvBufferDuplicate(t *testing.T) {
	count := 0
	rb := NewRecvBuffer(16, func(seq uint32, data []byte) {
		count++
	})

	rb.Insert(0, []byte("a"))
	rb.Insert(0, []byte("a"))

	if count != 1 {
		t.Fatalf("expected count=1, got %d", count)
	}
}

func TestRecvBufferGapFill(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(16, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	rb.Insert(0, []byte("a"))
	rb.Insert(1, []byte("b"))
	// skip 2
	rb.Insert(3, []byte("d"))
	rb.Insert(4, []byte("e"))

	if len(delivered) != 2 {
		t.Fatalf("after gap: expected 2 delivered, got %d", len(delivered))
	}

	// fill the gap
	rb.Insert(2, []byte("c"))

	if len(delivered) != 5 {
		t.Fatalf("after fill: expected 5 delivered, got %d", len(delivered))
	}
	for i, seq := range []uint32{0, 1, 2, 3, 4} {
		if delivered[i] != seq {
			t.Errorf("delivered[%d] = %d, want %d", i, delivered[i], seq)
		}
	}
}

func TestRecvBufferMaxSize(t *testing.T) {
	var delivered []uint32
	rb := NewRecvBuffer(3, func(seq uint32, data []byte) {
		delivered = append(delivered, seq)
	})

	// buffer seqs 1, 2, 3 (max=3, all fit)
	rb.Insert(1, []byte("b"))
	rb.Insert(2, []byte("c"))
	rb.Insert(3, []byte("d"))

	// seq 4 should be dropped (buffer full)
	rb.Insert(4, []byte("e"))

	// seq 0 arrives → flushes 1,2,3 (seq 4 was dropped, so stop at 4)
	rb.Insert(0, []byte("a"))

	// delivered: 0, 1, 2, 3 = 4 items
	if len(delivered) != 4 {
		t.Fatalf("expected 4 delivered, got %d: %v", len(delivered), delivered)
	}
	for i, seq := range []uint32{0, 1, 2, 3} {
		if delivered[i] != seq {
			t.Errorf("delivered[%d] = %d, want %d", i, delivered[i], seq)
		}
	}
}
