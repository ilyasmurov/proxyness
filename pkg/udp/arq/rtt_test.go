package arq

import (
	"testing"
	"time"
)

func TestRTTFirstSample(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)

	srtt := r.SRTT()
	rto := r.RTO()

	if srtt != 80*time.Millisecond {
		t.Errorf("SRTT = %v, want 80ms", srtt)
	}
	// rttvar = sample/2 = 40ms, rto = srtt + 4*rttvar = 80 + 160 = 240ms
	if rto != 240*time.Millisecond {
		t.Errorf("RTO = %v, want 240ms", rto)
	}
}

func TestRTTSubsequentSamples(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)
	r.Update(100 * time.Millisecond)

	srtt := r.SRTT()
	// srtt = 7/8*80 + 1/8*100 = 70 + 12.5 = 82.5ms
	want := time.Duration(float64(80*time.Millisecond)*0.875 + float64(100*time.Millisecond)*0.125)
	if srtt != want {
		t.Errorf("SRTT = %v, want %v", srtt, want)
	}
}

func TestRTOMinClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(5 * time.Millisecond)

	if r.RTO() < 100*time.Millisecond {
		t.Errorf("RTO = %v, want >= 100ms", r.RTO())
	}
}

func TestRTOMaxClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(800 * time.Millisecond)

	if r.RTO() > 500*time.Millisecond {
		t.Errorf("RTO = %v, want <= 500ms", r.RTO())
	}
}

func TestRTOBackoff(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)

	before := r.RTO()
	r.Backoff()
	after := r.RTO()

	if after != before*2 {
		t.Errorf("RTO after backoff = %v, want %v", after, before*2)
	}
}

func TestRTOBackoffClamp(t *testing.T) {
	r := NewRTTEstimator()
	r.Update(80 * time.Millisecond)

	for i := 0; i < 10; i++ {
		r.Backoff()
	}

	if r.RTO() > 500*time.Millisecond {
		t.Errorf("RTO = %v, want <= 500ms", r.RTO())
	}
}
