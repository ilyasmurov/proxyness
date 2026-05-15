package machineid

import (
	"crypto/sha256"
	"log"
)

// Fingerprint returns a 16-byte hardware-based machine fingerprint.
// SHA256(hardware_id + "proxyness"), first 16 bytes.
// Stable across reboots, network changes, VPN on/off.
func Fingerprint() [16]byte {
	id := hardwareID()
	if id == "" {
		id = "unknown"
		// hardwareID() exhausted its retry budget. The "unknown" fingerprint
		// will be rejected by the server with "machine id rejected" because
		// the real fingerprint is what's stored in DB. Grep for this line in
		// daemon.log to confirm the ioreg-after-wake hypothesis the next time
		// the user reports a morning rejection.
		log.Printf("[machineid] WARNING: hardwareID empty — sending 'unknown' fingerprint, server will reject")
	}
	hash := sha256.Sum256([]byte(id + "proxyness"))
	var fp [16]byte
	copy(fp[:], hash[:16])
	return fp
}
