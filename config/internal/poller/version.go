package poller

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"smurov-proxy/config/internal/db"
)

func Start(d *db.DB, repo string) {
	var lastVersion string
	check := func() {
		ver, err := fetchLatestVersion(repo)
		if err != nil {
			log.Printf("[poller] version check: %v", err)
			return
		}
		if lastVersion == "" {
			lastVersion = ver
			log.Printf("[poller] current latest: %s", ver)
			return
		}
		if ver != lastVersion {
			log.Printf("[poller] new version: %s (was %s)", ver, lastVersion)
			lastVersion = ver
			_, err := d.CreateNotification("update",
				fmt.Sprintf("Version %s available", ver),
				"A new client version has been released.",
				json.RawMessage(`{"label":"Update","type":"update"}`))
			if err != nil {
				log.Printf("[poller] create notification: %v", err)
			}
		}
	}
	check()
	for range time.Tick(1 * time.Hour) {
		check()
	}
}

func fetchLatestVersion(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", err
	}
	return release.TagName, nil
}
