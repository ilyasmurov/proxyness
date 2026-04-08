package sites

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// TokenStore is a per-extension auth token persisted to disk. Generated
// on first GetOrCreate; the same value is returned on subsequent calls.
// File mode 0600.
type TokenStore struct {
	path  string
	mu    sync.RWMutex
	token string
}

func NewTokenStore(path string) *TokenStore {
	return &TokenStore{path: path}
}

// GetOrCreate returns the persisted token, generating + saving a new one
// if no file exists.
func (s *TokenStore) GetOrCreate() (string, error) {
	s.mu.RLock()
	if s.token != "" {
		t := s.token
		s.mu.RUnlock()
		return t, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" {
		return s.token, nil
	}

	// Try to load.
	if data, err := os.ReadFile(s.path); err == nil {
		s.token = strings.TrimSpace(string(data))
		if len(s.token) == 64 {
			return s.token, nil
		}
		// Corrupted; fall through to regenerate.
	}

	// Generate new.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s.token = hex.EncodeToString(b)

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(s.path, []byte(s.token), 0600); err != nil {
		return "", err
	}
	return s.token, nil
}

// Check is a constant-time comparison of the provided token against the
// stored one. Returns false if no token has been generated yet.
func (s *TokenStore) Check(provided string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.token == "" || provided == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s.token), []byte(provided)) == 1
}

// DefaultTokenPath returns the OS-appropriate path for the daemon token.
func DefaultTokenPath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "SmurovProxy", "daemon-token")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "smurov-proxy", "daemon-token")
}
