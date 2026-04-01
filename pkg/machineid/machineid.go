package machineid

import "crypto/sha256"

// Fingerprint returns a 16-byte hardware-based machine fingerprint.
// SHA256(hardware_id + "smurov-proxy"), first 16 bytes.
// Stable across reboots, network changes, VPN on/off.
func Fingerprint() [16]byte {
	id := hardwareID()
	if id == "" {
		id = "unknown"
	}
	hash := sha256.Sum256([]byte(id + "smurov-proxy"))
	var fp [16]byte
	copy(fp[:], hash[:16])
	return fp
}
