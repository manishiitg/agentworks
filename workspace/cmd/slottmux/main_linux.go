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
	"time"

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

// slotEnv is the environment a slot's tmux server starts with: a small fixed set and the slot's own
// identity, never the platform's environment (which holds the services' secrets). What a CLI needs
// beyond that travels in its launch script.
func slotEnv(cfg slots.ExecConfig, slot string) []string {
	keep := map[string]bool{"PATH": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true, "TERM": true, "TZ": true, "COLORTERM": true}
	env := []string{"HOME=" + filepath.Join(filepath.Dir(cfg.SlotRunRoot), "home", slot), "USER=" + slot, "LOGNAME=" + slot, "SHELL=/bin/sh"}
	for _, entry := range os.Environ() {
		if key, _, ok := strings.Cut(entry, "="); ok && keep[key] {
			env = append(env, entry)
		}
	}
	return env
}

// A paste buffer belongs to one tmux server, but `load-buffer` names no session. The front-end keeps
// the content itself and loads it into the right server when the paste says which session it is for;
// the default server gets a copy too, so sessions that are not a slot's work as before.
func bufferFile(registry, name string) string {
	return slots.SessionFile(filepath.Join(registry, "buffers"), name)
}

func loadBuffer(cfg slots.ExecConfig, registry string, c slots.TmuxCommand, args []string) int {
	source := c.LoadBufferSource()
	var data []byte
	var err error
	if source == "-" || source == "" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(source)
	}
	if err != nil {
		_, _ = io.WriteString(os.Stderr, "tmux: cannot read the buffer: "+err.Error()+"\n")
		return 1
	}
	if err := os.MkdirAll(filepath.Join(registry, "buffers"), 0o700); err == nil {
		if file := bufferFile(registry, c.BufferName()); file != "" {
			_ = os.WriteFile(file, data, 0o600)
		}
	}
	sweepBuffers(filepath.Join(registry, "buffers"))
	return 0
}

// sweepBuffers drops paste content left behind by a paste that never came (a loaded buffer is deleted by
// the paste that uses it).
func sweepBuffers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// onStdin rewrites a load-buffer command to read its content from standard input.
func onStdin(args []string) []string {
	c := slots.ParseTmux(args)
	out := append([]string{}, c.Prefix...)
	out = append(out, c.Subcommand)
	for i := 0; i < len(c.Rest); i++ {
		if c.Rest[i] == "-b" && i+1 < len(c.Rest) {
			out = append(out, "-b", c.Rest[i+1])
			i++
		}
	}
	return append(out, "-")
}

func pasteBuffer(cfg slots.ExecConfig, registry, slot string, c slots.TmuxCommand, args []string) int {
	sock := slots.SlotSocket(cfg.SlotRunRoot, slot)
	if file := bufferFile(registry, c.BufferName()); file != "" {
		if data, err := os.ReadFile(file); err == nil {
			ld := exec.Command(slots.TmuxPath, "-S", sock, "load-buffer", "-b", c.BufferName(), "-")
			ld.Stdin = bytes.NewReader(data)
			if out, err := ld.CombinedOutput(); err != nil {
				_, _ = os.Stderr.Write(out)
				return exitCode(err)
			}
		}
	}
	code := exitCode(exec.Command(slots.TmuxPath, onSocket(sock, args)...).Run())
	if hasDelete(c) {
		_ = os.Remove(bufferFile(registry, c.BufferName()))
	}
	return code
}

// pasteDefault loads the stored content into the default server only now, when a paste for a session
// that is not a slot's says where it goes, and removes it after a deleting paste.
func pasteDefault(registry string, c slots.TmuxCommand, args []string) int {
	file := bufferFile(registry, c.BufferName())
	if data, err := os.ReadFile(file); err == nil {
		ld := exec.Command(slots.TmuxPath, "load-buffer", "-b", c.BufferName(), "-")
		ld.Stdin = bytes.NewReader(data)
		if out, err := ld.CombinedOutput(); err != nil {
			_, _ = os.Stderr.Write(out)
			return exitCode(err)
		}
	}
	code := exitCode(exec.Command(slots.TmuxPath, args...).Run())
	if hasDelete(c) {
		_ = os.Remove(file)
	}
	return code
}

func hasDelete(c slots.TmuxCommand) bool {
	for _, a := range c.Rest {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "d") && a != "-b" && a != "-t" {
			return true
		}
	}
	return false
}

// commandUsesSlotFolder reports whether the pane's command refers to a file in the slot's run folder.
func commandUsesSlotFolder(c slots.TmuxCommand, runDir string) bool {
	idx := c.ShellCommandIndex()
	if idx < 0 {
		return false
	}
	return strings.Contains(strings.Join(c.Rest[idx:], " "), runDir+"/")
}

// withLaunchLog makes the pane's program write its error output to log, for both tmux command forms.
func withLaunchLog(c slots.TmuxCommand, args []string, log string) []string {
	idx := c.ShellCommandIndex()
	if idx < 0 {
		return args
	}
	rest := append([]string(nil), c.Rest[:idx]...)
	tail := c.Rest[idx:]
	if len(tail) == 1 {
		// one string: tmux runs it through a shell
		rest = append(rest, "exec 2>"+shellQuote(log)+"; "+tail[0])
	} else {
		// several words: tmux runs them directly, so keep them as arguments of a small shell
		rest = append(rest, "/bin/sh", "-c", "exec 2>"+shellQuote(log)+`; exec "$@"`, "slot-launch")
		rest = append(rest, tail...)
	}
	return append(append(append([]string(nil), c.Prefix...), c.Subcommand), rest...)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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
		if slot == "" && name != "" && c.NewSessionDetached() && strings.Contains(name, "muse") {
			// Not a slot's session: Muse's launch error output is still kept (diagnostics for a Muse that
			// dies at start), under the registry, one file per session; other CLIs are left alone.
			dir := filepath.Join(registry, "launch-logs")
			if os.MkdirAll(dir, 0o700) == nil {
				sweepBuffers(dir)
				if file := slots.SessionFile(dir, name); file != "" {
					args = withLaunchLog(c, args, file)
				}
			}
			return passthrough(args)
		}
		if slot == "" || name == "" || !c.NewSessionDetached() {
			return passthrough(args)
		}
		// Only a launch prepared for the slot runs as the slot: its script lives in the slot's run folder.
		// A session whose command was prepared by the platform for itself (a user whose CLIs do not run as
		// a slot) is not readable by the slot and stays on the platform's own tmux.
		if !commandUsesSlotFolder(c, filepath.Join(cfg.SlotRunRoot, slot)) {
			return passthrough(args)
		}
		sock := slots.SlotSocket(cfg.SlotRunRoot, slot)
		// Keep the pane's error output in the slot's run folder: a CLI that fails to start otherwise
		// takes its reason with it when the session ends.
		args = withLaunchLog(c, args, filepath.Join(cfg.SlotRunRoot, slot, "last-launch.stderr"))
		code := asSlot(cfg, slot, args, slotEnv(cfg, slot), os.Stdout, os.Stderr)
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

	case c.Subcommand == "load-buffer" && c.Target() == "" && c.BufferName() != "":
		return loadBuffer(cfg, registry, c, args)

	case c.Subcommand == "delete-buffer" && c.BufferName() != "":
		_ = os.Remove(bufferFile(registry, c.BufferName()))
		return 0

	case c.Subcommand == "paste-buffer" && c.BufferName() != "":
		if slot := lookup(registry, c.Target()); slot != "" {
			return pasteBuffer(cfg, registry, slot, c, args)
		}
		return pasteDefault(registry, c, args)

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
