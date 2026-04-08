// Package sites holds the daemon-side state for the browser extension's
// /sites/* HTTP API: persisted device key, persisted extension auth token,
// in-memory my_sites cache, and an HTTP client to the server's /api/sync.
package sites

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// KeyStore is a tiny on-disk persisted device key. The file is mode 0600.
// The key is loaded once on Save and on the first Load call; subsequent
// Loads return the cached value.
type KeyStore struct {
	path string
	mu   sync.RWMutex
	key  string
}

func NewKeyStore(path string) *KeyStore {
	return &KeyStore{path: path}
}

// Load returns the persisted key or "" if no file exists. Reads from disk
// only on first call; subsequent calls hit the cache.
func (s *KeyStore) Load() string {
	s.mu.RLock()
	if s.key != "" {
		k := s.key
		s.mu.RUnlock()
		return k
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != "" {
		return s.key
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return ""
	}
	s.key = strings.TrimSpace(string(data))
	return s.key
}

// Save writes the key to disk with mode 0600 and updates the cache.
func (s *KeyStore) Save(key string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, []byte(key), 0600); err != nil {
		return err
	}
	s.mu.Lock()
	s.key = key
	s.mu.Unlock()
	return nil
}

// DefaultKeyPath returns the OS-appropriate path for the persisted device key.
func DefaultKeyPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "SmurovProxy", "device-key")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "smurov-proxy", "device-key")
}
