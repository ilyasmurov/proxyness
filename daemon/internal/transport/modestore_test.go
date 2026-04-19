package transport

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModeReturnsAutoForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")
	if got := LoadMode(path); got != ModeAuto {
		t.Fatalf("got %q, want %q", got, ModeAuto)
	}
}

func TestLoadModeReturnsPersistedValue(t *testing.T) {
	for _, mode := range []string{ModeAuto, ModeUDP, ModeTLS} {
		path := filepath.Join(t.TempDir(), "transport-mode")
		if err := os.WriteFile(path, []byte(mode), 0600); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if got := LoadMode(path); got != mode {
			t.Fatalf("mode=%q: got %q, want %q", mode, got, mode)
		}
	}
}

func TestLoadModeTrimsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-mode")
	if err := os.WriteFile(path, []byte("  udp\n"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := LoadMode(path); got != ModeUDP {
		t.Fatalf("got %q, want %q", got, ModeUDP)
	}
}

func TestLoadModeReturnsAutoForGarbage(t *testing.T) {
	// Don't crash or leak an invalid mode through — users can hand-edit
	// the file and the daemon shouldn't try to build a transport out of
	// whatever they wrote.
	path := filepath.Join(t.TempDir(), "transport-mode")
	if err := os.WriteFile(path, []byte("quic"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if got := LoadMode(path); got != ModeAuto {
		t.Fatalf("got %q, want %q", got, ModeAuto)
	}
}

func TestSaveModeRejectsInvalidMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-mode")
	if err := SaveMode(path, "bogus"); err == nil {
		t.Fatalf("expected error for invalid mode, got nil")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid input must not create the file, stat err=%v", err)
	}
}

func TestSaveModeCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "subdir", "transport-mode")
	if err := SaveMode(path, ModeUDP); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := LoadMode(path); got != ModeUDP {
		t.Fatalf("roundtrip: got %q, want %q", got, ModeUDP)
	}
}

func TestSaveModeOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-mode")
	if err := SaveMode(path, ModeUDP); err != nil {
		t.Fatalf("save udp: %v", err)
	}
	if err := SaveMode(path, ModeTLS); err != nil {
		t.Fatalf("save tls: %v", err)
	}
	if got := LoadMode(path); got != ModeTLS {
		t.Fatalf("got %q, want %q", got, ModeTLS)
	}
}

func TestSaveModeFilePermissionsAre0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "transport-mode")
	if err := SaveMode(path, ModeTLS); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// On Windows file mode bits below 0700 are mostly cosmetic but the
	// check at least proves SaveMode didn't widen perms accidentally.
	if perm := info.Mode().Perm() &^ 0600; perm != 0 {
		t.Fatalf("got perm %#o, want <= 0600", info.Mode().Perm())
	}
}
