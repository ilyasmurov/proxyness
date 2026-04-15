package tun

import (
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"sync"
	"time"
)

// longStreamThreshold — streams that live at least this long are worth
// logging. Everything shorter is background chatter (figma/cursor/slack
// housekeeping HTTPS) and would drown the signal.
const longStreamThreshold = 10 * time.Second

// sniSniffer wraps a net.Conn and passively records the SNI hostname out of
// the first TLS ClientHello read off it. Non-TLS streams or ones we can't
// parse end up with sni="" — that's fine, IP:port + proc name is enough
// for post-mortem correlation.
type sniSniffer struct {
	net.Conn

	mu   sync.Mutex
	buf  []byte
	sni  string
	done bool
}

func newSNISniffer(c net.Conn) *sniSniffer { return &sniSniffer{Conn: c} }

func (s *sniSniffer) Read(p []byte) (int, error) {
	n, err := s.Conn.Read(p)
	if n > 0 {
		s.mu.Lock()
		if !s.done {
			if len(s.buf)+n <= 2048 {
				s.buf = append(s.buf, p[:n]...)
				if h, ready := parseSNI(s.buf); ready {
					s.sni = h
					s.done = true
					s.buf = nil
				}
			} else {
				s.done = true
				s.buf = nil
			}
		}
		s.mu.Unlock()
	}
	return n, err
}

func (s *sniSniffer) Host() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sni
}

// parseSNI extracts the hostname from a TLS ClientHello. Returns
// (host, true) once we have a definitive answer (either "" meaning
// non-TLS / no SNI, or the actual host); (_, false) means we need
// more bytes to decide. All length checks are defensive — malformed
// input yields a definitive empty result rather than a panic.
func parseSNI(b []byte) (string, bool) {
	if len(b) < 5 {
		return "", false
	}
	if b[0] != 0x16 { // TLS handshake
		return "", true
	}
	recLen := int(binary.BigEndian.Uint16(b[3:5]))
	if len(b) < 5+recLen {
		return "", false
	}
	p := b[5 : 5+recLen]
	if len(p) < 4 || p[0] != 0x01 { // ClientHello
		return "", true
	}
	p = p[4:]
	if len(p) < 34 {
		return "", true
	}
	p = p[34:]
	if len(p) < 1 {
		return "", true
	}
	sidLen := int(p[0])
	if len(p) < 1+sidLen {
		return "", true
	}
	p = p[1+sidLen:]
	if len(p) < 2 {
		return "", true
	}
	csLen := int(binary.BigEndian.Uint16(p[:2]))
	if len(p) < 2+csLen {
		return "", true
	}
	p = p[2+csLen:]
	if len(p) < 1 {
		return "", true
	}
	cmLen := int(p[0])
	if len(p) < 1+cmLen {
		return "", true
	}
	p = p[1+cmLen:]
	if len(p) < 2 {
		return "", true
	}
	extLen := int(binary.BigEndian.Uint16(p[:2]))
	if len(p) < 2+extLen {
		return "", true
	}
	p = p[2 : 2+extLen]
	for len(p) >= 4 {
		extType := binary.BigEndian.Uint16(p[:2])
		extSize := int(binary.BigEndian.Uint16(p[2:4]))
		if len(p) < 4+extSize {
			return "", true
		}
		ext := p[4 : 4+extSize]
		p = p[4+extSize:]
		if extType == 0x0000 {
			if len(ext) < 2 {
				return "", true
			}
			listLen := int(binary.BigEndian.Uint16(ext[:2]))
			if len(ext) < 2+listLen {
				return "", true
			}
			lst := ext[2 : 2+listLen]
			for len(lst) >= 3 {
				nameType := lst[0]
				nameLen := int(binary.BigEndian.Uint16(lst[1:3]))
				if len(lst) < 3+nameLen {
					return "", true
				}
				if nameType == 0 {
					return string(lst[3 : 3+nameLen]), true
				}
				lst = lst[3+nameLen:]
			}
		}
	}
	return "", true
}

// formatBytes renders a byte count as a short human-readable string:
// "523B", "12.4KB", "2.1MB", "1.2GB".
func formatBytes(n int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case n < kb:
		return fmt.Sprintf("%dB", n)
	case n < mb:
		return fmt.Sprintf("%.1fKB", float64(n)/kb)
	case n < gb:
		return fmt.Sprintf("%.1fMB", float64(n)/mb)
	default:
		return fmt.Sprintf("%.2fGB", float64(n)/gb)
	}
}

// logTCPClose prints a post-mortem for a TUN-proxied TCP stream if it
// lived long enough to be interesting. Output is one line and easy to
// grep: `[tun] tcp close: 1.2.3.4:443 host=api.anthropic.com proc=node
// dur=3m12s up=12.4KB down=2.1MB reason="download: EOF"`.
func logTCPClose(dstAddr string, dstPort uint16, sni, appPath string, dur time.Duration, up, down int64, reason string) {
	if dur < longStreamThreshold {
		return
	}
	host := sni
	if host == "" {
		host = "-"
	}
	proc := "-"
	if appPath != "" {
		proc = filepath.Base(appPath)
	}
	log.Printf("[tun] tcp close: %s:%d host=%s proc=%s dur=%s up=%s down=%s reason=%q",
		dstAddr, dstPort, host, proc, dur.Truncate(time.Second), formatBytes(up), formatBytes(down), reason)
}
