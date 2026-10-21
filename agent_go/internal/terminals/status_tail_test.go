package terminals

import (
	"fmt"
	"strings"
	"testing"
)

// The busy checks read the end of a long terminal buffer instead of cleaning all of it. They must give the answers the
// full scan gave; these are the earlier full-scan versions, kept here as the reference.
func referenceLooksBusy(content string) bool {
	lines := cleanedLines(content)
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	for _, line := range lines {
		if isTerminalBusyLine(line) {
			return true
		}
	}
	return false
}

func referenceSettledAfterBusy(content string, metadata map[string]interface{}) bool {
	lines := cleanedLines(content)
	provider := providerLabel(content, metadata)
	if len(lines) == 0 || provider != "Codex CLI" && provider != "Claude Code" {
		return false
	}
	lastBusy, lastPrompt := -1, -1
	for i, line := range lines {
		if isTerminalBusyLine(line) {
			lastBusy = i
		}
		if isProviderIdlePromptLine(provider, line, true) {
			lastPrompt = i
		}
	}
	return lastPrompt > lastBusy
}

func TestBusyChecksReadTheEndOfALongBufferWithTheSameAnswers(t *testing.T) {
	noise := func(n int) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "\x1b[32mplain output line %d with some words to make it long enough\x1b[0m\n", i)
		}
		return b.String()
	}
	const busy, prompt = "Working (4s · esc to interrupt)\n", "❯ \n"
	cases := map[string]string{
		"busy at the end":           noise(5000) + busy,
		"busy only far above":       busy + noise(5000),
		"busy then prompt at end":   noise(5000) + busy + prompt,
		"prompt then busy at end":   noise(5000) + prompt + busy,
		"busy and prompt far above": busy + prompt + noise(5000),
		"prompt far above, no busy": prompt + noise(5000),
		"short buffer":              busy + prompt,
		"blank lines at the end":    noise(5000) + busy + strings.Repeat("\n", 200000),
		"nothing":                   "",
		"only noise":                noise(5000),
	}
	meta := map[string]interface{}{"provider": "Claude Code"}
	for name, content := range cases {
		if got, want := terminalContentLooksBusy(content), referenceLooksBusy(content); got != want {
			t.Errorf("%s: terminalContentLooksBusy = %v, the full scan said %v", name, got, want)
		}
		if got, want := terminalHasSettledPromptAfterBusy(content, meta), referenceSettledAfterBusy(content, meta); got != want {
			t.Errorf("%s: terminalHasSettledPromptAfterBusy = %v, the full scan said %v", name, got, want)
		}
	}
}
