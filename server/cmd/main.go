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
	"log"
	"math/big"
	"net"
	"os"
	"time"

	"smurov-proxy/server/internal/admin"
	"smurov-proxy/server/internal/db"
	"smurov-proxy/server/internal/mux"
	"smurov-proxy/server/internal/proxy"
	"smurov-proxy/server/internal/stats"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	dbPath := flag.String("db", "data.db", "SQLite database path")
	adminUser := flag.String("admin-user", "", "admin username (or ADMIN_USER env)")
	adminPass := flag.String("admin-password", "", "admin password (or ADMIN_PASSWORD env)")
	certFile := flag.String("cert", "cert.pem", "TLS certificate file")
	keyFile := flag.String("keyfile", "key.pem", "TLS private key file")
	flag.Parse()

	if *adminUser == "" {
		*adminUser = os.Getenv("ADMIN_USER")
	}
	if *adminPass == "" {
		*adminPass = os.Getenv("ADMIN_PASSWORD")
	}
	if *adminUser == "" || *adminPass == "" {
		log.Fatal("admin-user and admin-password are required (flags or ADMIN_USER/ADMIN_PASSWORD env)")
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	tracker := stats.New()

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

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	log.Printf("server listening on %s", *addr)

	adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass, "/data/downloads")

	proxyHandler := &proxy.Handler{DB: database, Tracker: tracker}
	m := mux.NewPreTLSMux(ln, tlsCfg,
		func(conn net.Conn, isTLS bool) { proxyHandler.Handle(conn, isTLS) },
		adminHandler,
	)
	m.Serve()
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
	certOut, _ := os.Create(certFile)
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()
	keyOut, _ := os.Create(keyFile)
	privKeyBytes, _ := x509.MarshalECPrivateKey(privKey)
	pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes})
	keyOut.Close()
	log.Printf("wrote %s and %s", certFile, keyFile)
	return nil
}
