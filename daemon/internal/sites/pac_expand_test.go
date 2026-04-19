package sites

import (
	"reflect"
	"testing"
)

func TestExpandDomainsAddsWWWAndStarVariants(t *testing.T) {
	got := ExpandDomains([]string{"habr.com"})
	want := []string{"habr.com", "www.habr.com", "*.habr.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsSkipsWWWPrefixForExistingWWW(t *testing.T) {
	// "www.X" input keeps just that one entry — generating www.www.X is
	// redundant and *.www.X never matches anything real.
	got := ExpandDomains([]string{"www.example.com"})
	want := []string{"www.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsNormalizesStarPrefixToBase(t *testing.T) {
	// "*.X" input is the same as just "X" — the literal "*." variant is
	// dead in PAC (no real hostname starts with "*"), so treat it as the
	// user's intent "proxy everything under X".
	got := ExpandDomains([]string{"*.adguard.com"})
	want := []string{"adguard.com", "www.adguard.com", "*.adguard.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsDedupsStarAndBareInput(t *testing.T) {
	// Daemon cache can legitimately have both "example.com" and
	// "*.example.com" for the same site; after normalization they must
	// collapse to a single set of expansions.
	got := ExpandDomains([]string{"*.example.com", "example.com"})
	want := []string{"example.com", "www.example.com", "*.example.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsDeduplicatesAcrossInputs(t *testing.T) {
	got := ExpandDomains([]string{"a.com", "a.com", "B.COM"})
	want := []string{"a.com", "www.a.com", "*.a.com", "b.com", "www.b.com", "*.b.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsTrimsWhitespaceAndIgnoresEmpty(t *testing.T) {
	got := ExpandDomains([]string{"  a.com  ", "", "   "})
	want := []string{"a.com", "www.a.com", "*.a.com"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExpandDomainsEmptyInput(t *testing.T) {
	got := ExpandDomains(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}
