package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dashboardAssets contains the exported Next.js dashboard shipped inside the API binary.
//
//go:embed all:dashboardapp/out/*
var dashboardAssets embed.FS

// dashboardAppHandler serves the embedded dashboard app and its static export assets.
func (s *Server) dashboardAppHandler() http.Handler {
	subtree, err := fs.Sub(dashboardAssets, "dashboardapp/out")
	if err != nil {
		return http.NotFoundHandler()
	}

	fileServer := http.FileServerFS(subtree)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}

		requestPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if requestPath == "." || requestPath == "/" {
			requestPath = "index.html"
		}

		if info, err := fs.Stat(subtree, requestPath); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}

		http.ServeFileFS(w, r, dashboardAssets, "dashboardapp/out/index.html")
	})
}
