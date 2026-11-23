//go:build !windows

package procshutdown

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A launcher that starts the real CLI as a child (the npm `codex` wrapper does) must not leave that child
// running when the turn is cancelled. Real processes: a shell launcher with a long-running child.
func TestKillGroupOnCancelStopsTheLaunchersChild(t *testing.T) {
	for _, withHelper := range []bool{false, true} {
		pidFile := filepath.Join(t.TempDir(), "child.pid")
		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `sleep 300 & echo $! > "$1"; wait`, "launcher", pidFile)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if withHelper {
			KillGroupOnCancel(cmd)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		child := waitForPID(t, pidFile)
		cancel()
		_ = cmd.Wait()

		alive := false
		deadline := time.Now().Add(3 * time.Second)
		for {
			alive = syscall.Kill(child, 0) == nil
			if !alive || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if alive {
			_ = syscall.Kill(child, syscall.SIGKILL) // do not leave the orphan behind
		}
		if withHelper && alive {
			t.Fatal("the launcher's child kept running after cancel")
		}
		if !withHelper && !alive {
			t.Fatal("expected the default CommandContext cancel to leave the child running (the bug this pins)")
		}
	}
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("launcher never wrote its child pid")
	return 0
}
