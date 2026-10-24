package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A source server served an old bundled app beside the current Vite app,
// turning saved status events into raw JSON cards in one browser tab.
func TestLocalFrontendNavigation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("bundled app"), 0600); err != nil {
		t.Fatal(err)
	}
	handler := spaStaticFileHandler(root)
	for _, test := range []struct {
		name, local, frontend, method, request, location string
	}{
		{"home", "true", "http://127.0.0.1:51733", "GET", "http://127.0.0.1:18743/", "http://127.0.0.1:51733/"},
		{"deep link", "true", "http://127.0.0.1:51733", "GET", "http://localhost:18743/report?path=YWJj%3D&document=db%2Freports%2Findex.html", "http://127.0.0.1:51733/report?path=YWJj%3D&document=db%2Freports%2Findex.html"},
		{"head", "true", "http://127.0.0.1:51734/", "HEAD", "http://localhost:18743/workflows/example", "http://127.0.0.1:51734/workflows/example"},
		{"same port alias", "true", "http://127.0.0.1:18743", "GET", "http://localhost:18743/", ""},
		{"packaged", "true", "", "GET", "http://localhost:18743/", ""},
		{"deployed", "false", "http://127.0.0.1:51733", "GET", "http://localhost:18743/", ""},
		{"public host", "true", "http://127.0.0.1:51733", "GET", "http://app.example.com/", ""},
		{"public override", "true", "http://app.example.com", "GET", "http://localhost:18743/", ""},
		{"api", "true", "http://127.0.0.1:51733", "GET", "http://localhost:18743/api/health", ""},
		{"asset", "true", "http://127.0.0.1:51733", "GET", "http://localhost:18743/assets/index-old.js", ""},
		{"runtime", "true", "http://127.0.0.1:51733", "GET", "http://localhost:18743/runtime-config.js", ""},
		{"post", "true", "http://127.0.0.1:51733", "POST", "http://localhost:18743/", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("LOCAL_MODE", test.local)
			t.Setenv("AGENTWORKS_CLI_BROWSER_URL", test.frontend)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(test.method, test.request, nil))
			if got := w.Header().Get("Location"); got != test.location {
				t.Fatalf("Location = %q, want %q", got, test.location)
			}
			if test.location != "" {
				if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("redirect must be temporary and uncached: %d %v", w.Code, w.Header())
				}
			} else if test.request == "http://localhost:18743/" && test.method == "GET" && w.Body.String() != "bundled app" {
				t.Fatalf("bundled app unavailable: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
