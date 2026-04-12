package main

import (
	"flag"
	"log"
	"net/http"

	"proxyness/config/internal/api"
	"proxyness/config/internal/db"
	"proxyness/config/internal/poller"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	dbPath := flag.String("db", "config.db", "SQLite database path")
	adminUser := flag.String("admin-user", "", "admin username")
	adminPass := flag.String("admin-pass", "", "admin password")
	proxyAddr := flag.String("proxy", "http://proxyness:443", "proxy server internal address for key validation")
	githubRepo := flag.String("github-repo", "ilyasmurov/proxyness", "GitHub repo for version check")
	flag.Parse()

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer d.Close()

	srv := api.New(d, *adminUser, *adminPass, *proxyAddr)

	go poller.Start(d, *githubRepo)

	log.Printf("[config] listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
