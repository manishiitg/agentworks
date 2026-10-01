package slots

import (
	"path/filepath"
	"strings"
)

// The tmux front-end (cmd/slottmux) sits first in PATH for the platform services, so the many
// places that run `tmux ...` need no change. It decides, from the arguments alone, which server a
// command belongs to: a session started in a slot's runtime folder lives in that slot's own tmux
// server (started as the slot); every other command goes to the default server as before.

var globalFlagWithValue = map[string]bool{"-c": true, "-f": true, "-L": true, "-S": true, "-T": true}

// TmuxCommand is the parsed shape of a tmux command line.
type TmuxCommand struct {
	// Subcommand is the tmux command name as given ("new-session", "send-keys", ...), "" if none.
	Subcommand string
	// ExplicitSocket is true when the caller chose a server (-L or -S): then nothing is rerouted.
	ExplicitSocket bool
	// Rest holds the arguments after the subcommand.
	Rest []string
	// Prefix holds the global flags before the subcommand.
	Prefix []string
}

// ParseTmux splits a tmux command line.
func ParseTmux(args []string) TmuxCommand {
	var cmd TmuxCommand
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			break
		}
		if globalFlagWithValue[a] {
			if a == "-L" || a == "-S" {
				cmd.ExplicitSocket = true
			}
			cmd.Prefix = append(cmd.Prefix, a)
			if i+1 < len(args) {
				cmd.Prefix = append(cmd.Prefix, args[i+1])
			}
			i += 2
			continue
		}
		cmd.Prefix = append(cmd.Prefix, a)
		i++
	}
	if i < len(args) {
		cmd.Subcommand = args[i]
		cmd.Rest = args[i+1:]
	}
	return cmd
}

// IsNewSession reports whether the subcommand creates a session.
func (c TmuxCommand) IsNewSession() bool {
	return c.Subcommand == "new-session" || c.Subcommand == "new"
}

// IsListSessions reports whether the subcommand lists sessions.
func (c TmuxCommand) IsListSessions() bool {
	return c.Subcommand == "list-sessions" || c.Subcommand == "ls"
}

var newSessionValueFlags = map[string]bool{"-c": true, "-e": true, "-F": true, "-f": true, "-n": true, "-s": true, "-t": true, "-x": true, "-y": true}

// NewSessionFlags returns the session name (-s) and start folder (-c) of a new-session command.
func (c TmuxCommand) NewSessionFlags() (name, dir string) {
	for i := 0; i < len(c.Rest); i++ {
		a := c.Rest[i]
		if !strings.HasPrefix(a, "-") {
			break // the shell command to run starts here
		}
		if newSessionValueFlags[a] && i+1 < len(c.Rest) {
			switch a {
			case "-s":
				name = c.Rest[i+1]
			case "-c":
				dir = c.Rest[i+1]
			}
			i++
		}
	}
	return name, dir
}

// ShellCommandIndex returns the index in Rest of the shell command a new-session runs (the first word
// that is not a flag or a flag's value), or -1 when the session runs the default shell.
func (c TmuxCommand) ShellCommandIndex() int {
	for i := 0; i < len(c.Rest); i++ {
		a := c.Rest[i]
		if !strings.HasPrefix(a, "-") {
			return i
		}
		if newSessionValueFlags[a] {
			i++
		}
	}
	return -1
}

// NewSessionDetached reports whether a new-session command is detached (-d, alone or in a cluster
// such as -dP). Only detached sessions can be started for a slot: an attached one needs a terminal.
func (c TmuxCommand) NewSessionDetached() bool {
	for i := 0; i < len(c.Rest); i++ {
		a := c.Rest[i]
		if !strings.HasPrefix(a, "-") {
			return false
		}
		if newSessionValueFlags[a] {
			i++
			continue
		}
		if !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "d") {
			return true
		}
	}
	return false
}

// Target returns the session a command addresses with -t ("" when none or addressed by id).
func (c TmuxCommand) Target() string {
	for i := 0; i < len(c.Rest); i++ {
		if c.Rest[i] == "--" {
			return ""
		}
		if c.Rest[i] == "-t" && i+1 < len(c.Rest) {
			return SessionOfTarget(c.Rest[i+1])
		}
	}
	return ""
}

// SessionOfTarget reduces a tmux target ("=name", "name:0.1", "name") to the session name. Targets
// by id ("$1", "%3", "@2") name no session.
func SessionOfTarget(target string) string {
	target = strings.TrimPrefix(strings.TrimSpace(target), "=")
	if target == "" || strings.ContainsAny(target[:1], "$%@") {
		return ""
	}
	if i := strings.IndexAny(target, ":."); i >= 0 {
		target = target[:i]
	}
	return target
}

// SlotOfDir returns the slot a folder under stateRoot belongs to ("" when it is not under one).
func SlotOfDir(stateRoot, dir string) string {
	stateRoot = filepath.Clean(stateRoot)
	dir = filepath.Clean(dir)
	prefix := stateRoot + string(filepath.Separator)
	if stateRoot == "" || !strings.HasPrefix(dir, prefix) {
		return ""
	}
	first := strings.SplitN(strings.TrimPrefix(dir, prefix), string(filepath.Separator), 2)[0]
	if ValidSlot(first) {
		return first
	}
	return ""
}

// SessionFile is where the front-end remembers which slot a session belongs to, so later commands
// for it (send-keys, capture-pane, attach) reach the right server. Names are sanitised: a session
// name never forms a path.
func SessionFile(registry, session string) string {
	if session == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range session {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return filepath.Join(registry, b.String())
}

// BufferName returns the value of -b (the buffer a buffer command names), or "".
func (c TmuxCommand) BufferName() string {
	for i := 0; i < len(c.Rest); i++ {
		if c.Rest[i] == "--" {
			return ""
		}
		if c.Rest[i] == "-b" && i+1 < len(c.Rest) {
			return c.Rest[i+1]
		}
	}
	return ""
}

// LoadBufferSource returns what a load-buffer command reads: a file path, or "-" for standard input.
func (c TmuxCommand) LoadBufferSource() string {
	for i := 0; i < len(c.Rest); i++ {
		a := c.Rest[i]
		if a == "-b" || a == "-t" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		return a
	}
	return ""
}
