//go:build darwin

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// The local app runs the workspace natively: the sandboxed shell inherits the real environment, real HOME included.
	t.Setenv("NATIVE_WORKSPACE", "true")
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
	// The shell's home is private to the project, so nothing in it tries to read the real home ("Operation not permitted").
	exec.Command(realTmux(), "-S", socket, "send-keys", "-t", "shell", "echo HOME=$HOME; ls ~/.bash_profile 2>&1; git config --global -l 2>&1 | head -1; echo END", "Enter").Run()
	time.Sleep(1500 * time.Millisecond)
	screen, _ := exec.Command(realTmux(), "-S", socket, "capture-pane", "-p", "-t", "shell").Output()
	if !strings.Contains(string(screen), "HOME="+filepath.Join(docs, own, ".sandbox-cache", "home")) || strings.Contains(string(screen), "Operation not permitted") {
		t.Fatalf("the shell must have a private home and no denied reads: %s", screen)
	}
	// A short prompt: the folder's name, not user@host:/full/path.
	if !strings.Contains(string(screen), "a $ echo HOME=") || strings.Contains(string(screen), "@") {
		t.Fatalf("the prompt must be just the folder name: %s", screen)
	}
	// An empty cd returns to the folder the terminal started in, not the private home it would otherwise land in.
	exec.Command(realTmux(), "-S", socket, "send-keys", "-t", "shell", "cd ..; cd ~; echo CD1=$PWD; cd /; cd; echo CD2=$PWD", "Enter").Run()
	time.Sleep(1500 * time.Millisecond)
	screen, _ = exec.Command(realTmux(), "-S", socket, "capture-pane", "-p", "-t", "shell").Output()
	start := filepath.Join(docs, own)
	if !strings.Contains(string(screen), "CD1="+start+"\n") || !strings.Contains(string(screen), "CD2="+start+"\n") {
		t.Fatalf("an empty cd must return to the starting folder %s: %s", start, screen)
	}
	if code, _ := call("/stop", map[string]any{"shell_id": id}); code != http.StatusOK || interactiveShellRunning(socket) {
		t.Fatal("stop did not end the shell")
	}
}

// On a person's own machine the terminal follows the coding agents' switch: no sandbox, the real home and rights, so git, codex and
// the rest read their normal config. A Mac in native mode only (see interactiveShellUnconfinedAllowed).
func TestInteractiveShellUnconfinedUsesTheRealHomeOnAMac(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("AGENTWORKS_SLOTS", "")
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
	id := "darwin-unconfined-e2e"
	defer call("/stop", map[string]any{"shell_id": id})
	guard := map[string]any{"enabled": true, "strict_allowlist": true, "read_paths": []string{own + "/"}, "write_paths": []string{own + "/"}}
	code, out := call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard, "unconfined": true})
	if code != http.StatusOK {
		t.Fatalf("start = %d %v", code, out)
	}
	data, _ := out["data"].(map[string]any)
	socket, _ := data["socket"].(string)
	home, _ := os.UserHomeDir()
	exec.Command(realTmux(), "-S", socket, "send-keys", "-t", "shell", "echo HOME=$HOME; ls ~ >/dev/null 2>&1 && echo HOME_READABLE; git config --global -l >/dev/null 2>&1; echo GIT_EXIT=$?; echo END", "Enter").Run()
	time.Sleep(1500 * time.Millisecond)
	screen, _ := exec.Command(realTmux(), "-S", socket, "capture-pane", "-p", "-t", "shell").Output()
	if !strings.Contains(string(screen), "HOME="+home) || !strings.Contains(string(screen), "HOME_READABLE") || strings.Contains(string(screen), "Operation not permitted") {
		t.Fatalf("an unconfined terminal must have the real, readable home: %s", screen)
	}
	if !strings.Contains(string(screen), "a $ echo HOME=") {
		t.Fatalf("an unconfined terminal gets the short prompt too: %s", screen)
	}
	// Without the request flag the same service still sandboxes it (the flag is only honoured where allowed, and asked for).
	call("/stop", map[string]any{"shell_id": id})
	code, _ = call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard})
	if code != http.StatusOK {
		t.Fatalf("sandboxed start = %d", code)
	}
	exec.Command(realTmux(), "-S", socket, "send-keys", "-t", "shell", "ls ~ >/dev/null 2>&1 && echo HOME_READABLE; echo END2", "Enter").Run()
	time.Sleep(1500 * time.Millisecond)
	screen, _ = exec.Command(realTmux(), "-S", socket, "capture-pane", "-p", "-t", "shell").Output()
	if strings.Contains(string(screen), "HOME_READABLE") && !strings.Contains(string(screen), "Operation not permitted") {
		// the sandboxed shell has a private home, which is readable; what matters is that it is not the real one
		if strings.Contains(string(screen), "HOME="+home) {
			t.Fatalf("the sandboxed terminal got the real home: %s", screen)
		}
	}
}
