package machineid

import (
	"crypto/sha256"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Fingerprint returns a 16-byte hardware-based machine fingerprint.
// SHA256(hardware_id + "proxyness"), first 16 bytes.
// Stable across reboots, network changes, VPN on/off.
func Fingerprint() [16]byte {
	id := resolveHardwareID()
	hash := sha256.Sum256([]byte(id + "proxyness"))
	var fp [16]byte
	copy(fp[:], hash[:16])
	return fp
}

var (
	resolveMu sync.Mutex
	// resolvedID memoizes the hardware ID for the life of the process.
	// IOPlatformUUID / MachineGuid never change on a running machine, so
	// once resolved there is no reason to shell out to ioreg again — and
	// the memo guarantees the fingerprint can't regress to "unknown"
	// mid-session (e.g. ioreg racing IOKit right after wake from sleep).
	resolvedID string
)

func resolveHardwareID() string {
	resolveMu.Lock()
	defer resolveMu.Unlock()
	if resolvedID != "" {
		return resolvedID
	}
	id := resolveWith(hardwareID, defaultCachePath())
	if id != "unknown" {
		resolvedID = id
	}
	return id
}

// resolveWith runs the resolution ladder: live hardware query → on-disk
// cache of the last successful query → "unknown". A successful live query
// refreshes the cache. The cache exists because the live query can fail
// transiently (ioreg returns no IOPlatformUUID for several seconds after
// wake from sleep); before it existed the daemon sent the "unknown"
// fingerprint, the server answered "machine id rejected", and the client
// treated that as a fatal device-binding conflict.
func resolveWith(query func() string, cachePath string) string {
	if id := query(); id != "" {
		if err := saveCachedID(cachePath, id); err != nil {
			log.Printf("[machineid] cache write failed: %v", err)
		}
		return id
	}
	if id := loadCachedID(cachePath); id != "" {
		log.Printf("[machineid] hardware query failed — using cached hardware ID")
		return id
	}
	log.Printf("[machineid] WARNING: hardware query failed and no cache — sending 'unknown' fingerprint, server will reject")
	return "unknown"
}

// defaultCachePath mirrors the key/token/transport-mode stores in
// daemon/internal — same directory, different filename.
func defaultCachePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("APPDATA"), "Proxyness", "machine-id")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "proxyness", "machine-id")
}

func loadCachedID(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveCachedID(path, id string) error {
	old, err := os.ReadFile(path)
	if err == nil && strings.TrimSpace(string(old)) == id {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(id), 0600)
}
