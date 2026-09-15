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

func TestParseServersKindsAndVia(t *testing.T) {
	got, err := parseServers(`[{"id":"aeza","label":"Aeza","addr":"1.2.3.4:443"},
	  {"id":"ru","label":"via RU","addr":"5.6.7.8:4443","kind":"bridge","via":"aeza"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0].Kind != "exit" || got[1].Kind != "bridge" || got[1].Via != "aeza" {
		t.Fatalf("kinds not normalised: %+v", got)
	}
	bad := []string{
		`[{"id":"a","label":"a","addr":"1.2.3.4:443","kind":"relay"}]`,                                                                                  // unknown kind
		`[{"id":"a","label":"a","addr":"1.2.3.4:443","kind":"bridge"}]`,                                                                                 // bridge without via
		`[{"id":"a","label":"a","addr":"1.2.3.4:443","kind":"bridge","via":"nope"}]`,                                                                    // via unknown
		`[{"id":"a","label":"a","addr":"1.2.3.4:443","via":"a"}]`,                                                                                       // exit with via
		`[{"id":"a","label":"a","addr":"1.2.3.4:443","kind":"bridge","via":"b"},{"id":"b","label":"b","addr":"1.2.3.5:443","kind":"bridge","via":"a"}]`, // bridge → bridge
	}
	for _, in := range bad {
		if _, err := parseServers(in); err == nil {
			t.Errorf("expected error for %s", in)
		}
	}
}

func TestParseHosts(t *testing.T) {
	m, err := parseHosts(`{"157.22.194.55":{"label":" FirstVDS · Москва "},"178.236.252.28":{"label":"Aeza","extras":["AmneziaWG · 13337/udp",""]}}`)
	if err != nil || len(m) != 2 || m["157.22.194.55"].Label != "FirstVDS · Москва" || len(m["178.236.252.28"].Extras) != 1 {
		t.Fatalf("unexpected: %+v %v", m, err)
	}
	if m2, err := parseHosts(""); err != nil || m2 != nil {
		t.Fatalf("empty must be nil,nil: %v %v", m2, err)
	}
	for _, bad := range []string{`[]`, `{"1.2.3.4":{"label":""}}`, `{"":{"label":"x"}}`} {
		if _, err := parseHosts(bad); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}
