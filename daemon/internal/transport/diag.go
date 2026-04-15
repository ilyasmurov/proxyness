package transport

import (
	"errors"
	"log"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// LogNetworkState dumps the kernel IPv4 routing table. Called once on slow-poll
// entry so that the next time the daemon sits in ENETUNREACH we have enough
// post-mortem context to confirm whether ifscope bypass routes got flushed,
// the server host route is gone, or something else entirely is broken. Cheap
// on macOS (~5ms) and best-effort on Windows — failures are logged but not
// propagated. Paired with LogNetworkDiagnostics (per-tick, lighter snapshot).
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
	out, err := runCapped(cmd, 3*time.Second)
	if err != nil {
		log.Printf("%s routing table dump failed: %v", prefix, err)
		return
	}
	log.Printf("%s routing table at slow-poll entry:\n%s", prefix, strings.TrimRight(string(out), "\n"))
}

// LogNetworkDiagnostics dumps fast-moving network state (ARP cache and the
// kernel's resolved route to serverAddr) on every slow-poll tick. We learned
// from a real incident that ENETUNREACH without interface changes is caused
// by Docker Desktop's vmnetd re-creating a virtual ethN — macOS configd then
// fires "network changed" 4× in 1 second, invalidating the ARP entry for the
// physical gateway. Without a per-tick snapshot we can't tell whether the
// gateway ARP came back, whether we're using the wrong interface, or whether
// the route to the server was replaced. The routing table itself rarely
// changes across ticks, so LogNetworkState stays one-shot on entry.
//
// serverAddr is "host:port" (what t.serverAddr / e.serverAddr hold); the
// port is stripped for `route get`. An empty or malformed serverAddr skips
// the route-get step rather than failing the whole dump.
func LogNetworkDiagnostics(prefix, serverAddr string) {
	host := ""
	if serverAddr != "" {
		if h, _, err := net.SplitHostPort(serverAddr); err == nil {
			host = h
		} else {
			// Fall back to the raw value — may already be a bare host.
			host = serverAddr
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if out, err := runCapped(exec.Command("arp", "-a", "-n"), 2*time.Second); err == nil {
			log.Printf("%s arp cache:\n%s", prefix, strings.TrimRight(string(out), "\n"))
		} else {
			log.Printf("%s arp cache dump failed: %v", prefix, err)
		}
		if host != "" {
			if out, err := runCapped(exec.Command("route", "-n", "get", host), 2*time.Second); err == nil {
				log.Printf("%s route get %s:\n%s", prefix, host, strings.TrimRight(string(out), "\n"))
			} else {
				log.Printf("%s route get %s failed: %v", prefix, host, err)
			}
		}
	case "windows":
		if out, err := runCapped(exec.Command("arp", "-a"), 2*time.Second); err == nil {
			log.Printf("%s arp cache:\n%s", prefix, strings.TrimRight(string(out), "\n"))
		} else {
			log.Printf("%s arp cache dump failed: %v", prefix, err)
		}
		// Windows has no direct `route get <ip>` equivalent; route PRINT is
		// already covered by LogNetworkState, and PowerShell's Find-NetRoute
		// is too slow for a per-tick dump. Skip.
	}
}

// runCapped executes cmd and returns its combined output, killing the process
// if it exceeds timeout. We're in the health loop and must never block.
func runCapped(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return nil, errDiagTimeout
	}
}

var errDiagTimeout = errors.New("diagnostic command timed out")
