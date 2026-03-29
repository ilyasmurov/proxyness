package admin

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed all:static
var staticFiles embed.FS

func SPAHandler() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		path = strings.TrimPrefix(path, "/")
		if path == "" {
			path = "index.html"
		}

		f, err := sub.Open(path)
		if err != nil {
			// SPA fallback: serve index.html for unknown routes
			path = "index.html"
			f, err = sub.Open(path)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		defer f.Close()

		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}
		w.Header().Set("Content-Type", ct)
		io.Copy(w, f.(io.Reader))
	})
}
