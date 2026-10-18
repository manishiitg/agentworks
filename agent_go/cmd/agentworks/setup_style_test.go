package main

import (
	"bytes"
	"strings"
	"testing"
)

// The setup report keeps its exact plain lines when it is not a terminal, and on a terminal turns the same
// lines into a headline, labelled sections and wrapped text.
func TestSetupStyleKeepsPlainLinesAndFormatsOnATerminal(t *testing.T) {
	lines := []string{
		"Claude Code is set up for AgentWorks without hooks: installed the AgentWorks tools plugin (agentworks-tools@agentworks, no hooks) for your user in Claude Code (~/.claude). This applies to every project.",
		"Next: restart Claude Code, then say \"connect this chat to AgentWorks\" in a chat. Nothing is typed and nothing shows as blocked.",
		"To undo: claude plugin uninstall agentworks-tools@agentworks --scope user",
	}
	var plain bytes.Buffer
	p := newSetupStyle(&plain)
	for _, l := range lines {
		p.Say(l)
	}
	if plain.String() != strings.Join(lines, "\n")+"\n" {
		t.Fatalf("a non-terminal must print the lines unchanged: %q", plain.String())
	}
	var out bytes.Buffer
	s := &setupStyle{w: &out, color: true, width: 60}
	for _, l := range lines {
		s.Say(l)
	}
	got := out.String()
	for _, want := range []string{"✓ Claude Code is set up for AgentWorks without hooks", "\x1b[1m\x1b[36mNext\x1b[0m", "To undo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Next: ") || strings.Contains(got, "To undo: ") {
		t.Fatalf("the section labels must replace the prefixes:\n%s", got)
	}
	for _, row := range strings.Split(got, "\n") {
		if visible := len(stripANSI(row)); visible > 60 {
			t.Fatalf("a line is wider than the terminal (%d): %q", visible, row)
		}
	}
	t.Log("\n" + got)
}

func stripANSI(s string) string {
	for {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return s
		}
		s = s[:i] + s[i+j+1:]
	}
}
