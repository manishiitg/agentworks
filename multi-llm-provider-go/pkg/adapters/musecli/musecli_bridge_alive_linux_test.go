//go:build linux

package musecli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The /proc reader against real processes: a shell with a child named mcpbridge, then the same shell without it.
func TestMuseReadProcessTableSeesARealBridgeChild(t *testing.T) {
	dir := t.TempDir()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	bridge := filepath.Join(dir, "mcpbridge")
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bridge, data, 0o755); err != nil {
		t.Fatal(err)
	}
	pane := exec.Command("/bin/sh", "-c", bridge+" 30 & wait")
	pane.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := pane.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pane.Process.Pid, syscall.SIGKILL); _ = pane.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		procs, ok := museReadProcessTable()
		if ok && museHasDescendantNamed(pane.Process.Pid, procs, "mcpbridge") {
			// kill only the bridge: the pane (shell) stays, as in a Muse whose bridge died
			for pid, p := range procs {
				if p.ppid == pane.Process.Pid && p.comm == "mcpbridge" {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			for time.Now().Before(deadline) {
				after, _ := museReadProcessTable()
				if !museHasDescendantNamed(pane.Process.Pid, after, "mcpbridge") {
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			t.Fatal("the bridge was killed but is still reported below the pane")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the bridge child of a real pane was never found in the process table")
}

// museBridgeAlive end to end with a real tmux session: a pane whose child is named mcpbridge reads as alive, and
// once only that child is killed (the pane stays, as in a Muse that lost its bridge) it reads as gone.
func TestMuseBridgeAliveWithARealTmuxPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("no tmux")
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep binary")
	}
	dir := t.TempDir()
	t.Setenv("TMUX_TMPDIR", dir)
	bridge := filepath.Join(dir, "mcpbridge")
	data, err := os.ReadFile(sleep)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bridge, data, 0o755); err != nil {
		t.Fatal(err)
	}
	const name = "muse-bridge-alive-test"
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, "/bin/sh", "-c", bridge+" 60 & wait; sleep 60").CombinedOutput(); err != nil {
		t.Skipf("tmux cannot start here: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-server").Run() })

	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if alive, known := museBridgeAlive(ctx, name); known && alive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	alive, known := museBridgeAlive(ctx, name)
	if !known || !alive {
		t.Fatalf("a pane with a bridge child must read as alive (alive=%v known=%v)", alive, known)
	}
	procs, _ := museReadProcessTable()
	for pid, p := range procs {
		if p.comm == "mcpbridge" {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	for time.Now().Before(deadline.Add(5 * time.Second)) {
		if alive, known := museBridgeAlive(ctx, name); known && !alive {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("after the bridge was killed the pane must read as gone while its session stays up")
}
