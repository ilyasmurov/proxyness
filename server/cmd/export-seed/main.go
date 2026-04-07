package main

import (
	"encoding/json"
	"fmt"
	"os"

	"smurov-proxy/server/internal/db"
)

// Shape emitted to client/resources/seed_sites.json. Fields match what
// the client sync module expects as its initial bootstrap state.
type seedJSON struct {
	ID            int      `json:"id"`
	Slug          string   `json:"slug"`
	Label         string   `json:"label"`
	PrimaryDomain string   `json:"primary_domain"`
	Domains       []string `json:"domains"`
	IPs           []string `json:"ips"`
}

func main() {
	entries := db.ExportSeedSites()
	out := make([]seedJSON, 0, len(entries))
	for i, s := range entries {
		domains := append([]string{s.PrimaryDomain}, s.Domains...)
		ips := s.IPs
		if ips == nil {
			ips = []string{}
		}
		out = append(out, seedJSON{
			ID:            i + 1,
			Slug:          s.Slug,
			Label:         s.Label,
			PrimaryDomain: s.PrimaryDomain,
			Domains:       domains,
			IPs:           ips,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}
}
