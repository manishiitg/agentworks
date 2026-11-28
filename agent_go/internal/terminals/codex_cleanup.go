package terminals

import (
	"regexp"
	"strings"
)

// codexBoxOnlyPattern matches lines made up of only box-drawing characters and
// whitespace — the codex input-box borders / rules that clutter a completed pane.
var codexBoxOnlyPattern = regexp.MustCompile(`^[\s\x{2500}-\x{257F}\x{2580}-\x{259F}]+$`)

// codexChromeKeywords are substrings that unambiguously mark codex TUI chrome
// (the input box + footer hint bar) rather than answer content. Conservative on
// purpose: matching real answer text here would drop content, so only phrases
// that codex itself renders in its chrome belong in this list.
var codexChromeKeywords = []string{
	"esc to interrupt",
	"ctrl+o",
	"shift+tab",
	"type your message",
	"use /skills",
	"openai codex",
	"chatgpt.com/codex",
}

// SnapshotIsCodex reports whether a terminal snapshot is a codex CLI pane.
func SnapshotIsCodex(s Snapshot) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(s.Status.ProviderLabel)), "codex") {
		return true
	}
	return strings.Contains(strings.ToLower(s.Content), "openai codex")
}

// CleanCompletedCodexContent tidies a COMPLETED codex pane capture for the static
// (xterm) fallback render. Codex is a full-screen TUI: once the pane stops
// streaming, its scrollback (captured with `capture-pane -e`) still holds the
// input box, the footer hint bar, and accumulated repaint frames, which render as
// clutter in a static view. This removes that chrome and de-dups consecutive
// repaint frames WHILE PRESERVING the ANSI SGR codes on real content lines, so
// xterm.js still renders the answer in color.
//
// It is deliberately conservative and self-guarding: it only drops lines that are
// unambiguously chrome (keyword bar / pure box borders) or exact repaint dupes,
// and if cleanup would remove almost everything it returns the original content
// untouched — so a completed codex pane is never rendered with LESS than today.
func CleanCompletedCodexContent(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	prevStripped := ""
	origNonBlank, keptNonBlank := 0, 0
	for _, line := range lines {
		stripped := line
		if strings.IndexByte(stripped, '\x1b') >= 0 {
			stripped = ansiEscapePattern.ReplaceAllString(stripped, "")
		}
		stripped = strings.TrimSpace(stripped)
		if stripped == "" {
			out = append(out, line)
			prevStripped = ""
			continue
		}
		origNonBlank++
		if codexLineIsChrome(stripped) {
			continue
		}
		// Drop consecutive identical repaint frames (same text re-rendered).
		if stripped == prevStripped {
			continue
		}
		prevStripped = stripped
		out = append(out, line) // keep the ORIGINAL line — ANSI colors intact
		keptNonBlank++
	}
	// Safety guard: if cleanup gutted the content, keep the original untouched.
	if keptNonBlank < 2 || (origNonBlank > 0 && keptNonBlank*100/origNonBlank < 30) {
		return content
	}
	return strings.Join(out, "\n")
}

func codexLineIsChrome(stripped string) bool {
	lower := strings.ToLower(stripped)
	for _, kw := range codexChromeKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return codexBoxOnlyPattern.MatchString(stripped)
}
