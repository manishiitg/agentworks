//go:build linux

package slotcheck

import (
	"bytes"
	"context"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// RunThroughShellTool runs `pwd` for a probe exactly as the workspace shell handler runs a folder-guarded command for
// a slot user: the same Isolator (grant builder, Landlock policy, launcher), wrapped by slots.WrapCommand into
// sudo + slotctl. It does not pre-create write folders (the handler's prepareGuardWriteDirectory): the probe folders
// exist already, and the self-test changes nothing.
func RunThroughShellTool(ctx context.Context, probe Probe) (string, string, error) {
	return runThroughShellTool(ctx, probe, "pwd")
}

// RunCommandThroughShellTool is RunThroughShellTool for one command (the confinement checks).
func RunCommandThroughShellTool(ctx context.Context, probe Probe, command string) (string, string, error) {
	return runThroughShellTool(ctx, probe, command)
}

// runThroughShellTool runs one fixed command for a probe (the self-test only ever runs `pwd`; the Linux container
// tests also run negative reads through the same chain).
func runThroughShellTool(ctx context.Context, probe Probe, command string) (string, string, error) {
	iso := &security.Isolator{
		Slot:           probe.Slot,
		UserHome:       probe.UserHome,
		ReadPaths:      probe.ReadPaths,
		WritePaths:     probe.WritePaths,
		WorkDir:        probe.Dir,
		BaseDir:        probe.DocsRoot,
		AllowNetwork:   true,
		BrowserSession: probe.BrowserSession,
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, cleanup, err := iso.ExecuteIsolated(ctx, command, nil)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return "", "", err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

// LookupAccount reads a slot account from the system's account database.
func LookupAccount(name string) (Account, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return Account{}, false
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return Account{}, false
	}
	account := Account{Name: name, UID: uid, Home: u.HomeDir}
	if ids, err := u.GroupIds(); err == nil {
		for _, id := range ids {
			if gid, err := strconv.Atoi(id); err == nil {
				account.GIDs = append(account.GIDs, gid)
			}
		}
	}
	return account, true
}

// TmuxControlAsSlot runs `tmux -S <socket> <args...>` as the slot through sudo + slotctl, like the platform's tmux
// front-end. The slot's login shell is nologin, so tmux gets SHELL=/bin/sh to run a session's command.
func TmuxControlAsSlot(ctx context.Context, slot, socket string, args ...string) (string, error) {
	cfg, err := slots.LoadExecConfig(slots.ConfigPath())
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, slots.TmuxPath, append([]string{"-S", socket}, args...)...)
	cmd.Dir = filepath.Dir(socket)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SHELL=/bin/sh", "HOME=" + cfg.SlotStateDir(slot)}
	wrapped, err := slots.WrapCommand(ctx, cmd, slot)
	if err != nil {
		return "", err
	}
	out, err := wrapped.CombinedOutput()
	return string(out), err
}
