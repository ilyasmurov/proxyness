package tun

import "testing"

func TestRulesProxyAll(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyAllExcept)
	r.SetApps([]string{"/usr/bin/curl"})

	// Browsers always bypass TUN (they use SOCKS5/PAC)
	if r.ShouldProxy("/usr/bin/firefox") {
		t.Error("firefox (browser) should bypass TUN")
	}
	if !r.ShouldProxy("/usr/bin/some-app") {
		t.Error("non-browser app should be proxied in proxy-all mode")
	}
	if r.ShouldProxy("/usr/bin/curl") {
		t.Error("curl should be excluded")
	}
	if !r.ShouldProxy("unknown-app") {
		t.Error("unknown should be proxied")
	}
}

func TestRulesProxyOnly(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyOnly)
	r.SetApps([]string{"/Applications/Telegram.app", "/Applications/Discord.app"})

	if !r.ShouldProxy("/Applications/Telegram.app") {
		t.Error("telegram should be proxied")
	}
	if !r.ShouldProxy("/Applications/Discord.app") {
		t.Error("discord should be proxied")
	}
	if r.ShouldProxy("/usr/bin/curl") {
		t.Error("curl should not be proxied")
	}
}

func TestRulesDefaultProxyAll(t *testing.T) {
	r := NewRules()
	if !r.ShouldProxy("anything") {
		t.Error("default mode should proxy everything")
	}
}

func TestRulesJSON(t *testing.T) {
	r := NewRules()
	r.SetMode(ModeProxyOnly)
	r.SetApps([]string{"app1", "app2"})

	data := r.ToJSON()
	r2 := NewRules()
	if err := r2.FromJSON(data); err != nil {
		t.Fatalf("from json: %v", err)
	}
	if r2.GetMode() != ModeProxyOnly {
		t.Error("mode mismatch")
	}
	if !r2.ShouldProxy("app1") {
		t.Error("app1 should be proxied")
	}
}
