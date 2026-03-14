package gateway

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed admin-ui
var adminUIFS embed.FS

// AdminUIHandler returns an HTTP handler that serves the embedded admin SPA.
// Non-file requests (SPA routing) fall back to index.html.
func AdminUIHandler() http.Handler {
	sub, _ := fs.Sub(adminUIFS, "admin-ui")
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip /admin/ prefix if present
		path := r.URL.Path
		if strings.HasPrefix(path, "/admin/") {
			path = strings.TrimPrefix(path, "/admin")
		}

		// Try to serve the file directly
		f, err := sub.Open(strings.TrimPrefix(path, "/"))
		if err != nil {
			// File not found — serve index.html for SPA routing
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		f.Close()

		r.URL.Path = path
		fileServer.ServeHTTP(w, r)
	})
}
