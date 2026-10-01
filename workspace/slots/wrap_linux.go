//go:build linux

package slots

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
	req := ExecRequest{Argv: append([]string(nil), cmd.Args...), Cwd: cmd.Dir, Env: append([]string(nil), cmd.Env...)}
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
	wrapped.WaitDelay = cmd.WaitDelay
	body, err := encode(req)
	if err != nil {
		return nil, err
	}
	wrapped.Stdin = bytes.NewReader(body)
	return wrapped, nil
}
