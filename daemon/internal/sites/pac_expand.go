package sites

import "strings"

// ExpandDomains takes a list of primary site domains and returns the
// flat list that goes into the PAC file. For each input domain it adds
// "www." and "*." variants so both the apex and subdomains are matched
// by GeneratePAC's `host === X || dnsDomainIs(host, ".X")` check.
//
// Normalization rules:
//   - Leading "*." is stripped. A PAC entry with a literal "*" in it is
//     always dead: the JS comparison "host === \"*.X\"" never matches a
//     real hostname, and dnsDomainIs against ".*.X" can't match either
//     (no DNS label starts with "*"). Treat "*.X" as just "X" so the
//     user's intent (proxy everything under X) is actually honoured.
//   - If the cleaned domain already starts with "www.", skip generating
//     both the redundant "www.www.X" AND the dead "*.www.X" variant.
//
// Mirrors (and fixes) the previous client-side implementation in
// client/src/renderer/sites/pac.ts.
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
		clean = strings.TrimPrefix(clean, "*.")
		if clean == "" {
			continue
		}
		add(clean)
		if strings.HasPrefix(clean, "www.") {
			continue
		}
		add("www." + clean)
		add("*." + clean)
	}
	return out
}
