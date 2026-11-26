//go:build !windows

package slotfs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// PLAT-872-119: the outer sudo must receive TERM, not a group KILL, so
// slotctl has a chance to release the native session in its own child group.
func TestWrappedCancelAllowsSlotSupervisorToHandleStop(t *testing.T) {
	state, _ := setup(t)
	root := t.TempDir()
	ready, stopped := filepath.Join(root, "ready"), filepath.Join(root, "stopped")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/true")
	cmd.Cancel = func() error { return cmd.Process.Kill() } // provider's original hard cancel
	cleanup, err := WrapCmd(cmd, filepath.Join(state, "slot05", "cli"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// Replace only the external sudo/slotctl boundary with a supervisor which
	// records TERM. This is not a live cross-uid or installed-slotctl test.
	cmd.Path = "/bin/sh"
	cmd.Args = []string{"sh", "-c", `trap 'echo stopped > "$2"; exit 0' TERM; echo ready > "$1"; while :; do sleep 0.05; done`, "supervisor", ready, stopped}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			_ = cmd.Wait()
			t.Fatal("supervisor never ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	_ = cmd.Wait()
	if _, err := os.Stat(stopped); err != nil {
		t.Fatal("cancel killed supervisor before it could forward Stop")
	}
}
