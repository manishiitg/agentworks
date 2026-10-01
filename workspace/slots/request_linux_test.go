//go:build linux

package slots

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func readRequest(t *testing.T, cmd *exec.Cmd) ExecRequest {
	t.Helper()
	body, err := io.ReadAll(cmd.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var req ExecRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestWrapCommandRunsSlotctlThroughSudoAndMovesTheRequestToStdin(t *testing.T) {
	t.Setenv(EnvSlotctl, "/opt/slotctl")
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{"WorkDir":"/w"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/opt/landlock-runner", "--config", policy, "--", "/bin/sh", "-c", "echo hi")
	cmd.Dir = "/srv/work"
	cmd.Env = []string{"A=1", "B=2"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS}

	wrapped, err := WrapCommand(context.Background(), cmd, "slot07")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{DefaultSudo, "-n", "-u", "slot07", "/opt/slotctl", "exec"}
	if len(wrapped.Args) != len(want) {
		t.Fatalf("argv %v, want %v", wrapped.Args, want)
	}
	for i := range want {
		if wrapped.Args[i] != want[i] {
			t.Fatalf("argv %v, want %v", wrapped.Args, want)
		}
	}
	if wrapped.SysProcAttr.Cloneflags != 0 || !wrapped.SysProcAttr.Setpgid {
		t.Fatalf("sudo must not create the namespaces and must keep the process group: %+v", wrapped.SysProcAttr)
	}
	if len(wrapped.Env) != 1 || wrapped.Env[0] != "PATH=/usr/bin:/bin" {
		t.Fatalf("sudo must not receive the platform's environment: %v", wrapped.Env)
	}
	req := readRequest(t, wrapped)
	if !req.Userns || req.Cwd != "/srv/work" || len(req.Env) != 5 { // A, B and git's safe.directory
		t.Fatalf("request lost fields: %+v", req)
	}
	if req.FD3 != `{"WorkDir":"/w"}` {
		t.Fatalf("the policy content must travel in the request: %q", req.FD3)
	}
	for i, arg := range req.Argv {
		if arg == "--config" && req.Argv[i+1] != "/proc/self/fd/3" {
			t.Fatalf("the launcher must read its policy from fd 3, got %q", req.Argv[i+1])
		}
	}
}

func TestWrapCommandRefusesBadInput(t *testing.T) {
	cmd := exec.Command("/bin/true")
	for _, slot := range []string{"", "root", "slot01;id", "agents"} {
		if _, err := WrapCommand(context.Background(), cmd, slot); err == nil {
			t.Fatalf("slot %q must be refused", slot)
		}
	}
	if _, err := WrapCommand(context.Background(), &exec.Cmd{}, "slot01"); err == nil {
		t.Fatal("an empty command must be refused")
	}
	missing := exec.Command("/opt/landlock-runner", "--config", "/nonexistent/policy.json", "--", "/bin/true")
	if _, err := WrapCommand(context.Background(), missing, "slot01"); err == nil {
		t.Fatal("an unreadable policy must be an error, not a silently unconfined run")
	}
}
