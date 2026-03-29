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
	mu   sync.RWMutex
	mode Mode
	apps map[string]bool
}

type rulesJSON struct {
	Mode Mode     `json:"mode"`
	Apps []string `json:"apps"`
}

func NewRules() *Rules {
	return &Rules{
		mode: ModeProxyAllExcept,
		apps: make(map[string]bool),
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
	data, _ := json.Marshal(rulesJSON{
		Mode: r.mode,
		Apps: apps,
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
	return nil
}
