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
	got := ExpandDomains([]string{"www.example.com"})
	want := []string{"www.example.com", "*.www.example.com"}
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
