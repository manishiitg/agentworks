//go:build linux

package codexcli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-872-55 T6: the actual terminal launcher must create the CLI process
// as its assigned Linux account. Run only inside the disposable slot harness.
func TestCodexTmuxLaunchRunsAsAssignedSlotLive(t *testing.T) {
	dir := os.Getenv("AGENTWORKS_SLOT_PROBE_DIR")
	expected := os.Getenv("AGENTWORKS_SLOT_PROBE_UID")
	if dir == "" || expected == "" {
		t.Skip("requires disposable Linux slot harness")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session := "mlp-codexcli-slot-proof"
	marker := filepath.Join(dir, "codexcli-uid")
	version := filepath.Join(dir, "codexcli-version")
	args := []string{"/bin/sh", "-c", "/usr/bin/id -u > " + marker + "; codex --version > " + version + "; sleep 20"}
	cleanup, err := startCodexTmuxSession(ctx, session, args, dir, nil, nil, []string{"PATH=" + os.Getenv("PATH")}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer exec.Command("tmux", "kill-session", "-t", session).Run()
	for {
		uid, err := os.ReadFile(marker)
		v, versionErr := os.ReadFile(version)
		if err == nil && versionErr == nil && strings.TrimSpace(string(v)) != "" {
			if strings.TrimSpace(string(uid)) != expected {
				t.Fatalf("pane uid %q expected %s", uid, expected)
			}
			t.Logf("CLI exec uid=%s; expected slot uid=%s; service uid=%d; %s", strings.TrimSpace(string(uid)), expected, os.Getuid(), strings.TrimSpace(string(v)))
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
