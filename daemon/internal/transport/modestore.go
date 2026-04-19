package transport

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultModePath returns the OS-appropriate path for the persisted
// transport-mode preference. Mirrors the key/token stores in
// daemon/internal/sites — same directory, different filename.
func DefaultModePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "Proxyness", "transport-mode")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "proxyness", "transport-mode")
}

// LoadMode reads the persisted mode from path. Returns ModeAuto if the
// file is missing, unreadable, or contains anything other than one of
// the three valid modes. Never returns an error — a bad/missing file
// just falls back to the default so the daemon can always start.
func LoadMode(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ModeAuto
	}
	mode := strings.TrimSpace(string(data))
	switch mode {
	case ModeAuto, ModeUDP, ModeTLS:
		return mode
	default:
		return ModeAuto
	}
}

// SaveMode writes the mode to path with mode 0600 (same as the key/token
// stores), creating the parent directory if needed. Returns an error on
// I/O failure — the caller should log but not fatal, since an unwritable
// config dir shouldn't take down the daemon.
func SaveMode(path, mode string) error {
	switch mode {
	case ModeAuto, ModeUDP, ModeTLS:
	default:
		return os.ErrInvalid
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(mode), 0600)
}
