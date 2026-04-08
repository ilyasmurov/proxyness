package sites

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStoreGenerateOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon-token")
	store := NewTokenStore(path)

	tok1, err := store.GetOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok1) != 64 {
		t.Fatalf("expected 64-char hex token, got %d chars: %q", len(tok1), tok1)
	}

	// Second call returns the same token.
	tok2, _ := store.GetOrCreate()
	if tok1 != tok2 {
		t.Fatalf("expected stable token, got %q vs %q", tok1, tok2)
	}

	// Fresh store loads the same token.
	store2 := NewTokenStore(path)
	tok3, _ := store2.GetOrCreate()
	if tok1 != tok3 {
		t.Fatalf("expected loaded token to match, got %q vs %q", tok1, tok3)
	}

	// File mode 0600.
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("expected mode 0600, got %o", info.Mode().Perm())
	}
}

func TestTokenStoreCheck(t *testing.T) {
	dir := t.TempDir()
	store := NewTokenStore(filepath.Join(dir, "daemon-token"))
	tok, _ := store.GetOrCreate()

	if !store.Check(tok) {
		t.Fatalf("expected Check(valid) = true")
	}
	if store.Check("wrong") {
		t.Fatalf("expected Check(wrong) = false")
	}
	if store.Check("") {
		t.Fatalf("expected Check(empty) = false")
	}
}
