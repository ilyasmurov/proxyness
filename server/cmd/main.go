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
	"os/signal"
	"syscall"
	"time"

	"proxyness/server/internal/admin"
	"proxyness/server/internal/db"
	"proxyness/server/internal/mux"
	"proxyness/server/internal/proxy"
	"proxyness/server/internal/stats"
	serverudp "proxyness/server/internal/udp"
)

func main() {
	addr := flag.String("addr", ":443", "listen address")
	adminUser := flag.String("admin-user", "", "admin username (or ADMIN_USER env)")
	adminPass := flag.String("admin-password", "", "admin password (or ADMIN_PASSWORD env)")
	certFile := flag.String("cert", "cert.pem", "TLS certificate file")
	keyFile := flag.String("keyfile", "key.pem", "TLS private key file")
	udpAddr := flag.String("udp-addr", ":8443", "UDP listen address (separate port to avoid TSPU blocks on UDP 443)")
	configAddr := flag.String("config", "", "config service address (default http://127.0.0.1:8443)")
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

	dbURL := os.Getenv("PROXYNESS_DB_URL")
	if dbURL == "" {
		log.Fatal("PROXYNESS_DB_URL env required (e.g. postgres://user:pw@10.88.0.1:5432/proxyness?sslmode=disable)")
	}

	database, err := db.Open(dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// Redirect log output to DB + stdout
	log.SetOutput(&db.DBWriter{DB: database})
	log.SetFlags(log.Ldate | log.Ltime)

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

	adminHandler := admin.NewHandler(database, tracker, *adminUser, *adminPass, *configAddr)

	// Start UDP listener on a separate port to avoid TSPU blocks on UDP 443
	udpConn, err := net.ListenPacket("udp", *udpAddr)
	if err != nil {
		log.Fatalf("udp listen: %v", err)
	}
	udpListener := serverudp.NewListener(udpConn, database, tracker)
	go udpListener.Serve()
	log.Printf("UDP listener started on %s", *udpAddr)

	proxyHandler := &proxy.Handler{DB: database, Tracker: tracker}
	m := mux.NewPreTLSMux(ln, tlsCfg,
		func(conn net.Conn, isTLS bool) { proxyHandler.Handle(conn, isTLS) },
		adminHandler,
	)

	// Graceful shutdown: on SIGTERM/SIGINT (what `docker stop` sends) we
	// broadcast a MsgSessionClose to every active UDP session so clients
	// reconnect immediately instead of waiting for the keepalive dead-ticker,
	// then close the listeners so the process exits cleanly.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		log.Printf("shutdown: received %s, broadcasting session close", sig)
		udpListener.BroadcastSessionClose()
		// Give UDP packets ~500ms to actually leave the kernel socket buffer
		// before we close the connection underneath them.
		time.Sleep(500 * time.Millisecond)
		ln.Close()
		udpConn.Close()
		log.Printf("shutdown: listeners closed, exiting")
		os.Exit(0)
	}()

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
		Subject:      pkix.Name{Organization: []string{"Proxyness"}},
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
