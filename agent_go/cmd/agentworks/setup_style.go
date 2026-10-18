package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// setupStyle prints what `agentworks setup` reports. On a terminal it adds colour, a check mark for the
// result, labelled sections (Next, To undo) and wrapped, indented text; anywhere else (a pipe, a log, NO_COLOR)
// it prints the plain lines exactly as given.
type setupStyle struct {
	w     io.Writer
	color bool
	width int
}

func newSetupStyle(w io.Writer) *setupStyle {
	s := &setupStyle{w: w, width: 88}
	f, ok := w.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return s
	}
	s.color = true
	if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
		s.width = min(max(cols-2, 50), 100)
	}
	return s
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
)

func (s *setupStyle) paint(code, text string) string { return code + text + ansiReset }

// Say prints one line of the setup report.
func (s *setupStyle) Say(text string) {
	if !s.color {
		fmt.Fprintln(s.w, text)
		return
	}
	switch {
	case strings.Contains(text, " is set up for AgentWorks") || strings.Contains(text, " was already set up for AgentWorks"):
		head, rest, _ := strings.Cut(text, ": ")
		fmt.Fprintln(s.w, s.paint(ansiBold+ansiGreen, "✓ "+head))
		if rest != "" {
			s.body(rest, ansiDim)
		}
	case strings.HasPrefix(text, "Next: "):
		fmt.Fprintln(s.w)
		fmt.Fprintln(s.w, s.paint(ansiBold+ansiCyan, "Next"))
		s.body(strings.TrimPrefix(text, "Next: "), "")
	case strings.HasPrefix(text, "To undo: "):
		fmt.Fprintln(s.w)
		fmt.Fprintln(s.w, s.paint(ansiBold, "To undo"))
		fmt.Fprintln(s.w, "  "+s.paint(ansiDim, strings.TrimPrefix(text, "To undo: "))) // a command: never wrapped, so it can be copied
	case strings.HasPrefix(text, "Note: "):
		fmt.Fprintln(s.w)
		fmt.Fprintln(s.w, s.paint(ansiBold+ansiYellow, "Note"))
		s.body(strings.TrimPrefix(text, "Note: "), "")
	default:
		s.body(text, "")
	}
}

// body prints text wrapped to the terminal width and indented by two spaces.
func (s *setupStyle) body(text, code string) {
	line := ""
	flush := func() {
		if line == "" {
			return
		}
		if code != "" {
			line = s.paint(code, line)
		}
		fmt.Fprintln(s.w, "  "+line)
		line = ""
	}
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > s.width-2 {
			flush()
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	flush()
}
