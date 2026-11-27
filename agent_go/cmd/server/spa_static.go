package server

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func spaStaticFileHandler(root string) http.Handler {
	fileServer := http.FileServer(http.Dir(root))
	indexPath := filepath.Join(root, "index.html")

	// serveShell serves the SPA shell with no-cache so an upgraded desktop build's
	// new UI shows up on next launch without a manual hard reload. The agent server
	// runs on a stable localhost origin/port across upgrades, so without this header
	// Chromium's heuristic cache keeps serving the previous index.html (and its old
	// asset references). no-cache lets the browser store but forces revalidation.
	serveShell := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, indexPath)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target := localFrontendRedirect(r); target != "" {
			w.Header().Set("Cache-Control", "no-store")
			log.Printf("[FRONTEND] App navigation uses current local frontend %s", strings.TrimRight(strings.TrimSpace(os.Getenv("AGENTWORKS_CLI_BROWSER_URL")), "/"))
			http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fileServer.ServeHTTP(w, r)
			return
		}

		if r.URL.Path == "/" {
			serveShell(w, r)
			return
		}
		// An address under /api that no route answers is "not found", never the web page: a client reading data from
		// an older server must learn the route is missing instead of failing to read HTML (#3900).
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			externalError(w, http.StatusNotFound, "not_found", "This server doesn't have this feature yet. It needs an update.")
			return
		}

		relativePath := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if relativePath != "." && !strings.HasPrefix(relativePath, "..") {
			requestedPath := filepath.Join(root, relativePath)
			if info, err := os.Stat(requestedPath); err == nil && !info.IsDir() {
				// Build assets are content-hashed (assets/index-<hash>.js), so a
				// given URL is immutable and safe to cache for a year.
				if strings.HasPrefix(relativePath, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		if _, err := os.Stat(indexPath); err == nil {
			serveShell(w, r)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}
