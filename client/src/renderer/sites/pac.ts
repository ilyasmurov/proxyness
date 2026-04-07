// expandDomains generates the flat domain list sent to the daemon's
// /pac/sites endpoint. For each input domain we also add "www." and
// "*." variants because the PAC file in the daemon matches by suffix.
//
// Before the sites catalog refactor, this lived inside AppRules.tsx
// as a useCallback and relied on a hardcoded RELATED_DOMAINS map. Now
// related domains arrive already joined from the server via
// LocalSite.domains, so this function just does the www/* expansion.
export function expandDomains(domains: string[]): string[] {
  const out = new Set<string>();
  for (const d of domains) {
    if (!d) continue;
    const clean = d.trim().toLowerCase();
    if (!clean) continue;
    out.add(clean);
    if (!clean.startsWith("www.")) {
      out.add("www." + clean);
    }
    out.add("*." + clean);
  }
  return [...out];
}
