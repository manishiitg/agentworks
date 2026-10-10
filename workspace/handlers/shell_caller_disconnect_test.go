//go:build !windows

package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

// PLAT-805: stopping a step drops the HTTP request that started its command, and the command must not outlive it. The
// handler used to run every command on a context made from context.Background(), so a stopped step's Python script,
// Chrome and ffmpeg kept running until the command's own timeout (RTS, 2026-10-09: over ten hours, on a 2-vCPU host).
func TestExecuteShellCommandStopsWhenTheCallerDisconnects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", t.TempDir())
	defer viper.Set("docs-dir", "")

	const marker = "sleep 3607" // a duration nothing else on a test host runs
	running := func() bool { return exec.Command("pgrep", "-f", marker).Run() == nil }
	defer func() { _ = exec.Command("pkill", "-KILL", "-f", marker).Run() }()

	body, _ := json.Marshal(models.ExecuteShellRequest{Command: marker, Timeout: 3600})
	caller, disconnect := context.WithCancel(context.Background())
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/execute", strings.NewReader(string(body))).WithContext(caller)
	c.Request.Header.Set("Content-Type", "application/json")
	handled := make(chan struct{})
	go func() { ExecuteShellCommand(c); close(handled) }()

	started := time.Now()
	for !running() {
		if time.Since(started) > 10*time.Second {
			t.Fatal("the command never started")
		}
		time.Sleep(50 * time.Millisecond)
	}

	disconnect() // the caller goes away, as when stop_step cancels the step
	select {
	case <-handled:
	case <-time.After(20 * time.Second):
		t.Fatal("the handler kept waiting for a command nobody is waiting for")
	}
	deadline := time.Now().Add(10 * time.Second)
	for running() {
		if time.Now().After(deadline) {
			t.Fatal("the command kept running after its caller disconnected")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
