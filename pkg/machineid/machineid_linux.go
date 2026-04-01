//go:build linux

package machineid

import (
	"os"
	"strings"
)

func hardwareID() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
