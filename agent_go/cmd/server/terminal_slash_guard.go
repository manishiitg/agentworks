package server

import (
	"os"
	"strings"
	"sync"
)

// Typed slash commands in the browser terminal are limited to an allowlist (default: /usage).
// Slash commands change the CLI's own settings or run large commands, both of which the app offers
// itself; the session-switching ones (/new, /clear, /resume, /fork) leave the chat reading a session
// the CLI has left; and any slash command leaves a draft the CLI never records (Confida,
// 2026-09-30). /usage stays because people need to see their limits.
//
// The CLI's slash menu can be driven without typing a name: "/", then the arrow keys, then Enter.
// So the guard follows the line as it is typed. Once a line starts with "/", it forwards what is
// typed (the person sees the menu filter), drops menu navigation (arrows, Tab), and only lets Enter
// through when the full typed name is on the allowlist. Otherwise it drops the Enter and erases the
// line. The chat's own commands are pasted by the platform on another path and are unaffected.

// terminalSlashCommandsEnv is a comma-separated allowlist of command names (no slash). "allow"
// permits every slash command, "none" permits none. Unset means "usage".
const terminalSlashCommandsEnv = "AGENTWORKS_TERMINAL_SLASH_COMMANDS"

const defaultTerminalSlashCommands = "usage"

type slashDecision int

const (
	slashForward slashDecision = iota // pass the bytes through
	slashDrop                         // discard the bytes
	slashCancel                       // discard the bytes and erase the typed command line
)

type slashLine struct {
	typed   int    // printable characters typed on this line so far
	command bool   // the line started with "/"
	buf     []byte // the command line typed so far, including the slash
}

type slashGuard struct {
	mu    sync.Mutex
	lines map[string]*slashLine
}

var terminalSlashGuard = &slashGuard{lines: map[string]*slashLine{}}

// forget drops the typed-line state of a session that ended.
func (g *slashGuard) forget(session string) {
	g.mu.Lock()
	delete(g.lines, session)
	g.mu.Unlock()
}

// slashPolicy resolves the allowlist from the environment.
func slashPolicy() (allowAll bool, allowed map[string]bool) {
	value := strings.TrimSpace(os.Getenv(terminalSlashCommandsEnv))
	if value == "" {
		value = defaultTerminalSlashCommands
	}
	if strings.EqualFold(value, "allow") {
		return true, nil
	}
	allowed = map[string]bool{}
	if strings.EqualFold(value, "none") {
		return false, allowed
	}
	for _, name := range strings.Split(value, ",") {
		if name = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "/"))); name != "" {
			allowed[name] = true
		}
	}
	return false, allowed
}

func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return len(data) > 0
}

// decide classifies one chunk of raw terminal input. On slashCancel the second result is how many
// characters to erase (backspaces to send).
func (g *slashGuard) decide(session string, data []byte) (slashDecision, int) {
	return g.decideWith(session, data, false)
}

// decideStrict is decide for a usage terminal, where the person only reads limits: besides the slash-command rules, every
// line that does not start with "/" is dropped, typed or pasted, so nothing in it can be a prompt to the model (a prompt would
// run on the shared account outside the person's model and token limits). Navigation keys, Enter and one-line slash commands
// on the allowlist still go through.
func (g *slashGuard) decideStrict(session string, data []byte) (slashDecision, int) {
	return g.decideWith(session, data, true)
}

func (g *slashGuard) decideWith(session string, data []byte, strict bool) (slashDecision, int) {
	allowAll, allowed := slashPolicy()
	if (allowAll && !strict) || len(data) == 0 {
		return slashForward, 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	line := g.lines[session]
	if line == nil {
		if len(g.lines) > 4096 { // per-terminal line state is tiny; never let it grow without bound
			g.lines = map[string]*slashLine{}
		}
		line = &slashLine{}
		g.lines[session] = line
	}
	reset := func() { *line = slashLine{} }

	// A paste, bracketed or not: text starting with "/" on an empty line is a command line too.
	text := data
	if len(data) > 6 && string(data[:6]) == "\x1b[200~" {
		text = data[6:]
		text = []byte(strings.TrimSuffix(string(text), "\x1b[201~"))
	}
	if strict {
		pasted := len(data) > 6 && string(data[:6]) == "\x1b[200~"
		slashStart := line.command || (line.typed == 0 && len(text) > 0 && text[0] == '/')
		switch {
		case pasted && (!slashStart || !isPrintableASCII(text)):
			return slashDrop, 0 // only a one-line slash command may be pasted
		case !pasted && len(data) > 1 && data[0] != 0x1b && !isPrintableASCII(data):
			return slashDrop, 0 // multi-byte or multi-line text that is not an escape sequence
		}
	}
	if len(text) > 1 && isPrintableASCII(text) {
		if strict && !line.command && !(line.typed == 0 && text[0] == '/') {
			return slashDrop, 0 // free text
		}
		if line.command {
			line.buf = append(line.buf, text...)
		} else if line.typed == 0 && text[0] == '/' {
			line.command, line.buf = true, append([]byte(nil), text...)
		}
		line.typed += len(text)
		return slashForward, 0
	}

	if len(data) > 1 { // an escape sequence: arrows, Tab-like keys, function keys, terminal replies
		if line.command {
			return slashDrop, 0 // no picking from the slash menu with the arrow keys
		}
		return slashForward, 0
	}

	b := data[0]
	switch {
	case b == 0x03 || b == 0x1b: // Ctrl-C, Escape: the line is abandoned
		reset()
		return slashForward, 0
	case b == '\r' || b == '\n':
		if !line.command {
			reset()
			return slashForward, 0
		}
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(string(line.buf), "/")))
		erase := len(line.buf)
		reset()
		if allowAll || allowed[name] {
			return slashForward, 0
		}
		return slashCancel, erase
	case b == 0x7f || b == 0x08:
		if line.command {
			if len(line.buf) > 0 {
				line.buf = line.buf[:len(line.buf)-1]
			}
			if len(line.buf) == 0 {
				line.command = false
			}
		}
		if line.typed > 0 {
			line.typed--
		}
		return slashForward, 0
	case b >= 0x20 && b <= 0x7e:
		if strict && !line.command && !(line.typed == 0 && b == '/') {
			return slashDrop, 0 // free text
		}
		if line.command {
			line.buf = append(line.buf, b)
		} else if line.typed == 0 && b == '/' {
			line.command, line.buf = true, []byte{'/'}
		}
		line.typed++
		return slashForward, 0
	default: // Tab and other control bytes
		if line.command {
			return slashDrop, 0
		}
		return slashForward, 0
	}
}
