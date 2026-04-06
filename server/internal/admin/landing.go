package admin

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type downloadFile struct {
	Name  string
	URL   string
	Label string
	Class string
	Icon  template.HTML
	Badge string
}

var landingTmpl = template.Must(template.New("landing").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>SmurovProxy</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: #0a0a1a; color: #e0e0e0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
.container { max-width: 600px; padding: 60px 32px; text-align: center; }
h1 { font-size: 2.5rem; font-weight: 800; margin-bottom: 12px; background: linear-gradient(135deg, #3b82f6, #10b981); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
.subtitle { color: #888; font-size: 1.1rem; margin-bottom: 48px; }
.downloads { display: flex; flex-direction: column; gap: 16px; margin-bottom: 48px; }
.download-btn { display: flex; align-items: center; justify-content: center; gap: 12px; padding: 16px 24px; border-radius: 12px; text-decoration: none; font-size: 1rem; font-weight: 600; transition: transform 0.15s, box-shadow 0.15s; }
.download-btn:hover { transform: translateY(-2px); box-shadow: 0 8px 24px rgba(0,0,0,0.3); }
.mac { background: #1a1a2e; border: 1px solid #333; color: #fff; }
.win { background: #1a1a2e; border: 1px solid #333; color: #fff; }
.icon { font-size: 1.4rem; }
.badge { font-size: 0.75rem; background: #333; padding: 2px 8px; border-radius: 6px; color: #aaa; margin-left: 4px; }
.footer { color: #555; font-size: 0.85rem; }
.footer a { color: #3b82f6; text-decoration: none; }
.footer a:hover { text-decoration: underline; }
.setup { background: #111; border: 1px solid #222; border-radius: 12px; padding: 24px; margin-bottom: 32px; text-align: left; }
.setup h3 { font-size: 0.9rem; color: #888; margin-bottom: 12px; text-transform: uppercase; letter-spacing: 1px; }
.setup ol { padding-left: 20px; line-height: 2; color: #bbb; font-size: 0.95rem; }
</style>
</head>
<body>
<div class="container">
  <h1>SmurovProxy</h1>
  <p class="subtitle">Secure TLS proxy — fast, private, undetectable</p>

  <div class="downloads">
    {{range .}}<a href="{{.URL}}" class="download-btn {{.Class}}">
      <span class="icon">{{.Icon}}</span> {{.Label}} <span class="badge">{{.Badge}}</span>
    </a>
    {{end}}
  </div>

  <div class="setup">
    <h3>Quick Start</h3>
    <ol>
      <li>Download and install the app</li>
      <li>Enter your access key</li>
      <li>Click Connect</li>
    </ol>
  </div>

  <div class="footer">
    <a href="/admin/">Admin Panel</a>
  </div>
</div>
</body>
</html>`))

func LandingHandler(downloadsDir string) http.Handler {
	cache := &releaseCache{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := cache.get()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		landingTmpl.Execute(w, files)
	})
}

const githubRepo = "ilyasmurov/smurov-proxy"

type releaseCache struct {
	mu      sync.Mutex
	files   []downloadFile
	fetched time.Time
}

func (c *releaseCache) get() []downloadFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) < 5*time.Minute && c.files != nil {
		return c.files
	}
	files := fetchGitHubAssets()
	if files != nil {
		c.files = files
		c.fetched = time.Now()
	}
	if c.files != nil {
		return c.files
	}
	return fallbackDownloads()
}

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func fetchGitHubAssets() []downloadFile {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/" + githubRepo + "/releases/latest")
	if err != nil {
		log.Printf("[landing] github API error: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("[landing] github API status: %d", resp.StatusCode)
		return nil
	}

	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		log.Printf("[landing] github API decode error: %v", err)
		return nil
	}

	var result []downloadFile
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		switch {
		case strings.HasSuffix(lower, ".pkg") && strings.Contains(lower, "arm64"):
			result = append(result, downloadFile{
				Name: a.Name, URL: a.BrowserDownloadURL,
				Label: "macOS Apple Silicon", Class: "mac",
				Icon: "&#63743;", Badge: ".pkg",
			})
		case strings.HasSuffix(lower, ".exe") && strings.Contains(lower, "setup"):
			result = append(result, downloadFile{
				Name: a.Name, URL: a.BrowserDownloadURL,
				Label: "Windows", Class: "win",
				Icon: "&#9114;", Badge: ".exe",
			})
		}
	}
	return result
}

func fallbackDownloads() []downloadFile {
	ghBase := "https://github.com/" + githubRepo + "/releases/latest"
	return []downloadFile{
		{URL: ghBase, Label: "macOS Apple Silicon", Class: "mac", Icon: "&#63743;", Badge: ".pkg"},
		{URL: ghBase, Label: "Windows", Class: "win", Icon: "&#9114;", Badge: ".exe"},
	}
}
