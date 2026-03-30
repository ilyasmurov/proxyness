package admin

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		files := scanDownloads(downloadsDir)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		landingTmpl.Execute(w, files)
	})
}

func scanDownloads(dir string) []downloadFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var macArm, macIntel []string
	var winExe []string

	for _, e := range entries {
		name := e.Name()
		lower := strings.ToLower(name)
		switch {
		case (strings.HasSuffix(lower, ".dmg") || strings.HasSuffix(lower, ".pkg")) && strings.Contains(lower, "arm64"):
			macArm = append(macArm, name)
		case (strings.HasSuffix(lower, ".dmg") || strings.HasSuffix(lower, ".pkg")) && !strings.Contains(lower, "arm64"):
			macIntel = append(macIntel, name)
		case strings.HasSuffix(lower, ".exe") && strings.Contains(lower, "setup"):
			winExe = append(winExe, name)
		}
	}

	// Sort descending so newest version comes first
	sortDesc := func(s []string) { sort.Sort(sort.Reverse(sort.StringSlice(s))) }
	sortDesc(macArm)
	sortDesc(macIntel)
	sortDesc(winExe)

	var result []downloadFile
	if len(macArm) > 0 {
		result = append(result, downloadFile{
			Name: macArm[0], URL: "/download/" + macArm[0],
			Label: "macOS Apple Silicon — " + macArm[0], Class: "mac",
			Icon: "&#63743;", Badge: filepath.Ext(macArm[0]),
		})
	}
	if len(macIntel) > 0 {
		result = append(result, downloadFile{
			Name: macIntel[0], URL: "/download/" + macIntel[0],
			Label: "macOS Intel — " + macIntel[0], Class: "mac",
			Icon: "&#63743;", Badge: filepath.Ext(macIntel[0]),
		})
	}
	if len(winExe) > 0 {
		result = append(result, downloadFile{
			Name: winExe[0], URL: "/download/" + winExe[0],
			Label: "Windows — " + winExe[0], Class: "win",
			Icon: "&#9114;", Badge: filepath.Ext(winExe[0]),
		})
	}
	return result
}
