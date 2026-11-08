package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
	if err := os.Mkdir(filepath.Join(viper.GetString("docs-dir"), "project"), 0700); err != nil {
		t.Fatal(err)
	}

	run := func(command string) (models.ExecuteShellResponse, time.Duration) {
		t.Helper()
		body, _ := json.Marshal(models.ExecuteShellRequest{Command: command, Timeout: 60, WorkingDirectory: "project", LogDirectory: "_users/person/chat_history/shell-runs", LogBufferBytes: 48000})
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

	project := filepath.Join(viper.GetString("docs-dir"), "project")
	if out, err := exec.Command("git", "init", project).CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %s %v", out, err)
	}
	short, _ := run("git status --porcelain")
	if strings.TrimSpace(short.Stdout) != "" || short.LogFile != "" || short.LogError != "" {
		t.Fatalf("short command created a log: %+v", short)
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
	if first.LogFile == "" || first.LogError != "" || first.LogComplete {
		t.Fatalf("missing growing full log: %+v", first)
	}
	m := regexp.MustCompile(`kill (\d+)`).FindStringSubmatch(first.Stderr)
	if m == nil {
		t.Fatalf("result does not name the background pid: %q", first.Stderr)
	}
	pid, _ := strconv.Atoi(m[1])
	defer func() { _ = signalProcess(pid, syscall.SIGKILL) }()

	next, took := run("echo next")
	if took > 5*time.Second || strings.TrimSpace(next.Stdout) != "next" || strings.Contains(next.Stderr, "[background]") {
		t.Fatalf("next command: took=%s stdout=%q stderr=%q", took, next.Stdout, next.Stderr)
	}
	late, took := run("echo before; (sleep 2; printf late-stdout; printf late-stderr >&2) &")
	if took > 1500*time.Millisecond || late.LogFile == "" || late.LogError != "" || late.LogComplete {
		t.Fatalf("background log blocked reply: %s %+v", took, late)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, err := os.ReadFile(late.LogFile)
		text := string(data)
		if err == nil && strings.Contains(text, "late-stdout") && strings.Contains(text, "late-stderr") && strings.Contains(text, "output streams closed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("output after detach missing: %q %v", text, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	large, _ := run("awk 'BEGIN {for(i=0;i<2097152;i++)printf \"Q\"}'")
	full, err := os.ReadFile(large.LogFile)
	if err != nil || large.LogError != "" || !large.LogComplete || strings.Count(string(full), "Q") != 2097152 {
		t.Fatalf("large private log missing: size=%d %v %+v", len(full), err, large)
	}
	private := filepath.Join(viper.GetString("docs-dir"), "_users/person/chat_history")
	if rel, err := filepath.Rel(private, large.LogFile); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		t.Fatalf("log escaped private chat folder: %s", large.LogFile)
	}
	clean, _ := run("git status --porcelain")
	if strings.TrimSpace(clean.Stdout) != "" || clean.LogFile != "" {
		t.Fatalf("shell logs dirtied git: %+v", clean)
	}

}
