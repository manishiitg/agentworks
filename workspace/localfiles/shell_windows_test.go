//go:build windows

package localfiles

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// On Windows commands run through Git Bash with the user's permissions (no sandbox): the command sees its working folder,
// reports its exit code, and gets only the filtered environment (system variables, no secrets).
func TestWindowsShellRunsThroughGitBash(t *testing.T) {
	if findGitBash() == "" {
		t.Fatal("Git for Windows is expected on this machine")
	}
	t.Setenv("AGENTWORKS_TOKEN", "must-not-reach-commands")
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd, cleanup, err := windowsShellCommand(ctx, dir, `pwd -W; echo "token=[$AGENTWORKS_TOKEN]"; echo "root=[$SYSTEMROOT]"; exit 3`)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	cmd.Env = shellEnvironment(cmd.Env)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	kill := configureShellProcess(cmd)
	defer kill()
	err = runShellCommand(cmd)
	var exit *exec.ExitError
	if err == nil || !asExit(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("expected exit code 3, got %v\n%s", err, out.String())
	}
	got := strings.ReplaceAll(strings.ToLower(out.String()), "\\", "/")
	if !strings.Contains(got, strings.ToLower(strings.ReplaceAll(dir, "\\", "/"))) {
		t.Fatalf("the command must run in its working folder %s:\n%s", dir, out.String())
	}
	if strings.Contains(out.String(), "must-not-reach-commands") || !strings.Contains(out.String(), "token=[]") {
		t.Fatalf("secrets must not reach commands:\n%s", out.String())
	}
	if strings.Contains(out.String(), "root=[]") {
		t.Fatalf("system variables must reach commands:\n%s", out.String())
	}
}

func asExit(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}
