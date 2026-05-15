//go:build darwin

package machineid

import (
	"context"
	"log"
	"os/exec"
	"strings"
	"time"
)

// hardwareID polls `ioreg` until it returns a non-empty IOPlatformUUID, or
// the budget runs out. On a normally-running Mac the first call succeeds in
// a few milliseconds; the retry loop exists because immediately after wake
// from sleep IOKit can briefly produce output without IOPlatformUUID, which
// previously caused us to fall back to the "unknown" fingerprint and get
// rejected by the server with "machine id rejected".
//
// Total budget ~5s (25 × 200ms). On failure returns "" and the caller falls
// back to "unknown".
func hardwareID() string {
	deadline := time.Now().Add(5 * time.Second)
	attempt := 0
	for {
		attempt++
		if id := readIOPlatformUUID(); id != "" {
			if attempt > 1 {
				log.Printf("[machineid] IOPlatformUUID resolved on attempt %d", attempt)
			}
			return id
		}
		if time.Now().After(deadline) {
			log.Printf("[machineid] IOPlatformUUID still empty after %d attempts (~5s) — falling back to 'unknown'", attempt)
			return ""
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func readIOPlatformUUID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "IOPlatformUUID") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				return strings.Trim(strings.TrimSpace(parts[1]), "\"")
			}
		}
	}
	return ""
}
