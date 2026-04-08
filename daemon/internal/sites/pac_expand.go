package sites

import "strings"

// ExpandDomains takes a list of primary site domains and returns the
// flat list that goes into the PAC file. For each input domain it adds
// "www." and "*." variants because the PAC matches by suffix.
//
// Mirrors the previous client-side implementation in
// client/src/renderer/sites/pac.ts so the daemon can take ownership of
// PAC formation.
func ExpandDomains(domains []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(domains)*3)
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, d := range domains {
		clean := strings.ToLower(strings.TrimSpace(d))
		if clean == "" {
			continue
		}
		add(clean)
		if !strings.HasPrefix(clean, "www.") {
			add("www." + clean)
		}
		add("*." + clean)
	}
	return out
}
