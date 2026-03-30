package tunnel

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/pkg/auth"
	"smurov-proxy/pkg/proto"
)

const testKey = "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd"

// startMockServer starts a TLS server that accepts auth and responds OK.
func startMockServer(t *testing.T) string {
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
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				msg := make([]byte, auth.AuthMsgLen)
				if _, err := c.Read(msg); err != nil {
					return
				}
				proto.WriteResult(c, true)
			}(conn)
		}
	}()

	return ln.Addr().String()
}

func TestNew(t *testing.T) {
	tun := New(dstats.NewRateMeter())
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
	if tun.Uptime() != 0 {
		t.Fatalf("expected 0 uptime, got %d", tun.Uptime())
	}
}

func TestStartStop(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())

	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if tun.GetStatus() != Connected {
		t.Fatalf("expected connected, got %s", tun.GetStatus())
	}

	tun.Stop()
	if tun.GetStatus() != Disconnected {
		t.Fatalf("expected disconnected, got %s", tun.GetStatus())
	}
}

func TestDoubleStart(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())

	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	err = tun.Start("127.0.0.1:0", addr, testKey)
	if err == nil {
		t.Fatal("expected error on double start")
	}
}

func TestUptime(t *testing.T) {
	addr := startMockServer(t)
	tun := New(dstats.NewRateMeter())
	err := tun.Start("127.0.0.1:0", addr, testKey)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Stop()

	if tun.Uptime() < 0 {
		t.Fatal("uptime should be >= 0")
	}
}
