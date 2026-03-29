package tun

// ProcessInfo looks up process information from network connections.
type ProcessInfo interface {
	// FindProcess returns the executable path for the process that owns
	// the given local TCP or UDP port. Returns empty string if not found.
	FindProcess(network string, localPort uint16) (path string, err error)
}
