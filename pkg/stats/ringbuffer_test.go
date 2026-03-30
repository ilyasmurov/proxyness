package stats

import "testing"

func TestRingBufferAddAndSlice(t *testing.T) {
	rb := NewRingBuffer()
	rb.Add(RatePoint{Timestamp: 1, BytesIn: 100, BytesOut: 50})
	rb.Add(RatePoint{Timestamp: 2, BytesIn: 200, BytesOut: 80})

	s := rb.Slice()
	if len(s) != 2 {
		t.Fatalf("expected 2 points, got %d", len(s))
	}
	if s[0].Timestamp != 1 || s[1].Timestamp != 2 {
		t.Fatalf("wrong order: %v", s)
	}
	if s[0].BytesIn != 100 || s[1].BytesIn != 200 {
		t.Fatalf("wrong values: %v", s)
	}
}

func TestRingBufferWraparound(t *testing.T) {
	rb := NewRingBuffer()
	for i := int64(0); i < 310; i++ {
		rb.Add(RatePoint{Timestamp: i, BytesIn: i * 10, BytesOut: i})
	}

	s := rb.Slice()
	if len(s) != 300 {
		t.Fatalf("expected 300 points, got %d", len(s))
	}
	if s[0].Timestamp != 10 {
		t.Fatalf("expected oldest timestamp 10, got %d", s[0].Timestamp)
	}
	if s[299].Timestamp != 309 {
		t.Fatalf("expected newest timestamp 309, got %d", s[299].Timestamp)
	}
}

func TestRingBufferEmpty(t *testing.T) {
	rb := NewRingBuffer()
	s := rb.Slice()
	if len(s) != 0 {
		t.Fatalf("expected empty slice, got %d", len(s))
	}
}
