package musecli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const museRunningToolPane = `◆ Used sleep 240; inspect outputs — running (2m 43s · esc to interrupt)
──────────────────────────────
❯
──────────────────────────────
  muse-spark-1.3-contributor · xhigh · /tmp/work
`

func TestMuseRunningToolIsNotCompletedP0(t *testing.T) {
	if museTUIAtPrompt(museRunningToolPane) {
		t.Fatal("a frozen running tool must not count as a completed turn")
	}
	if !museTUIAtPrompt(museRunningToolPane + "\n◆ Finished successfully.\n❯\necho · /tmp/work\n") {
		t.Fatal("historical running text must not hide a later completed response")
	}
}

// Real tmux, synthetic tool output, no model or workflow side effects:
// readiness must stay cancellable while a previous tool is still running.
func TestMuseLongRunningToolWaitTmuxP0(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "busy"), []byte(museRunningToolPane), 0600); err != nil {
		t.Fatal(err)
	}
	session := "mlp-muse-lifetime-test-" + museRandomSessionSuffix()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", session,
		"-x", "160", "-y", "30", "-c", dir, "sh", "-c", "cat busy; exec sleep 30")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("launch scratch terminal: %v %s", err, out)
	}
	defer museKillTmuxSession(context.Background(), session)
	for {
		pane, err := museTmuxCapturePane(ctx, session)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pane, "esc to interrupt") {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}

	readyCtx, readyCancel := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err := museWaitAtPrompt(readyCtx, session, 0)
	readyCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("readiness must honor caller deadline: %v", err)
	}
}
