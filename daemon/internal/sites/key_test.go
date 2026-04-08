package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestKeyStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-key")

	store := NewKeyStore(path)

	if got := store.Load(); got != "" {
		t.Fatalf("expected empty on first load, got %q", got)
	}

	if err := store.Save("abc123"); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify file mode is 0600
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}

	// Load via a fresh store
	store2 := NewKeyStore(path)
	if got := store2.Load(); got != "abc123" {
		t.Fatalf("expected 'abc123', got %q", got)
	}
}

func TestKeyStoreOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device-key")
	store := NewKeyStore(path)

	store.Save("first")
	store.Save("second")

	if got := NewKeyStore(path).Load(); got != "second" {
		t.Fatalf("expected 'second', got %q", got)
	}
}
