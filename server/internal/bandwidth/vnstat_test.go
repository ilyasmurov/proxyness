package bandwidth

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// fixtureNow is just after the "updated" timestamp in testdata/vnstat.json,
// so the fixture reads as fresh.
var fixtureNow = time.Unix(1785416760, 0)

func loadFixture(t *testing.T) *Snapshot {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "vnstat.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	snap, err := Parse(data, fixtureNow)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return snap
}

func find(t *testing.T, snap *Snapshot, name string) Interface {
	t.Helper()
	for _, iface := range snap.Interfaces {
		if iface.Name == name {
			return iface
		}
	}
	t.Fatalf("interface %q missing, got %v", name, names(snap))
	return Interface{}
}

func names(snap *Snapshot) []string {
	out := make([]string, 0, len(snap.Interfaces))
	for _, iface := range snap.Interfaces {
		out = append(out, iface.Name)
	}
	return out
}

func TestParseKeepsRealInterfaces(t *testing.T) {
	snap := loadFixture(t)

	find(t, snap, "enp0s5")
	find(t, snap, "awg0")
}

func TestParseDropsVirtualInterfaces(t *testing.T) {
	snap := loadFixture(t)

	for _, iface := range snap.Interfaces {
		if isVirtual(iface.Name) {
			t.Errorf("virtual interface %q should have been dropped", iface.Name)
		}
	}
}

func TestParseSortsPointsChronologically(t *testing.T) {
	iface := find(t, loadFixture(t), "enp0s5")

	if len(iface.FiveMinute) < 2 {
		t.Fatalf("expected multiple five-minute points, got %d", len(iface.FiveMinute))
	}
	for i := 1; i < len(iface.FiveMinute); i++ {
		if iface.FiveMinute[i].T <= iface.FiveMinute[i-1].T {
			t.Fatalf("points out of order at %d: %d <= %d",
				i, iface.FiveMinute[i].T, iface.FiveMinute[i-1].T)
		}
	}
}

func TestParseComputesRates(t *testing.T) {
	// 37.5 MB over a five-minute window is exactly 1 Mbit/s.
	data := []byte(`{"interfaces":[{"name":"eth0","updated":{"timestamp":1785416700},
		"traffic":{"total":{"rx":1,"tx":2},
		"fiveminute":[{"timestamp":1785416400,"rx":37500000,"tx":18750000}]}}]}`)

	snap, err := Parse(data, fixtureNow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := snap.Interfaces[0].FiveMinute[0]

	if math.Abs(got.RxMbps-1) > 1e-9 {
		t.Errorf("RxMbps = %v, want 1", got.RxMbps)
	}
	if math.Abs(got.TxMbps-0.5) > 1e-9 {
		t.Errorf("TxMbps = %v, want 0.5", got.TxMbps)
	}
}

func TestParseDropsSamplesOutsideWindow(t *testing.T) {
	// One sample inside the 24h five-minute window, one two days old.
	recent := fixtureNow.Add(-time.Hour).Unix()
	old := fixtureNow.Add(-48 * time.Hour).Unix()
	data := []byte(`{"interfaces":[{"name":"eth0","updated":{"timestamp":1785416700},
		"traffic":{"fiveminute":[
			{"timestamp":` + strconv.FormatInt(old, 10) + `,"rx":1,"tx":1},
			{"timestamp":` + strconv.FormatInt(recent, 10) + `,"rx":2,"tx":2}]}}]}`)

	snap, err := Parse(data, fixtureNow)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	points := snap.Interfaces[0].FiveMinute

	if len(points) != 1 {
		t.Fatalf("got %d points, want 1 (the stale one must be dropped)", len(points))
	}
	if points[0].T != recent {
		t.Errorf("kept point at %d, want %d", points[0].T, recent)
	}
}

func TestParseFlagsStaleExport(t *testing.T) {
	data := []byte(`{"interfaces":[{"name":"eth0","updated":{"timestamp":1785416700},
		"traffic":{"total":{"rx":1,"tx":1}}}]}`)

	fresh, err := Parse(data, time.Unix(1785416700, 0).Add(5*time.Minute))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fresh.Stale {
		t.Error("export 5 minutes old should not be stale")
	}

	stale, err := Parse(data, time.Unix(1785416700, 0).Add(20*time.Minute))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !stale.Stale {
		t.Error("export 20 minutes old should be stale")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json"), fixtureNow); err == nil {
		t.Fatal("expected an error for a missing export")
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not json"), fixtureNow); err == nil {
		t.Fatal("expected an error for malformed input")
	}
}
