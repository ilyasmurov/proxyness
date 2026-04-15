package transport

import (
	"log"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// LogNetworkState dumps the kernel IPv4 routing table. Called from the
// slow-poll entry in tunnel.go / tun/engine.go so that the next time the
// daemon sits in ENETUNREACH we have enough post-mortem context to confirm
// whether ifscope bypass routes got flushed, the server host route is gone,
// or something else entirely is broken. Cheap on macOS (~5ms) and best-effort
// on Windows — failures are logged but not propagated.
func LogNetworkState(prefix string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("netstat", "-rn", "-f", "inet")
	case "windows":
		cmd = exec.Command("route", "print", "-4")
	default:
		return
	}
	// Hard cap in case the tool hangs — we're in the health loop, must not block.
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		log.Printf("%s routing table dump timed out", prefix)
		return
	}
	if err != nil {
		log.Printf("%s routing table dump failed: %v", prefix, err)
		return
	}
	log.Printf("%s routing table at slow-poll entry:\n%s", prefix, strings.TrimRight(string(out), "\n"))
}
