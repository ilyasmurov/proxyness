package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"time"

	"smurov-proxy/pkg/proto"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	key := flag.String("key", "", "shared secret key (hex)")
	certFile := flag.String("cert", "cert.pem", "TLS certificate file")
	keyFile := flag.String("keyfile", "key.pem", "TLS private key file")
	flag.Parse()

	if *key == "" {
		log.Fatal("-key is required")
	}

	if err := ensureCert(*certFile, *keyFile); err != nil {
		log.Fatalf("cert: %v", err)
	}

	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("load cert: %v", err)
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}

	ln, err := tls.Listen("tcp", *addr, tlsCfg)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("server listening on %s", *addr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, *key)
	}
}

func handleConn(conn net.Conn, key string) {
	defer conn.Close()

	// Phase 1: Auth
	if err := proto.ReadAuth(conn, key); err != nil {
		proto.WriteResult(conn, false)
		log.Printf("auth failed from %s: %v", conn.RemoteAddr(), err)
		return
	}
	proto.WriteResult(conn, true)

	// Phase 2: Connect
	addr, port, err := proto.ReadConnect(conn)
	if err != nil {
		log.Printf("connect read: %v", err)
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", addr, port), 10*time.Second)
	if err != nil {
		proto.WriteResult(conn, false)
		log.Printf("dial %s:%d: %v", addr, port, err)
		return
	}
	defer target.Close()
	proto.WriteResult(conn, true)

	// Phase 3: Relay
	proto.Relay(conn, target)
}

func ensureCert(certFile, keyFile string) error {
	if _, err := os.Stat(certFile); err == nil {
		return nil
	}

	log.Println("generating self-signed TLS certificate...")

	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"SmurovProxy"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return err
	}

	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()

	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return err
	}
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes})
	keyOut.Close()

	log.Printf("wrote %s and %s", certFile, keyFile)
	return nil
}
