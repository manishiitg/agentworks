package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
)

func serveCliDownload(t *testing.T, dir, file string) *httptest.ResponseRecorder {
	t.Helper()
	old := cliDownloadsDirOverride
	cliDownloadsDirOverride = dir
	t.Cleanup(func() { cliDownloadsDirOverride = old })
	req := httptest.NewRequest(http.MethodGet, "/api/downloads/cli/"+file, nil)
	req = mux.SetURLVars(req, map[string]string{"file": file})
	w := httptest.NewRecorder()
	(&StreamingAPI{}).handleCliDownload(w, req)
	return w
}

func TestCliDownloadServesAllowlistedFiles(t *testing.T) {
	dir := t.TempDir()
	for name := range cliDownloadFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for name, contentType := range cliDownloadFiles {
		w := serveCliDownload(t, dir, name)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: got %d", name, w.Code)
		}
		if got := w.Header().Get("Content-Type"); got != contentType {
			t.Fatalf("%s: content-type %q, want %q", name, got, contentType)
		}
		if w.Body.String() != "payload:"+name {
			t.Fatalf("%s: wrong body %q", name, w.Body.String())
		}
	}
}

func TestCliDownloadPinsOnlyArchivesToGithub(t *testing.T) {
	dir := t.TempDir()
	const pinned = "https://github.com/example/builds/releases/download/cli-aaaaaaaa-20261024000000"
	if err := os.WriteFile(filepath.Join(dir, "cli-download-base.txt"), []byte(pinned+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, contentType := range cliDownloadFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("local:"+name), 0o644); err != nil {
			t.Fatal(err)
		}
		w := serveCliDownload(t, dir, name)
		if contentType == "application/gzip" {
			if w.Code != http.StatusFound || w.Header().Get("Location") != pinned+"/"+name {
				t.Fatalf("%s: expected pinned redirect, got %d %q", name, w.Code, w.Header().Get("Location"))
			}
		} else if w.Code != http.StatusOK || w.Header().Get("Location") != "" || w.Body.String() != "local:"+name {
			t.Fatalf("%s: expected server-owned file, got %d %q", name, w.Code, w.Body.String())
		}
	}
	if w := serveCliDownload(t, dir, "cli-download-base.txt"); w.Code != http.StatusNotFound {
		t.Fatalf("build metadata was exposed: %d", w.Code)
	}
}

func TestCliDownloadRejectsUnknownAndTraversal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "agentworks-darwin-arm64"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"agentworks-windows-amd64", "install-agentworks.sh.bak", "../server.go", "..%2Fserver.go", "", "agentworks-darwin-arm64/extra"} {
		if w := serveCliDownload(t, dir, file); w.Code != http.StatusNotFound {
			t.Fatalf("%q: got %d, want 404", file, w.Code)
		}
	}
}

func TestCliDownloadMissingFileOrDirIs404(t *testing.T) {
	if w := serveCliDownload(t, t.TempDir(), "agentworks-linux-amd64"); w.Code != http.StatusNotFound {
		t.Fatalf("missing file: got %d, want 404", w.Code)
	}
	if w := serveCliDownload(t, filepath.Join(t.TempDir(), "absent"), "install-agentworks.sh"); w.Code != http.StatusNotFound {
		t.Fatalf("missing dir: got %d, want 404", w.Code)
	}
}

func TestCliDownloadUsesConfiguredLocalDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORKS_CLI_DOWNLOAD_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "install-agentworks.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := serveCliDownload(t, "", "install-agentworks.sh"); w.Code != http.StatusOK || w.Body.String() != "#!/bin/sh\n" {
		t.Fatalf("configured installer not served: %d %q", w.Code, w.Body.String())
	}
}
