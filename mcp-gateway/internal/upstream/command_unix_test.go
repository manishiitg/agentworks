//go:build !windows

package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// TestLocalCommandChildFixture is invoked as a subprocess by
// TestLocalCommandProcessGroupKilledOnClose. It spawns a grandchild sleep
// process, records its PID, and serves a single MCP tool over stdio.
func TestLocalCommandChildFixture(t *testing.T) {
	if os.Getenv("LOCAL_CMD_PGROUP_FIXTURE") != "1" {
		t.Skip("fixture process only")
	}
	pidFile := os.Getenv("LOCAL_CMD_CHILD_PID_FILE")
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(sleepBin, "60")
	if err := child.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0o600)
	s := server.NewMCPServer("pgroup-fixture", "1")
	s.AddTool(mcp.NewTool("ping"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("pong"), nil
	})
	_ = server.ServeStdio(s)
	os.Exit(0)
}

func TestLocalCommandProcessGroupKilledOnClose(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	launch, err := json.Marshal(LocalCommand{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestLocalCommandChildFixture$"},
		Env: map[string]string{
			"LOCAL_CMD_PGROUP_FIXTURE": "1",
			"LOCAL_CMD_CHILD_PID_FILE": pidFile,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	up, err := DialWithOptions(dialCtx, "command:fixture", DialOptions{
		AllowLocalCommands: true,
		Transport:          CommandTransport,
		AuthScheme:         CommandAuthScheme,
		BearerToken:        string(launch),
	})
	// Cancelling the dial context after DialWithOptions succeeds must NOT kill the running MCP server.
	cancel()
	if err != nil {
		t.Fatalf("DialWithOptions: %v", err)
	}
	if _, err := up.Call(context.Background(), "ping", nil); err != nil {
		_ = up.Close()
		t.Fatalf("Call after dialCtx cancel failed: %v", err)
	}
	rawPID, err := os.ReadFile(pidFile)
	if err != nil {
		_ = up.Close()
		t.Fatalf("read child pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(rawPID)))
	if err != nil || childPID <= 0 {
		_ = up.Close()
		t.Fatalf("invalid child pid %q: %v", string(rawPID), err)
	}
	if err := up.Close(); err != nil {
		// stdio leader may exit via signal; closing still must terminate the whole group.
		t.Logf("up.Close returned: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	_ = syscall.Kill(childPID, syscall.SIGKILL)
	t.Fatalf("grandchild process %d survived up.Close()", childPID)
}
