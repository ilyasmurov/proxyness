package tun

import (
	"encoding/json"
	"strings"
	"sync"
)

type Mode string

const (
	ModeProxyAllExcept Mode = "proxy_all_except"
	ModeProxyOnly      Mode = "proxy_only"
)

type Rules struct {
	mu        sync.RWMutex
	mode      Mode
	apps      map[string]bool
	noTLSApps map[string]bool
}

type rulesJSON struct {
	Mode      Mode     `json:"mode"`
	Apps      []string `json:"apps"`
	NoTLSApps []string `json:"no_tls_apps,omitempty"`
}

func NewRules() *Rules {
	return &Rules{
		mode:      ModeProxyAllExcept,
		apps:      make(map[string]bool),
		noTLSApps: make(map[string]bool),
	}
}

func (r *Rules) SetMode(m Mode) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mode = m
}

func (r *Rules) GetMode() Mode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mode
}

func (r *Rules) SetApps(apps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.apps = make(map[string]bool, len(apps))
	for _, a := range apps {
		r.apps[strings.ToLower(a)] = true
	}
}

func (r *Rules) GetApps() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]string, 0, len(r.apps))
	for a := range r.apps {
		apps = append(apps, a)
	}
	return apps
}

func (r *Rules) SetNoTLSApps(apps []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.noTLSApps = make(map[string]bool, len(apps))
	for _, a := range apps {
		r.noTLSApps[strings.ToLower(a)] = true
	}
}

func (r *Rules) ShouldUseTLS(appPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	lower := strings.ToLower(appPath)
	for app := range r.noTLSApps {
		if lower == app || strings.HasPrefix(lower, app+"/") || strings.HasPrefix(lower, app+"\\") {
			return false
		}
	}
	return true
}

// NeedProcessLookup returns false when we can skip the expensive process
// identification (e.g. proxy_all_except with empty exclusion list).
func (r *Rules) NeedProcessLookup() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.mode == ModeProxyAllExcept && len(r.apps) == 0 {
		return false
	}
	return true
}

func (r *Rules) ShouldProxy(appPath string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(appPath)
	inList := false
	for app := range r.apps {
		if lower == app || strings.HasPrefix(lower, app+"/") || strings.HasPrefix(lower, app+"\\") {
			inList = true
			break
		}
	}

	switch r.mode {
	case ModeProxyAllExcept:
		return !inList
	case ModeProxyOnly:
		return inList
	default:
		return true
	}
}

func (r *Rules) ToJSON() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	apps := make([]string, 0, len(r.apps))
	for a := range r.apps {
		apps = append(apps, a)
	}
	noTLSApps := make([]string, 0, len(r.noTLSApps))
	for a := range r.noTLSApps {
		noTLSApps = append(noTLSApps, a)
	}
	data, _ := json.Marshal(rulesJSON{
		Mode:      r.mode,
		Apps:      apps,
		NoTLSApps: noTLSApps,
	})
	return data
}

func (r *Rules) FromJSON(data []byte) error {
	var rj rulesJSON
	if err := json.Unmarshal(data, &rj); err != nil {
		return err
	}
	r.SetMode(rj.Mode)
	r.SetApps(rj.Apps)
	r.SetNoTLSApps(rj.NoTLSApps)
	return nil
}
