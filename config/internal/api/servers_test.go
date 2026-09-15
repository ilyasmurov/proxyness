package api

import "testing"

func TestParseServersAcceptsAValidList(t *testing.T) {
	got, err := parseServers(` [{"id":"aeza","label":"Aeza NL","addr":"178.236.252.28:443"},
	  {"id":"aeza-ru","label":" Aeza via RU ","addr":"157.22.194.55:4443"}] `)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[1].Label != "Aeza via RU" || got[0].Addr != "178.236.252.28:443" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestParseServersEmptyMeansNoOverride(t *testing.T) {
	for _, in := range []string{"", "  ", "[]", "null"} {
		got, err := parseServers(in)
		if err != nil || got != nil {
			t.Fatalf("%q: want nil,nil got %v,%v", in, got, err)
		}
	}
}

func TestParseServersRejectsBadEntries(t *testing.T) {
	bad := []string{
		`{"id":"x"}`, // not an array
		`[{"id":"","label":"a","addr":"1.2.3.4:443"}]`,                                              // blank id
		`[{"id":"a","label":"a","addr":"1.2.3.4"}]`,                                                 // no port
		`[{"id":"a","label":"a","addr":"1.2.3.4:99999"}]`,                                           // bad port
		`[{"id":"a","label":"a","addr":"1.2.3.4:443"},{"id":"a","label":"b","addr":"5.6.7.8:443"}]`, // dup id
		`[{"id":"a","label":"a","addr":"1.2.3.4:443"},{"id":"b","label":"b","addr":"1.2.3.4:443"}]`, // dup addr
	}
	for _, in := range bad {
		if _, err := parseServers(in); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}
