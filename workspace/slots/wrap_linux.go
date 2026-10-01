//go:build linux

package slots

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"
)

// WrapCommand turns cmd into the same command run as slot. The returned command carries the
// request on its standard input; callers must not set Stdin on it. Stdout and stderr stay the
// caller's to set. SysProcAttr's process-group flag is preserved; namespace flags move into the
// request because they must be created after the switch to the slot, not by sudo.
func WrapCommand(ctx context.Context, cmd *exec.Cmd, slot string) (*exec.Cmd, error) {
	if !ValidSlot(slot) {
		return nil, fmt.Errorf("invalid slot %q", slot)
	}
	if cmd == nil || len(cmd.Args) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	req := ExecRequest{Argv: append([]string(nil), cmd.Args...), Cwd: cmd.Dir, Env: WithSlotDocker(append([]string(nil), cmd.Env...), slot)}
	// The user's folders belong to the platform account with the slot's group, so git would refuse them as
	// "dubious ownership". The command can only reach what its folder guard grants.
	hasGit := false
	for _, e := range req.Env {
		if strings.HasPrefix(e, "GIT_CONFIG_COUNT=") {
			hasGit = true
		}
	}
	if !hasGit {
		req.Env = append(req.Env, GitSlotEnv()...)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWUSER != 0 {
		req.Userns = true
	}
	// A Landlock policy file the platform wrote cannot be read by the slot: send its content on fd 3.
	for i := 0; i+1 < len(req.Argv); i++ {
		if req.Argv[i] == "--config" && !strings.HasPrefix(req.Argv[i+1], "/proc/self/fd/") {
			policy, err := os.ReadFile(req.Argv[i+1])
			if err != nil {
				return nil, fmt.Errorf("read the sandbox policy to hand to the slot: %w", err)
			}
			req.FD3 = string(policy)
			req.Argv[i+1] = "/proc/self/fd/3"
			break
		}
	}
	argv := SudoArgv(slot)
	wrapped := exec.CommandContext(ctx, argv[0], argv[1:]...)
	wrapped.Env = []string{"PATH=/usr/bin:/bin"}
	wrapped.SysProcAttr = &syscall.SysProcAttr{Setpgid: cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid}
	// A stop must reach the slot's processes, which this process cannot signal itself: ask sudo to pass a graceful
	// signal to slotctl (a hard kill of sudo cannot be passed on and would leave the command running). The delay is
	// the last resort after slotctl's own grace period.
	wrapped.Cancel = func() error { return wrapped.Process.Signal(syscall.SIGTERM) }
	wrapped.WaitDelay = cmd.WaitDelay
	if wrapped.WaitDelay == 0 {
		wrapped.WaitDelay = StopGrace * 3
	}
	body, err := encode(req)
	if err != nil {
		return nil, err
	}
	wrapped.Stdin = bytes.NewReader(body)
	return wrapped, nil
}

// IsWrapped reports whether cmd is a command WrapCommand built (it runs as a slot through sudo).
func IsWrapped(cmd *exec.Cmd) bool {
	return cmd != nil && len(cmd.Args) >= 5 && cmd.Args[0] == DefaultSudo && cmd.Args[2] == "-u" && ValidSlot(cmd.Args[3])
}

// lookupSlotUID returns a slot account's numeric uid; a variable so tests can run without real accounts.
var lookupSlotUID = func(name string) (string, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return "", err
	}
	return u.Uid, nil
}

// WithSlotDocker returns env with DOCKER_HOST set to the slot's own Docker socket when this host gives every slot
// one (slot_docker in the slotctl config); otherwise env is unchanged. The platform's own DOCKER_HOST (its
// account's socket, in a folder only that account can open) is replaced, never passed through to a slot.
func WithSlotDocker(env []string, slot string) []string {
	cfg, err := LoadExecConfig(ConfigPath())
	if err != nil || !cfg.SlotDocker {
		return env
	}
	uid, err := lookupSlotUID(slot)
	if err != nil || uid == "" {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "DOCKER_HOST=") {
			out = append(out, entry)
		}
	}
	return append(out, "DOCKER_HOST=unix:///run/user/"+uid+"/docker.sock")
}
