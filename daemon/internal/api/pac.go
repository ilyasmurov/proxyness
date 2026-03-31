package api

import (
	"fmt"
	"strings"
	"sync"
)

type PacSites struct {
	mu           sync.RWMutex
	proxyAll     bool
	sites        []string
	forceProxAll bool // when TUN is active, DIRECT doesn't work
}

func NewPacSites() *PacSites {
	return &PacSites{proxyAll: true}
}

func (p *PacSites) Set(proxyAll bool, sites []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxyAll = proxyAll
	p.sites = sites
}

func (p *PacSites) SetForceProxyAll(v bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.forceProxAll = v
}

func (p *PacSites) Get() (bool, []string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.proxyAll, append([]string{}, p.sites...)
}

func (p *PacSites) GeneratePAC() string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	b.WriteString(`  if (host === "127.0.0.1" || host === "localhost") return "DIRECT";` + "\n")

	proxy := `"SOCKS5 127.0.0.1:1080; SOCKS 127.0.0.1:1080; DIRECT"`

	// When TUN is active, DIRECT doesn't work (TUN routes break it),
	// so all browser traffic must go through SOCKS5.
	if p.forceProxAll || p.proxyAll {
		b.WriteString(fmt.Sprintf("  return %s;\n", proxy))
	} else if len(p.sites) == 0 {
		b.WriteString(`  return "DIRECT";` + "\n")
	} else {
		for _, site := range p.sites {
			site = strings.TrimSpace(site)
			if site == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("  if (host === %q || dnsDomainIs(host, %q)) return %s;\n",
				site, "."+site, proxy))
		}
		b.WriteString(`  return "DIRECT";` + "\n")
	}

	b.WriteString("}")
	return b.String()
}
