package rpc

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// WebDist embeds the built web UI assets. The variable is populated by the
// main package using go:embed. When empty (dev mode), the web UI routes
// are not registered.
var WebDist embed.FS

// webUIHandler returns an http.Handler that serves the embedded SPA.
// All unknown paths fall back to index.html for client-side routing.
func webUIHandler() http.Handler {
	sub, err := fs.Sub(WebDist, "web/dist")
	if err != nil {
		// No embedded assets — return 404 for everything.
		return http.NotFoundHandler()
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the static file directly.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Check if file exists in the embedded FS.
		f, err := sub.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		// SPA fallback: serve index.html for all unmatched routes.
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}
