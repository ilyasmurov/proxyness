package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"golang.org/x/net/proxy"
	"proxyness/daemon/internal/api"
	dstats "proxyness/daemon/internal/stats"
	"proxyness/daemon/internal/sites"
	"proxyness/daemon/internal/tun"
	"proxyness/daemon/internal/tunnel"
)

func main() {
	// Cap Go heap at 512MB — GC runs more aggressively near the limit
	debug.SetMemoryLimit(512 * 1024 * 1024)
	serverAddr := flag.String("server", "", "proxy server address (host:port)")
	key := flag.String("key", "", "shared secret key (hex)")
	listenAddr := flag.String("listen", "127.0.0.1:1080", "SOCKS5 listen address")
	apiAddr := flag.String("api", "127.0.0.1:9090", "HTTP API listen address")
	flag.Parse()

	meter := dstats.NewRateMeter()
	tnl := tunnel.New(meter)
	tunEngine := tun.NewEngine(meter)
	tnl.SetRouteRefresher(tunEngine.RefreshRoutes)

	if *serverAddr != "" && *key != "" {
		if err := tnl.Start(*listenAddr, *serverAddr, *key); err != nil {
			log.Fatalf("start tunnel: %v", err)
		}
		log.Printf("tunnel connected to %s, SOCKS5 on %s", *serverAddr, *listenAddr)
	}

	srv := api.New(tnl, tunEngine, *listenAddr, meter)
	keyStore := sites.NewKeyStore(sites.DefaultKeyPath())
	srv.SetKeyStore(keyStore)
	tokenStore := sites.NewTokenStore(sites.DefaultTokenPath())
	if _, err := tokenStore.GetOrCreate(); err != nil {
		log.Fatalf("daemon token: %v", err)
	}
	sitesManager := sites.NewManager("https://proxyness.smurov.com", keyStore)
	sitesManager.StartBackgroundRefresh(5 * time.Minute)
	srv.SetSites(sitesManager, tokenStore)
	// Wire RebuildPAC into the cache-replace callback so that any change
	// to the cache (background refresh, mutation through extension API,
	// or mutation through desktop client UI) automatically rebuilds PAC.
	sitesManager.SetOnCacheReplaced(srv.RebuildPAC)

	socksDialer, err := proxy.SOCKS5("tcp", *listenAddr, nil, proxy.Direct)
	if err != nil {
		log.Fatalf("socks5 dialer: %v", err)
	}
	testClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return socksDialer.Dial(network, addr)
			},
		},
	}
	srv.SetSitesTestClient(testClient)

	// Best-effort first refresh — fine to fail if offline.
	go func() {
		if err := sitesManager.Refresh(); err != nil {
			log.Printf("[sites] initial refresh: %v", err)
		}
	}()

	log.Printf("API listening on %s", *apiAddr)
	if err := http.ListenAndServe(*apiAddr, srv.Handler()); err != nil {
		log.Fatalf("api: %v", err)
	}
}
