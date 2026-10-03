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

// The browser terminal keeps the history itself: tmux runs with its mouse OFF (a drag is the browser's selection, so copy works) and
// without the alternate screen (smcup@:rmcup@), so lines that scroll off the top are sent to the browser, whose scrollback the wheel
// scrolls locally and smoothly. Opt-in like the other shell checks (needs tmux; on Linux also the Landlock launcher).
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
	if got := tmux("show-options", "-gv", "mouse"); got != "off" {
		t.Fatalf("tmux mouse = %q: with it on, tmux takes every drag and nothing can be copied", got)
	}
	if got := tmux("show-options", "-gv", "history-limit"); got != "50000" {
		t.Fatalf("history-limit = %q, want 50000", got)
	}
	if got := tmux("show-options", "-gv", "terminal-overrides"); !strings.Contains(got, "smcup@:rmcup@") {
		t.Fatalf("terminal-overrides = %q: without smcup@:rmcup@ tmux uses the alternate screen and the browser keeps no history", got)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id="+id+"&cols=100&rows=24", nil)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer conn.Close()
	// The attach must not switch the browser to the alternate screen (ESC [ ? 1049 h), or its scrollback stays empty.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var stream strings.Builder
	for {
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		stream.Write(data)
	}
	if strings.Contains(stream.String(), "\x1b[?1049h") {
		t.Fatal("tmux switched the browser terminal to the alternate screen")
	}
}
