package test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"proxyness/pkg/auth"
	"proxyness/pkg/proto"
)

// startTLSServer starts a proxy server on a random port for testing.
func startTLSServer(t *testing.T, key string) (addr string, cleanup func()) {
	t.Helper()

	privKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	tlsCert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  privKey,
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if err := proto.ReadAuth(c, key); err != nil {
					proto.WriteResult(c, false)
					return
				}
				proto.WriteResult(c, true)

				addr, port, err := proto.ReadConnect(c)
				if err != nil {
					return
				}
				target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", addr, port), 5*time.Second)
				if err != nil {
					proto.WriteResult(c, false)
					return
				}
				defer target.Close()
				proto.WriteResult(c, true)
				proto.Relay(c, target)
			}(conn)
		}
	}()

	return ln.Addr().String(), func() { ln.Close() }
}

func TestEndToEnd(t *testing.T) {
	key := auth.GenerateKey()

	// Start a target HTTP server
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from target"))
	}))
	defer target.Close()

	// Start proxy server
	proxyAddr, cleanup := startTLSServer(t, key)
	defer cleanup()

	// Connect to proxy via TLS
	tlsConn, err := tls.Dial("tcp", proxyAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer tlsConn.Close()

	// Auth
	if err := proto.WriteAuth(tlsConn, key); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	ok, err := proto.ReadResult(tlsConn)
	if err != nil || !ok {
		t.Fatalf("auth failed: ok=%v err=%v", ok, err)
	}

	// Parse target address
	host, portStr, _ := net.SplitHostPort(target.Listener.Addr().String())
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	// Connect
	if err := proto.WriteConnect(tlsConn, host, port); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	ok, err = proto.ReadResult(tlsConn)
	if err != nil || !ok {
		t.Fatalf("connect failed: ok=%v err=%v", ok, err)
	}

	// Send HTTP request through the tunnel
	fmt.Fprintf(tlsConn, "GET / HTTP/1.0\r\nHost: localhost\r\n\r\n")
	resp, err := io.ReadAll(tlsConn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(resp), "hello from target") {
		t.Fatalf("unexpected response: %s", string(resp))
	}
}

func TestEndToEnd_BadKey(t *testing.T) {
	key := auth.GenerateKey()
	wrongKey := auth.GenerateKey()

	proxyAddr, cleanup := startTLSServer(t, key)
	defer cleanup()

	tlsConn, err := tls.Dial("tcp", proxyAddr, &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("tls dial: %v", err)
	}
	defer tlsConn.Close()

	proto.WriteAuth(tlsConn, wrongKey)
	ok, err := proto.ReadResult(tlsConn)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if ok {
		t.Fatal("expected auth failure with wrong key")
	}
}
