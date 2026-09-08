package webui

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

// NewHandler serves the embedded UI at the origin root. The UI uses hash
// routing, so missing assets and API paths must remain 404 responses.
func NewHandler() (http.Handler, error) {
	root, err := fs.Sub(assets, assetRoot)
	if err != nil {
		return nil, fmt.Errorf("open embedded UI: %w", err)
	}
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		info, err := fs.Stat(root, name)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}), nil
}
