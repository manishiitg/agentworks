//go:build !windows

package claudecode

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// PLAT-746-859: losing the in-memory registry must not leave a second CLI for
// the same chat. Use real tmux panes and processes, with no model or login.
func TestClaudeColdRegistryClosesOnlySameChatTmuxCopies(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	dir, err := os.MkdirTemp("/tmp", "claude-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// This server was created only for this test, never the user's tmux.
	defer exec.Command("tmux", "kill-server").Run()
	owner := "test-chat"
	other := newTmuxSessionName()
	start := func(name, taggedOwner string) int {
		t.Helper()
		// Direct argv avoids the verifier account's non-login default shell.
		run("new-session", "-d", "-s", name, "/bin/sleep", "60")
		if taggedOwner != "" {
			run("set-option", "-t", name, tmuxinput.OwnerSessionOption, taggedOwner)
		}
		pid, err := strconv.Atoi(run("list-panes", "-t", name, "-F", "#{pane_pid}"))
		if err != nil {
			t.Fatal(err)
		}
		return pid
	}
	otherPIDs := []int{start(other, "another-chat"), start(newTmuxSessionName(), ""), start("mlp-other-provider-live", owner)}
	oldPIDs := []int{start(newTmuxSessionName(), owner), start(newTmuxSessionName(), owner)}
	for i := 0; i < 2; i++ {
		// No persistent registry entries exist, as after a backend restart.
		closePreviousClaudeTmuxCopies(ctx, owner)
		for _, pid := range oldPIDs {
			if err := syscall.Kill(pid, 0); err == nil {
				t.Fatalf("previous chat process %d survived cold acquisition", pid)
			}
		}
		for _, pid := range otherPIDs {
			if err := syscall.Kill(pid, 0); err != nil {
				t.Fatalf("unrelated process %d was stopped: %v", pid, err)
			}
		}
		oldPIDs = []int{start(newTmuxSessionName(), owner)}
		names := strings.Fields(run("list-sessions", "-F", "#{session_name}"))
		if len(names) != 4 {
			t.Fatalf("reopen %d left %d sessions; want one for the reopened chat and three unrelated: %v", i, len(names), names)
		}
	}
}
