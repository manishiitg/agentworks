//go:build linux

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
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// A Code's plain shell runs in the same sandbox as agent commands: its own
// project writable, other projects, the host /tmp and the shared tmux server
// out of reach. Opt-in: needs the Landlock launcher (and, for the private
// /tmp on Ubuntu, a test binary the host's userns exception covers).
func TestInteractiveShellIsSandboxedE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	viper.Set("docs-dir", docs)
	own := "_users/alice/Chats/Code/projects/a"
	other := "_users/bob/Chats/Code/projects/b"
	for _, d := range []string{own, other} {
		if err := os.MkdirAll(filepath.Join(docs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(docs, other, "secret.txt"), []byte("bob-secret"), 0o644); err != nil {
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
	id := "e2e-" + strings.ReplaceAll(filepath.Base(docs), "_", "-")
	id = strings.ToLower(id)
	if len(id) > 40 {
		id = id[:40]
	}
	defer call("/stop", map[string]any{"shell_id": id})

	guard := map[string]any{"enabled": true, "read_paths": []string{own}, "write_paths": []string{own}}
	if code, out := call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard}); code != 200 {
		t.Fatalf("start = %d %v", code, out)
	}
	code, out := call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard})
	data, _ := out["data"].(map[string]any)
	socket, _ := data["socket"].(string)
	if code != 200 || socket == "" {
		t.Fatalf("second start must return the running shell: %d %v", code, out)
	}
	if code, _ := call("/start", map[string]any{"shell_id": "noguard-" + id, "working_directory": own}); code != 400 {
		t.Fatalf("a shell without a folder guard must be refused, got %d", code)
	}

	run := func(line string) {
		if err := exec.Command("tmux", "-S", socket, "send-keys", "-t", "shell", line, "Enter").Run(); err != nil {
			t.Fatalf("send-keys: %v", err)
		}
	}
	run(`echo owned > ./mine.txt && echo OWN_WRITE_OK`)
	run(`cat ../../../../bob/Chats/Code/projects/b/secret.txt 2>/dev/null && echo READ_OTHER`)
	run(`echo x > ../../../../bob/Chats/Code/projects/b/planted 2>/dev/null && echo WROTE_OTHER`)
	run(`tmux -S /tmp/tmux-$(id -u)/default ls >/dev/null 2>&1 && echo HOST_TMUX`)
	run(`echo DONE-$((40+2))`)
	var screen string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		captured, _ := exec.Command("tmux", "-S", socket, "capture-pane", "-p", "-t", "shell", "-S", "-200").Output()
		screen = string(captured)
		if strings.Contains(screen, "DONE-42") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("shell screen:\n%s", screen)
	if !strings.Contains(screen, "DONE-42") || !strings.Contains(screen, "OWN_WRITE_OK") {
		t.Fatal("the shell did not run commands in its own project")
	}
	for _, bad := range []string{"bob-secret", "WROTE_OTHER", "HOST_TMUX"} {
		if strings.Contains(strings.ReplaceAll(screen, "&& echo "+bad, ""), bad) {
			t.Errorf("shell escaped its sandbox: %s", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(docs, other, "planted")); err == nil {
		t.Error("shell wrote into another project")
	}
	if code, _ := call("/stop", map[string]any{"shell_id": id}); code != 200 || interactiveShellRunning(socket) {
		t.Fatal("stop did not end the shell")
	}
}

// The attach client runs in the shell's sandbox: a command the tmux server
// hands the attached client (`detach-client -E`) gets the shell's rights, not
// the service's. Opt-in like the test above.
func TestInteractiveShellAttachIsSandboxedE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	viper.Set("docs-dir", docs)
	own := "_users/alice/Chats/Code/projects/a"
	other := "_users/bob/Chats/Code/projects/b"
	for _, d := range []string{own, other} {
		if err := os.MkdirAll(filepath.Join(docs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	router.POST("/start", StartInteractiveShell)
	router.POST("/stop", StopInteractiveShell)
	router.GET("/attach", AttachInteractiveShell)
	server := httptest.NewServer(router)
	defer server.Close()
	post := func(path string, body any) int {
		raw, _ := json.Marshal(body)
		resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	id := strings.ToLower("att-" + strings.ReplaceAll(filepath.Base(docs), "_", "-"))
	if len(id) > 40 {
		id = id[:40]
	}
	defer post("/stop", map[string]any{"shell_id": id})
	guard := map[string]any{"enabled": true, "read_paths": []string{own}, "write_paths": []string{own}}
	if code := post("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard}); code != 200 {
		t.Fatalf("start = %d", code)
	}
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id=unknown-shell", nil); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("attach to an unknown shell must be refused")
	}
	browserHeader := http.Header{"Origin": {"https://example.com"}}
	if conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id="+id, browserHeader); err == nil {
		conn.Close()
		t.Fatal("a browser-origin attach must be refused")
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id="+id+"&cols=80&rows=24", nil)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer conn.Close()
	planted := filepath.Join(docs, other, "planted-by-attach")
	ownMarker := filepath.Join(docs, own, "attach-ran")
	line := "tmux -S \"$(tmux display -p '#{socket_path}')\" detach-client -E " +
		shellQuote("touch "+shellQuote(ownMarker)+"; touch "+shellQuote(planted)) + "\r"
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(line)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(ownMarker); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if _, err := os.Stat(ownMarker); err != nil {
		t.Fatal("the attach client never ran the command; the test proves nothing")
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(planted); err == nil {
		t.Fatal("a command run by the attach client wrote outside the shell's sandbox")
	}
}

// A Code's Folder Guard protects the coding agent's own files inside the
// writable project (.claude, CLAUDE.md). That shape used to push the shell to
// the mount-namespace backend, where its socket was moved off the path the
// service attaches to and the host /tmp stayed visible. It must run in the
// Landlock sandbox with the protected files read-only. Opt-in like above.
func TestInteractiveShellWithProtectedAgentFilesE2E(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	viper.Set("docs-dir", docs)
	own := "_users/alice/Chats/Code/projects/p"
	ownAbs := filepath.Join(docs, own)
	if err := os.MkdirAll(filepath.Join(ownAbs, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownAbs, "CLAUDE.md"), []byte("policy"), 0o644); err != nil {
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
	id := strings.ToLower("pro-" + strings.ReplaceAll(filepath.Base(docs), "_", "-"))
	if len(id) > 40 {
		id = id[:40]
	}
	defer call("/stop", map[string]any{"shell_id": id})
	guard := map[string]any{
		"enabled": true, "read_paths": []string{own + "/"}, "write_paths": []string{own + "/"},
		"blocked_write_paths": []string{filepath.Join(ownAbs, ".claude"), filepath.Join(ownAbs, "CLAUDE.md"), ".claude", "CLAUDE.md"},
	}
	code, out := call("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard})
	if code != 200 {
		t.Fatalf("start = %d %v", code, out)
	}
	_, socket := interactiveShellSocketNoSlot(id)
	if !interactiveShellRunning(socket) {
		t.Fatal("the shell's socket is not where the service attaches")
	}
	run := func(line string) {
		if err := exec.Command("tmux", "-S", socket, "send-keys", "-t", "shell", line, "Enter").Run(); err != nil {
			t.Fatalf("send-keys: %v", err)
		}
	}
	run(`echo ok > ./mine.txt && echo OWN_WRITE_OK`)
	run(`echo x > ./.claude/planted 2>/dev/null && echo WROTE_AGENT_DIR`)
	run(`echo x >> ./CLAUDE.md 2>/dev/null && echo WROTE_POLICY`)
	run(`rm -f ./CLAUDE.md 2>/dev/null; test -e ./CLAUDE.md || echo REMOVED_POLICY`)
	run(`tmux -S /tmp/tmux-$(id -u)/default ls >/dev/null 2>&1 && echo HOST_TMUX`)
	run(`echo DONE-$((40+2))`)
	var screen string
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		captured, _ := exec.Command("tmux", "-S", socket, "capture-pane", "-p", "-t", "shell", "-S", "-200").Output()
		screen = string(captured)
		if strings.Contains(screen, "DONE-42") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("shell screen:\n%s", screen)
	if !strings.Contains(screen, "DONE-42") || !strings.Contains(screen, "OWN_WRITE_OK") {
		t.Fatal("the shell did not run commands in its own project")
	}
	for _, bad := range []string{"WROTE_AGENT_DIR", "WROTE_POLICY", "REMOVED_POLICY", "HOST_TMUX"} {
		if strings.Contains(strings.ReplaceAll(screen, "echo "+bad, ""), bad) {
			t.Errorf("shell escaped its sandbox: %s", bad)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(ownAbs, "CLAUDE.md")); string(raw) != "policy" {
		t.Errorf("CLAUDE.md changed: %q", raw)
	}
}

// interactiveShellSocketNoSlot is the shell folder and socket where slots are off.
func interactiveShellSocketNoSlot(id string) (dir, socket string) {
	dir, socket, _ = interactiveShellPaths(id, "")
	return dir, socket
}
