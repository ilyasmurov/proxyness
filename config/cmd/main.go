package main

import (
	"flag"
	"log"
	"net/http"

	"smurov-proxy/config/internal/api"
	"smurov-proxy/config/internal/db"
	"smurov-proxy/config/internal/poller"
)

func main() {
	addr := flag.String("addr", ":8443", "listen address")
	dbPath := flag.String("db", "config.db", "SQLite database path")
	adminUser := flag.String("admin-user", "", "admin username")
	adminPass := flag.String("admin-pass", "", "admin password")
	proxyAddr := flag.String("proxy", "http://smurov-proxy:443", "proxy server internal address for key validation")
	githubRepo := flag.String("github-repo", "ilyasmurov/smurov-proxy", "GitHub repo for version check")
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
