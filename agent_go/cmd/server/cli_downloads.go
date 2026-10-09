package server

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gorilla/mux"
)

// cliDownloadFiles is the exact set of CLI distribution files served from the
// release downloads directory: darwin/linux/windows binaries, their sha256 sidecars,
// and the installers. Anything else 404s — no listing, no traversal.
var cliDownloadFiles = map[string]string{
	"agentworks-darwin-arm64":             "application/octet-stream",
	"agentworks-darwin-amd64":             "application/octet-stream",
	"agentworks-linux-amd64":              "application/octet-stream",
	"agentworks-linux-arm64":              "application/octet-stream",
	"agentworks-windows-amd64.exe":        "application/octet-stream",
	"agentworks-windows-arm64.exe":        "application/octet-stream",
	"agentworks-windows-amd64.exe.sha256": "text/plain; charset=utf-8",
	"agentworks-windows-arm64.exe.sha256": "text/plain; charset=utf-8",
	"install-agentworks.ps1":              "text/plain; charset=utf-8",
	"agentworks-darwin-arm64.sha256":      "text/plain; charset=utf-8",
	"agentworks-darwin-amd64.sha256":      "text/plain; charset=utf-8",
	"agentworks-linux-amd64.sha256":       "text/plain; charset=utf-8",
	"agentworks-linux-arm64.sha256":       "text/plain; charset=utf-8",
	"install-agentworks.sh":               "text/x-shellscript; charset=utf-8",
	"version.json":                        "application/json",
}

// cliDownloadsDirOverride pins the downloads directory in tests. Production
// releases place downloads next to bin; the local run script sets an explicit
// directory for the native CLI and installer it packages before startup.
var cliDownloadsDirOverride string

func cliDownloadsDir() string {
	if cliDownloadsDirOverride != "" {
		return cliDownloadsDirOverride
	}
	if configured := os.Getenv("AGENTWORKS_CLI_DOWNLOAD_DIR"); filepath.IsAbs(configured) {
		return configured
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Join(filepath.Dir(exe), "..", "downloads")
}

// handleCliDownload serves one CLI distribution file. The route is public on
// both the gateway and the app (same as the CDP launcher zip): the binaries
// require a per-user browser approval (or a legacy personal token), which
// the app verifies on every call.
func (api *StreamingAPI) handleCliDownload(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["file"]
	contentType, ok := cliDownloadFiles[name]
	if !ok || filepath.Base(name) != name {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	base := cliDownloadsDir()
	if base == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	f, err := os.Open(filepath.Join(base, name))
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	if contentType == "application/octet-stream" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
