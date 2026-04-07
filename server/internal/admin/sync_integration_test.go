package admin

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"smurov-proxy/server/internal/db"
)

func TestSyncIntegrationFullFlow(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	user, _ := d.CreateUser("alice")
	dev, _ := d.CreateDevice(user.ID, "mac")

	h := NewHandler(d, nil, "admin", "pw", t.TempDir())

	// 1. Empty sync returns empty my_sites
	w := postSync(t, h, dev.Key, map[string]interface{}{"last_sync_at": 0, "ops": []interface{}{}})
	if w.Code != http.StatusOK {
		t.Fatalf("empty sync code=%d", w.Code)
	}
	var r1 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r1)
	if len(r1["my_sites"].([]interface{})) != 0 {
		t.Fatalf("expected empty my_sites")
	}

	// 2. Add a seed site by primary_domain — expect dedup
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "add", "local_id": -1, "site": map[string]string{"primary_domain": "youtube.com", "label": "YouTube"}, "at": 1000},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("add seed code=%d: %s", w.Code, w.Body.String())
	}
	var r2 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r2)
	ops := r2["op_results"].([]interface{})
	if op := ops[0].(map[string]interface{}); op["status"] != "ok" || op["deduped"] != true {
		t.Fatalf("expected deduped ok, got %+v", op)
	}

	// 3. Disable it
	w = postSync(t, h, dev.Key, map[string]interface{}{
		"last_sync_at": 0,
		"ops": []map[string]interface{}{
			{"op": "disable", "site_id": 1, "at": 2000},
		},
	})
	var r3 map[string]interface{}
	json.NewDecoder(w.Body).Decode(&r3)
	sites := r3["my_sites"].([]interface{})
	found := false
	for _, s := range sites {
		m := s.(map[string]interface{})
		if m["id"].(float64) == 1 {
			if m["enabled"].(bool) {
				t.Fatalf("expected disabled")
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("youtube not in my_sites after disable")
	}
}
