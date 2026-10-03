//go:build darwin

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// On a Mac the strict sandbox (what Code uses) must let a terminal start: tmux needs terminal devices, and its socket must be
// named by the real folder because the sandbox will not follow /tmp (a link to /private/tmp). Both failed on 2026-10-03 as
// "error connecting to .../tmux.sock (Operation not permitted)". Needs tmux; opt-in like the Linux checks.
func TestInteractiveShellStartsInTheStrictSandboxOnAMac(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	// Outside /var/folders, which the strict profile grants as scratch space.
	docs, err := os.MkdirTemp(".", "zz-darwin-docs-")
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = filepath.Abs(docs)
	t.Cleanup(func() { os.RemoveAll(docs) })
	viper.Set("docs-dir", docs)
	own := "_users/alice/Chats/Code/projects/a"
	if err := os.MkdirAll(filepath.Join(docs, own), 0o755); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/start", StartInteractiveShell)
	router.POST("/stop", StopInteractiveShell)
	call := func(path string, body any) (int, map[string]any) {
		raw, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw)))
		out := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	id := "darwin-strict-e2e"
	defer call("/stop", map[string]any{"shell_id": id})
	guard := map[string]any{"enabled": true, "strict_allowlist": true, "read_paths": []string{own + "/"}, "write_paths": []string{own + "/"}}
	code, out := call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard})
	if code != http.StatusOK {
		t.Fatalf("start = %d %v", code, out)
	}
	data, _ := out["data"].(map[string]any)
	socket, _ := data["socket"].(string)
	if !strings.HasPrefix(socket, "/private/tmp/") || !interactiveShellRunning(socket) {
		t.Fatalf("socket %q must be the real path and the shell must be running", socket)
	}
	if code, _ := call("/stop", map[string]any{"shell_id": id}); code != http.StatusOK || interactiveShellRunning(socket) {
		t.Fatal("stop did not end the shell")
	}
}
