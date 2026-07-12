package machineid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWithLiveQueryWritesCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "nested", "machine-id")
	got := resolveWith(func() string { return "UUID-LIVE" }, cache)
	if got != "UUID-LIVE" {
		t.Fatalf("resolveWith = %q, want UUID-LIVE", got)
	}
	data, err := os.ReadFile(cache)
	if err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	if string(data) != "UUID-LIVE" {
		t.Fatalf("cache = %q, want UUID-LIVE", data)
	}
}

func TestResolveWithFallsBackToCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "machine-id")
	if err := saveCachedID(cache, "UUID-CACHED"); err != nil {
		t.Fatal(err)
	}
	got := resolveWith(func() string { return "" }, cache)
	if got != "UUID-CACHED" {
		t.Fatalf("resolveWith = %q, want UUID-CACHED (cache fallback)", got)
	}
}

func TestResolveWithNoQueryNoCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "machine-id")
	got := resolveWith(func() string { return "" }, cache)
	if got != "unknown" {
		t.Fatalf("resolveWith = %q, want unknown", got)
	}
}

func TestResolveWithTrimsCachedWhitespace(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(cache, []byte("UUID-CACHED\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got := resolveWith(func() string { return "" }, cache)
	if got != "UUID-CACHED" {
		t.Fatalf("resolveWith = %q, want UUID-CACHED (trimmed)", got)
	}
}

func TestResolveWithUnwritableCacheStillReturnsID(t *testing.T) {
	// Cache path inside a file (not a dir) → MkdirAll fails. The live
	// query result must still win; an unwritable config dir must never
	// take down fingerprinting.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(blocker, "machine-id")
	got := resolveWith(func() string { return "UUID-LIVE" }, cache)
	if got != "UUID-LIVE" {
		t.Fatalf("resolveWith = %q, want UUID-LIVE despite cache write failure", got)
	}
}

func TestFingerprintStable(t *testing.T) {
	a := Fingerprint()
	b := Fingerprint()
	if a != b {
		t.Fatalf("Fingerprint not stable across calls: %x vs %x", a, b)
	}
	var zero [16]byte
	if a == zero {
		t.Fatal("Fingerprint is all zeros")
	}
}
