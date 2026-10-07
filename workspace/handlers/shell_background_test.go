package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

// PLAT-645: a command that starts a background job used to hold the call until that job ended (Go waited for the
// job to close the inherited output pipe), so the chat's next commands looked stuck. It must return right away,
// say what is still running, and leave the next command free to run.
func TestExecuteShellCommandBackgroundJobReturnsPromptly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", t.TempDir())
	defer viper.Set("docs-dir", "")

	run := func(command string) (models.ExecuteShellResponse, time.Duration) {
		t.Helper()
		body, _ := json.Marshal(models.ExecuteShellRequest{Command: command, Timeout: 60})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/api/execute", strings.NewReader(string(body)))
		c.Request.Header.Set("Content-Type", "application/json")
		started := time.Now()
		ExecuteShellCommand(c)
		var resp models.APIResponse[models.ExecuteShellResponse]
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %q: %v (%s)", command, err, rec.Body.String())
		}
		return resp.Data, time.Since(started)
	}

	first, took := run("sleep 30 & echo started")
	if took > 10*time.Second {
		t.Fatalf("command with a background job took %s; it waited for the job", took)
	}
	if strings.TrimSpace(first.Stdout) != "started" || first.ExitCode != 0 {
		t.Fatalf("stdout=%q exit=%d", first.Stdout, first.ExitCode)
	}
	if !strings.Contains(first.Stderr, "[background]") {
		t.Fatalf("result does not say a background process is running: %q", first.Stderr)
	}
	m := regexp.MustCompile(`kill (\d+)`).FindStringSubmatch(first.Stderr)
	if m == nil {
		t.Fatalf("result does not name the background pid: %q", first.Stderr)
	}
	pid, _ := strconv.Atoi(m[1])
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()

	next, took := run("echo next")
	if took > 5*time.Second || strings.TrimSpace(next.Stdout) != "next" || strings.Contains(next.Stderr, "[background]") {
		t.Fatalf("next command: took=%s stdout=%q stderr=%q", took, next.Stdout, next.Stderr)
	}
}
