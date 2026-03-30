package main

import (
	"flag"
	"log"
	"net/http"

	"smurov-proxy/daemon/internal/api"
	dstats "smurov-proxy/daemon/internal/stats"
	"smurov-proxy/daemon/internal/tun"
	"smurov-proxy/daemon/internal/tunnel"
)

func main() {
	serverAddr := flag.String("server", "", "proxy server address (host:port)")
	key := flag.String("key", "", "shared secret key (hex)")
	listenAddr := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	apiAddr := flag.String("api", "127.0.0.1:9090", "HTTP API listen address")
	flag.Parse()

	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	tunEngine := tun.NewEngine(meter)

	if *serverAddr != "" && *key != "" {
		if err := tnl.Start(*listenAddr, *serverAddr, *key); err != nil {
			log.Fatalf("start tunnel: %v", err)
		}
		log.Printf("tunnel connected to %s, SOCKS5 on %s", *serverAddr, *listenAddr)
	}

	srv := api.New(tnl, tunEngine, *listenAddr, meter)
	log.Printf("API listening on %s", *apiAddr)
	if err := http.ListenAndServe(*apiAddr, srv.Handler()); err != nil {
		log.Fatalf("api: %v", err)
	}
}
