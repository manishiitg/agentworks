package tmuxcapture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCaptureAgentTailRealTmux(t *testing.T) {
	if os.Getenv("RUN_TMUX_INTEGRATION") != "1" {
		t.Skip("set RUN_TMUX_INTEGRATION=1 to run the real tmux capture")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	session := fmt.Sprintf("mlp-capture-real-%d", time.Now().UnixNano())
	marker := "SHARED_TMUX_CAPTURE_OK"
	command := fmt.Sprintf("printf '\\033[32m%s\\033[0m\\n'; sleep 30", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session, command).CombinedOutput(); err != nil {
		t.Fatalf("start tmux session: %v: %s", err, output)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cleanupCancel()
		_ = exec.CommandContext(cleanupCtx, "tmux", "kill-session", "-t", session).Run()
	})

	var snapshot Snapshot
	var err error
	for ctx.Err() == nil {
		snapshot, err = (Capturer{}).CaptureAgentTail(ctx, session, Options{})
		if err == nil && strings.Contains(snapshot.Text, marker) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("capture real tmux pane: %v", err)
	}
	if !strings.Contains(snapshot.Text, marker) || strings.Contains(snapshot.Text, "\x1b[") {
		t.Fatalf("real tmux snapshot = %#v", snapshot)
	}
}
