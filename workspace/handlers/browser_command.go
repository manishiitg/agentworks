package handlers

import "strings"

// isStandaloneBrowserCommand reports whether command is exactly one agent-browser invocation and nothing
// else: the first word is `agent-browser`, and no unquoted shell operator (; & | < > ( ) newline), command
// substitution ($( or a backtick outside single quotes) or second command can ride along. Quoted arguments
// (JavaScript for `eval`, text to type) may contain anything but substitution.
//
// Why it matters: the browser tool starts its daemon and Chrome from inside the command and the platform manages
// them (profile folders, the live view, restarts), so a browser command runs as the service account like it did
// before per-user accounts; as a slot it could not write the profile folders or be stopped by the platform
// ("Browser restarted - reconnecting" in a loop on RTS, "Permission denied" on Confida, 2026-10-01). Only this
// exact shape is exempted from the slot, so adding `agent-browser` to a longer command does not escape it.
func isStandaloneBrowserCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	const prog = "agent-browser"
	if !strings.HasPrefix(command, prog) {
		return false
	}
	rest := command[len(prog):]
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return false // agent-browser-something, agent-browser.sh, ...
	}
	var quote byte // 0, '\'' or '"'
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			}
		case '"':
			switch c {
			case '"':
				quote = 0
			case '\\':
				i++ // an escaped character, including \" and \$
			case '`':
				return false
			case '$':
				if i+1 < len(rest) && (rest[i+1] == '(' || rest[i+1] == '{') {
					return false
				}
			}
		default:
			switch c {
			case '\'', '"':
				quote = c
			case '\\':
				i++
			case ';', '&', '|', '<', '>', '(', ')', '\n', '\r', '`':
				return false
			case '$':
				if i+1 < len(rest) && (rest[i+1] == '(' || rest[i+1] == '{') {
					return false
				}
			}
		}
	}
	return quote == 0
}
