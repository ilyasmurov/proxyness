package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:static
var staticFiles embed.FS

func SPAHandler() http.Handler {
	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		if path == "" || path == "/" {
			path = "/index.html"
		}

		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			// SPA fallback: serve index.html for client-side routes
			r.URL.Path = "/index.html"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()
		r.URL.Path = path
		fileServer.ServeHTTP(w, r)
	})
}
