package llmtypes

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const terminalErrorTailLines = 16
const terminalErrorMaxBytes = 2400

// CompactTerminalPaneForError keeps provider errors small. The full pane is
// available through terminal APIs/debug controls; errors should carry only the
// identity needed to find that pane plus a short tail for orientation.
func CompactTerminalPaneForError(tmuxSession, pane string) string {
	tmuxSession = strings.TrimSpace(tmuxSession)
	pane = strings.TrimSpace(pane)

	var b strings.Builder
	if tmuxSession != "" {
		fmt.Fprintf(&b, "tmux_session=%s", tmuxSession)
	}
	if pane == "" {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString("latest pane is empty")
		return b.String()
	}

	lines := strings.Split(pane, "\n")
	if len(lines) > terminalErrorTailLines {
		lines = lines[len(lines)-terminalErrorTailLines:]
	}
	tail := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(tail) > terminalErrorMaxBytes {
		start := len(tail) - terminalErrorMaxBytes
		for start < len(tail) && !utf8.RuneStart(tail[start]) {
			start++
		}
		tail = "[pane tail truncated]\n" + strings.TrimSpace(tail[start:])
	}

	if b.Len() > 0 {
		b.WriteString("; ")
	}
	b.WriteString("latest pane tail:\n")
	b.WriteString(tail)
	return b.String()
}
