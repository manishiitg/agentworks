package server

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

const providerUsageInputLimit = 512

// Xterm generates these bytes itself, including when it replays old output.
// Usage queries are answered by the server; focus reports are not user keys.
var providerUsageProtocolInput = regexp.MustCompile(`\x1b\[(?:[IO]|[?>]?[0-9;]*c|[0-9]+;[0-9]+R)|\x1b\](?:10|11|4);[^\x07\x1b]*(?:\x07|\x1b\\)`)

// Account setup and session changes belong in their existing screens, not in
// an account's usage window. Read commands such as /usage, /status and /help
// are accepted, as is short ordinary text (which may use the account's model).
var providerUsageBlockedCommands = []string{
	"login", "logout", "auth", "config", "settings", "model", "models",
	"permissions", "mcp", "plugin", "plugins", "new", "clear", "resume",
	"fork", "rewind", "reset", "init", "add-dir", "connect", "disconnect",
}

type providerUsageInput struct {
	line   []rune
	cursor int
}

func (g *providerUsageInput) blocked() bool {
	fields := strings.Fields(string(g.line))
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return false
	}
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	for _, blocked := range providerUsageBlockedCommands {
		// Include abbreviations: Enter can accept the CLI menu's completion.
		if strings.HasPrefix(blocked, name) {
			return true
		}
	}
	return false
}

// filter is called under the session lock. Navigation and editing keys stay
// native. Keep a bounded draft so typed/pasted account changes are cancelled
// before their Enter reaches the CLI. Never return or log the person's text.
func (g *providerUsageInput) filter(data string) (string, error) {
	data = providerUsageProtocolInput.ReplaceAllString(data, "")
	paste := strings.HasPrefix(data, "\x1b[200~") && strings.HasSuffix(data, "\x1b[201~")
	text := data
	if paste {
		text = strings.TrimSuffix(strings.TrimPrefix(data, "\x1b[200~"), "\x1b[201~")
	}
	// These prefixes are handled by the CLI before any model tool policy:
	// ! enters user shell mode, and @ can attach a local credential file.
	if strings.ContainsRune(text, '@') || strings.HasPrefix(strings.TrimSpace(string(g.line[:g.cursor])+text), "!") {
		return "", errors.New("This usage window does not run shell commands or attach files.")
	}
	printable := 0
	for _, key := range text {
		if unicode.IsPrint(key) {
			printable++
		}
	}
	if printable+len(g.line) > providerUsageInputLimit && !strings.HasPrefix(text, "\x1b") {
		return "", errors.New("Keep each entry to 512 characters or fewer.")
	}
	if paste {
		if strings.ContainsAny(text, "\r\n\x1b") {
			return "", errors.New("Paste one short entry at a time.")
		}
		g.insert([]rune(text))
		return data, nil
	}
	if data != "" && (providerSetupANSI.FindString(data) == data || (len(data) == 3 && strings.HasPrefix(data, "\x1bO"))) {
		switch data {
		case "\x1b[D", "\x1bOD":
			if g.cursor > 0 {
				g.cursor--
			}
		case "\x1b[C", "\x1bOC":
			if g.cursor < len(g.line) {
				g.cursor++
			}
		case "\x1b[H", "\x1bOH", "\x1b[1~":
			g.cursor = 0
		case "\x1b[F", "\x1bOF", "\x1b[4~":
			g.cursor = len(g.line)
		case "\x1b[3~":
			if g.cursor < len(g.line) {
				g.line = append(g.line[:g.cursor], g.line[g.cursor+1:]...)
			}
		}
		return data, nil
	}
	var out strings.Builder
	for _, key := range data {
		switch key {
		case '\r', '\n':
			if g.blocked() {
				g.line, g.cursor = nil, 0
				out.WriteString("\x05\x15") // end, then clear the cancelled draft
				return out.String(), errors.New("Sign-in, account settings and session changes are blocked here. Use Providers to manage the account.")
			}
			g.line, g.cursor = nil, 0
		case '\x03':
			g.line, g.cursor = nil, 0
		case '\x01':
			g.cursor = 0
		case '\x05':
			g.cursor = len(g.line)
		case '\x15':
			g.line = g.line[g.cursor:]
			g.cursor = 0
		case '\x0b':
			g.line = g.line[:g.cursor]
		case '\x17':
			start := g.cursor
			for start > 0 && unicode.IsSpace(g.line[start-1]) {
				start--
			}
			for start > 0 && !unicode.IsSpace(g.line[start-1]) {
				start--
			}
			g.line = append(g.line[:start], g.line[g.cursor:]...)
			g.cursor = start
		case '\x7f', '\x08':
			if g.cursor > 0 {
				g.line = append(g.line[:g.cursor-1], g.line[g.cursor:]...)
				g.cursor--
			}
		default:
			if unicode.IsPrint(key) {
				g.insert([]rune{key})
			}
		}
		out.WriteRune(key)
	}
	return out.String(), nil
}

func (g *providerUsageInput) insert(text []rune) {
	tail := append([]rune(nil), g.line[g.cursor:]...)
	g.line = append(append(g.line[:g.cursor], text...), tail...)
	g.cursor += len(text)
}
