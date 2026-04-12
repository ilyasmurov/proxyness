package stats

import (
	"testing"

	pkgstats "proxyness/pkg/stats"
)

func TestSmoothRate(t *testing.T) {
	tests := []struct {
		name   string
		hist   []pkgstats.RatePoint
		window int
		wantD  int64
		wantU  int64
	}{
		{
			name:   "empty history",
			hist:   nil,
			window: 5,
			wantD:  0,
			wantU:  0,
		},
		{
			name: "single burst followed by idle seconds averages across window",
			hist: []pkgstats.RatePoint{
				{BytesIn: 5000, BytesOut: 500},
				{BytesIn: 0, BytesOut: 0},
				{BytesIn: 0, BytesOut: 0},
				{BytesIn: 0, BytesOut: 0},
				{BytesIn: 0, BytesOut: 0},
			},
			window: 5,
			wantD:  1000,
			wantU:  100,
		},
		{
			name: "all idle returns zero",
			hist: []pkgstats.RatePoint{
				{}, {}, {}, {}, {},
			},
			window: 5,
			wantD:  0,
			wantU:  0,
		},
		{
			name: "fewer points than window divides by actual count",
			hist: []pkgstats.RatePoint{
				{BytesIn: 1000, BytesOut: 100},
				{BytesIn: 3000, BytesOut: 300},
			},
			window: 5,
			wantD:  2000,
			wantU:  200,
		},
		{
			name: "ignores points older than window",
			hist: []pkgstats.RatePoint{
				{BytesIn: 999999, BytesOut: 999999}, // older than window, excluded
				{BytesIn: 1000, BytesOut: 100},
				{BytesIn: 1000, BytesOut: 100},
				{BytesIn: 1000, BytesOut: 100},
				{BytesIn: 1000, BytesOut: 100},
				{BytesIn: 1000, BytesOut: 100},
			},
			window: 5,
			wantD:  1000,
			wantU:  100,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, u := smoothRate(tc.hist, tc.window)
			if d != tc.wantD || u != tc.wantU {
				t.Errorf("smoothRate(%s): got (down=%d, up=%d), want (down=%d, up=%d)",
					tc.name, d, u, tc.wantD, tc.wantU)
			}
		})
	}
}
