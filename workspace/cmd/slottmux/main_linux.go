//go:build linux

// slottmux is the `tmux` the platform services find first in PATH. A session started in a slot's
// runtime folder is created by that slot's own tmux server (run as the slot through sudo and
// slotctl) and later commands for it are sent to that server's socket. Everything else goes to the
// normal tmux unchanged, as does anything run by a slot account itself.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

func main() { os.Exit(run(os.Args[1:])) }

func passthrough(args []string) int {
	err := syscall.Exec(slots.TmuxPath, append([]string{"tmux"}, args...), os.Environ())
	_, _ = io.WriteString(os.Stderr, "tmux: cannot start "+slots.TmuxPath+": "+err.Error()+"\n")
	return 127
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return 127
}

// asSlot runs a tmux command as the slot through sudo and slotctl, returning its output.
func asSlot(cfg slots.ExecConfig, slot string, tmuxArgs []string, env []string, stdout, stderr io.Writer) int {
	req := slots.ExecRequest{
		Argv: append([]string{slots.TmuxPath, "-S", slots.SlotSocket(cfg.SlotRunRoot, slot)}, tmuxArgs...),
		Cwd:  filepath.Join(cfg.SlotRunRoot, slot),
		Env:  env,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return 127
	}
	argv := slots.SudoArgv(slot)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.Stdin = bytes.NewReader(body)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return exitCode(cmd.Run())
}

// runAsSlot runs an allowed program (not tmux) as the slot through sudo and slotctl.
func runAsSlot(cfg slots.ExecConfig, slot string, argv []string) int {
	body, err := json.Marshal(slots.ExecRequest{Argv: argv, Cwd: filepath.Join(cfg.SlotRunRoot, slot), Env: []string{"PATH=/usr/bin:/bin"}})
	if err != nil {
		return 127
	}
	sudo := slots.SudoArgv(slot)
	cmd := exec.Command(sudo[0], sudo[1:]...)
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Dir = "/"
	cmd.Stdin = bytes.NewReader(body)
	return exitCode(cmd.Run())
}

func onSocket(sock string, args []string) []string {
	c := slots.ParseTmux(args)
	out := append([]string{}, c.Prefix...)
	out = append(out, "-S", sock)
	if c.Subcommand != "" {
		out = append(out, c.Subcommand)
	}
	return append(out, c.Rest...)
}

func lookup(registry, session string) string {
	file := slots.SessionFile(registry, session)
	if file == "" {
		return ""
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	slot := strings.TrimSpace(string(data))
	if !slots.ValidSlot(slot) {
		return ""
	}
	return slot
}

func remember(registry, session, slot string) {
	file := slots.SessionFile(registry, session)
	if file == "" {
		return
	}
	if err := os.MkdirAll(registry, 0o700); err == nil {
		_ = os.WriteFile(file, []byte(slot+"\n"), 0o600)
	}
}

func sockets(cfg slots.ExecConfig) map[string]string {
	found := map[string]string{}
	matches, _ := filepath.Glob(filepath.Join(cfg.SlotRunRoot, "slot*", "tmux.sock"))
	for _, m := range matches {
		slot := filepath.Base(filepath.Dir(m))
		if slots.ValidSlot(slot) {
			found[slot] = m
		}
	}
	return found
}

func run(args []string) int {
	cfg, err := slots.LoadExecConfig(slots.DefaultSlotctlConfig)
	if err != nil || cfg.SlotRunRoot == "" || cfg.SlotStateRoot == "" {
		return passthrough(args) // slots are not set up on this host
	}
	me, err := user.Current()
	if err != nil || slots.ValidSlot(me.Username) {
		return passthrough(args) // a slot's own tmux is plain tmux
	}
	c := slots.ParseTmux(args)
	if c.ExplicitSocket || c.Subcommand == "" {
		return passthrough(args)
	}
	registry := filepath.Join(cfg.SlotRunRoot, ".sessions")

	switch {
	case c.IsNewSession():
		name, dir := c.NewSessionFlags()
		slot := cfg.SlotForDir(dir)
		if slot == "" || name == "" || !c.NewSessionDetached() {
			return passthrough(args)
		}
		sock := slots.SlotSocket(cfg.SlotRunRoot, slot)
		code := asSlot(cfg, slot, args, os.Environ(), os.Stdout, os.Stderr)
		if code != 0 {
			return code
		}
		// tmux makes its socket owner-only, so open it to the slot's group (the platform is a member), then
		// let the platform account talk to it (tmux only accepts its own user otherwise). Always done:
		// a stale socket file from an earlier server must not be taken for a configured one.
		_ = runAsSlot(cfg, slot, []string{slots.ChmodPath, "660", sock})
		_ = asSlot(cfg, slot, []string{"server-access", "-aw", me.Username}, []string{"PATH=/usr/bin:/bin"}, io.Discard, io.Discard)
		remember(registry, name, slot)
		return 0

	case c.IsListSessions():
		return listAll(cfg, args)

	default:
		target := c.Target()
		if target == "" {
			return passthrough(args)
		}
		if slot := lookup(registry, target); slot != "" {
			return direct(slots.SlotSocket(cfg.SlotRunRoot, slot), args)
		}
		return passthrough(args)
	}
}

// direct runs tmux as this account against a slot's socket (the platform is in the slot's group and
// was granted access by the server).
func direct(sock string, args []string) int {
	return passthrough(onSocket(sock, args))
}

func listAll(cfg slots.ExecConfig, args []string) int {
	var out bytes.Buffer
	status := 1
	run := func(argv []string) {
		cmd := exec.Command(slots.TmuxPath, argv...)
		cmd.Env = os.Environ()
		var buf bytes.Buffer
		cmd.Stdout = &buf
		if cmd.Run() == nil {
			status = 0
			out.Write(buf.Bytes())
		}
	}
	run(args)
	for _, sock := range sockets(cfg) {
		run(onSocket(sock, args))
	}
	_, _ = os.Stdout.Write(out.Bytes())
	if status != 0 {
		_, _ = io.WriteString(os.Stderr, "no server running\n")
	}
	return status
}
