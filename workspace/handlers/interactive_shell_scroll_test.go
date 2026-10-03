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

// The browser terminal draws tmux's screen, so it has no scrollback of its own: the wheel only scrolls if tmux reports mouse events
// and keeps a long history. With the mouse off (tmux's default) a wheel did nothing, and history stopped at 2000 lines. Opt-in like the
// other shell checks (needs tmux; on Linux also the Landlock launcher).
func TestInteractiveShellWheelScrollsTheHistory(t *testing.T) {
	if os.Getenv("AGENTWORKS_INTERACTIVE_SHELL_E2E") != "1" {
		t.Skip("set AGENTWORKS_INTERACTIVE_SHELL_E2E=1 to run")
	}
	gin.SetMode(gin.TestMode)
	// Outside /var/folders, which the strict profile on a Mac grants as scratch space.
	docs, err := os.MkdirTemp(".", "zz-scroll-docs-")
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = filepath.Abs(docs)
	t.Cleanup(func() { os.RemoveAll(docs) })
	viper.Set("docs-dir", docs)
	own := "_users/alice/Chats/Code/projects/p/code"
	if err := os.MkdirAll(filepath.Join(docs, own), 0o755); err != nil {
		t.Fatal(err)
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
	id := "scroll-e2e"
	defer post("/stop", map[string]any{"shell_id": id})
	guard := map[string]any{"enabled": true, "strict_allowlist": true, "read_paths": []string{"_users/alice/Chats/Code/projects/p/"}, "write_paths": []string{"_users/alice/Chats/Code/projects/p/"}}
	if code := post("/start", map[string]any{"shell_id": id, "working_directory": own, "folder_guard": guard}); code != http.StatusOK {
		t.Fatalf("start = %d", code)
	}
	_, socket, _ := interactiveShellPaths(id, "")
	tmux := func(args ...string) string {
		out, _ := exec.Command(realTmux(), append([]string{"-S", socket}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out))
	}
	if got := tmux("show-options", "-gv", "mouse"); got != "on" {
		t.Fatalf("tmux mouse = %q: without it the wheel does nothing", got)
	}
	if got := tmux("show-options", "-gv", "history-limit"); got != "50000" {
		t.Fatalf("history-limit = %q, want 50000", got)
	}
	if got := tmux("show-options", "-gv", "status"); got != "off" {
		t.Fatalf("status = %q: the tmux status bar should be hidden", got)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id="+id+"&cols=100&rows=24", nil)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("seq 1 300\r")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	// What xterm.js sends for a wheel-up when the application asked for mouse reporting (SGR encoding).
	for i := 0; i < 5; i++ {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("\x1b[<64;10;5M"))
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(800 * time.Millisecond)
	if tmux("display-message", "-p", "-t", "shell", "#{pane_in_mode}") != "1" || tmux("display-message", "-p", "-t", "shell", "#{scroll_position}") == "" {
		t.Fatal("a wheel-up must scroll the history back")
	}
	for i := 0; i < 8; i++ {
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte("\x1b[<65;10;5M"))
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond)
	if tmux("display-message", "-p", "-t", "shell", "#{pane_in_mode}") != "0" {
		t.Fatal("scrolling back down to the bottom must leave scroll mode")
	}
}
