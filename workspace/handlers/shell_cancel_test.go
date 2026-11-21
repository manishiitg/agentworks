//go:build !windows

package handlers

import (
	"bytes"
	"context"
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
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

// PLAT-746-506: ordinary shell requests must stop their real child, including
// one that ignores TERM, rather than reporting only the launcher's exit.
func TestShellRequestCancellationStopsTheLaunchersChild(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the real child-process check")
	}
	root := t.TempDir()
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	heartbeat := filepath.Join(root, "heartbeat")
	script := filepath.Join(root, "child.py")
	if err := os.WriteFile(script, []byte("import signal,time\nfrom pathlib import Path\nsignal.signal(signal.SIGTERM,signal.SIG_IGN)\nfor i in range(2400):\n Path('heartbeat').write_text(str(i))\n time.sleep(0.05)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	data, _ := json.Marshal(models.ExecuteShellRequest{Command: quote(python) + " " + quote(script) + " & wait", WorkingDirectory: ".", Timeout: 20})
	done := make(chan struct{})
	router := gin.New()
	router.POST("/execute", func(c *gin.Context) { defer close(done); ExecuteShellCommand(c) })
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/execute", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", "test-user")
	go func() {
		if r, err := http.DefaultClient.Do(request); err == nil {
			r.Body.Close()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		before, _ := os.ReadFile(heartbeat)
		time.Sleep(80 * time.Millisecond)
		after, _ := os.ReadFile(heartbeat)
		if len(before) > 0 && len(after) > 0 && !bytes.Equal(before, after) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real child did not advance its heartbeat")
		}
	}
	cancel()
	stoppedAt := time.Now()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shell handler did not return after cancellation")
	}
	if remaining := time.Until(stoppedAt.Add(5 * time.Second)); remaining > 0 {
		time.Sleep(remaining)
	}
	before, err := os.Stat(heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	after, err := os.Stat(heartbeat)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.ModTime().After(stoppedAt.Add(5*time.Second)) {
		t.Fatalf("real child kept writing after cancellation +5s: before=%v after=%v err=%v", before, after, err)
	}
}
