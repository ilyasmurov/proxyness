package tun

import (
	"encoding/binary"
	"testing"
)

// buildClientHello constructs a minimal TLS 1.2 ClientHello record with a
// server_name extension naming host. Enough bytes to drive parseSNI — not
// a wire-valid handshake for any actual TLS stack.
func buildClientHello(host string) []byte {
	// SNI extension body: list_len(2) + name_type(1) + name_len(2) + name
	sni := make([]byte, 0, 5+len(host))
	sni = append(sni, 0, 0)                          // list_len placeholder
	sni = append(sni, 0x00)                          // name_type = host_name
	sni = binary.BigEndian.AppendUint16(sni, uint16(len(host)))
	sni = append(sni, []byte(host)...)
	binary.BigEndian.PutUint16(sni[0:2], uint16(len(sni)-2))

	// Extension entry: type(2) + size(2) + body
	ext := make([]byte, 0, 4+len(sni))
	ext = binary.BigEndian.AppendUint16(ext, 0x0000) // server_name
	ext = binary.BigEndian.AppendUint16(ext, uint16(len(sni)))
	ext = append(ext, sni...)

	// extensions block: length(2) + body
	extsLen := len(ext)

	// ClientHello body: version(2) + random(32) + session_id_len(1) +
	//   cipher_suites_len(2) + cipher_suites(2) + compression_methods_len(1) +
	//   compression_methods(1) + extensions_len(2) + extensions
	body := make([]byte, 0, 44+extsLen)
	body = append(body, 0x03, 0x03) // TLS 1.2
	body = append(body, make([]byte, 32)...)
	body = append(body, 0x00)                     // session_id_len = 0
	body = binary.BigEndian.AppendUint16(body, 2) // cipher_suites_len
	body = append(body, 0xC0, 0x2F)               // one cipher suite
	body = append(body, 0x01, 0x00)               // compression_methods: len=1, null
	body = binary.BigEndian.AppendUint16(body, uint16(extsLen))
	body = append(body, ext...)

	// Handshake header: type(1) + length(3) + body
	hs := []byte{0x01, 0x00, 0x00, 0x00}
	binary.BigEndian.PutUint32(hs, uint32(len(body))) // writes full 4 bytes — overwrites type
	hs[0] = 0x01                                      // restore type = ClientHello

	hsFull := append(hs, body...)

	// TLS record: type(1) + version(2) + length(2) + handshake
	rec := []byte{0x16, 0x03, 0x01}
	rec = binary.BigEndian.AppendUint16(rec, uint16(len(hsFull)))
	rec = append(rec, hsFull...)
	return rec
}

func TestParseSNI_RecognizesHost(t *testing.T) {
	for _, host := range []string{"api.anthropic.com", "api2.cursor.sh", "www.figma.com"} {
		pkt := buildClientHello(host)
		got, ready := parseSNI(pkt)
		if !ready {
			t.Errorf("host=%s: parseSNI returned ready=false on a full ClientHello", host)
			continue
		}
		if got != host {
			t.Errorf("host=%s: parseSNI returned %q", host, got)
		}
	}
}

func TestParseSNI_NeedsMoreBytes(t *testing.T) {
	pkt := buildClientHello("api.anthropic.com")
	// Partial record: strip the SNI extension off the end so we know
	// len(pkt) > than what's available yet.
	partial := pkt[:len(pkt)-6]
	if _, ready := parseSNI(partial); ready {
		t.Errorf("partial ClientHello should have parser asking for more bytes")
	}
}

func TestParseSNI_NonTLS(t *testing.T) {
	// GET / HTTP/1.1\r\n — first byte is 0x47 ('G'), not 0x16.
	got, ready := parseSNI([]byte("GET / HTTP/1.1\r\n"))
	if !ready || got != "" {
		t.Errorf("non-TLS traffic: expected ready=true host=\"\", got ready=%v host=%q", ready, got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0B"},
		{512, "512B"},
		{1024, "1.0KB"},
		{1500, "1.5KB"},
		{2 * 1024 * 1024, "2.0MB"},
		{2_200_000, "2.1MB"},
		{3 * 1024 * 1024 * 1024, "3.00GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.in); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
