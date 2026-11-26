// Package paneview holds helpers for normalising tmux pane snapshots
// before they reach the UI. Adapters that stream live capture-pane output
// (claude-code, codex, gemini, cursor, pi) all share the same
// hygiene needs, so the logic lives once here.
package paneview

import "strings"

// maxConsecutiveBlankLines caps how many blank rows survive a collapse. CLI
// agents redraw their loading spinners ("Generating...") via cursor
// positioning, and `capture-pane -e` materialises every frame's current pane
// state into scrollback — typically the spinner row followed by ~25 empty rows
// of pane area — so the runs must be capped or the snapshot becomes mostly
// whitespace. But the TUIs deliberately separate sections with 2–3 blank rows;
// capping at 1 stacked those sections directly on top of each other and made
// the pane hard to read once it flipped to a re-captured snapshot. Keeping up
// to 2 preserves that visual separation while still squashing the spinner
// gaps.
const maxConsecutiveBlankLines = 2

// StripANSIPreserveColors removes ANSI escape sequences EXCEPT SGR (Select
// Graphic Rendition: color, bold, dim, underline — sequences terminated by
// 'm'), then collapses blank-run noise. The preserved SGR codes let the
// frontend colorize the rendered pane snapshot via ansi_up; cursor-positioning
// and screen-clear sequences are dropped because ansi_up does not emulate
// VT100 movement.
//
// All interactive tmux adapters (claude-code, codex, gemini, cursor)
// fed `capture-pane -e` output through a byte-identical copy of this before it
// reached the UI; the logic now lives here once.
//
// An escape is terminated by the first ASCII letter; only a final 'm' keeps the
// sequence. A trailing incomplete escape (buffer cut mid-sequence) is dropped,
// which is the desired behavior — a half-written color code must not reach the
// renderer.
func StripANSIPreserveColors(s string) string {
	var b, esc strings.Builder
	inEscape := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inEscape {
			esc.WriteByte(ch)
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				if ch == 'm' {
					b.WriteString(esc.String())
				}
				esc.Reset()
				inEscape = false
			}
			continue
		}
		if ch == 0x1b {
			esc.WriteByte(ch)
			inEscape = true
			continue
		}
		b.WriteByte(ch)
	}
	return CollapseBlankRuns(b.String())
}

// CollapseBlankRuns squeezes any run of blank/whitespace-only lines down to at
// most maxConsecutiveBlankLines and trims trailing whitespace from each line.
//
// Use this as the final step after stripping ANSI cursor escapes — color SGR
// is preserved upstream, blank-row noise is squashed here.
func CollapseBlankRuns(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	lines = stripTrailingSpinnerCells(lines)
	lines = pruneInputBoxTrailer(lines)
	lines = pruneSpinnerLines(lines)
	lines = pruneSpinnerWordFragments(lines)
	out := make([]string, 0, len(lines))
	blankRun := 0
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			blankRun++
			if blankRun <= maxConsecutiveBlankLines {
				out = append(out, "")
			}
			continue
		}
		blankRun = 0
		out = append(out, trimmed)
	}

	// Trim leading empty lines
	start := 0
	for start < len(out) && out[start] == "" {
		start++
	}
	out = out[start:]

	return strings.Join(out, "\n")
}

func stripTrailingSpinnerCells(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = stripTrailingSpinnerCell(line)
	}
	return out
}

func stripTrailingSpinnerCell(line string) string {
	brailleIdx := -1
	for idx, r := range line {
		if r >= 0x2800 && r <= 0x28FF {
			brailleIdx = idx
			break
		}
	}
	if brailleIdx <= 0 {
		return line
	}
	prefix := strings.TrimRight(line[:brailleIdx], " \t\r")
	if strings.TrimSpace(prefix) == "" {
		return line
	}
	suffix := strings.ToLower(strings.TrimSpace(line[brailleIdx:]))
	for _, word := range spinnerStatusWords {
		if strings.Contains(suffix, word) {
			return prefix
		}
	}
	return line
}

// pruneSpinnerLines identifies lines containing Braille spinner characters.
// Any historical spinner frame that has been scrolled up (meaning there are
// non-blank lines following it) is pruned. Only the active spinner frame
// at the very end (followed only by blank lines) is preserved.
func pruneSpinnerLines(lines []string) []string {
	lastBrailleIdx := -1
	for i, line := range lines {
		if hasBraille(line) {
			lastBrailleIdx = i
		}
	}
	if lastBrailleIdx == -1 {
		return lines
	}

	isActive := true
	for j := lastBrailleIdx + 1; j < len(lines); j++ {
		if strings.TrimSpace(lines[j]) != "" {
			isActive = false
			break
		}
	}

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if hasBraille(line) {
			if i == lastBrailleIdx && isActive {
				out = append(out, line)
			}
			// Otherwise, prune it (skip)
		} else {
			out = append(out, line)
		}
	}
	return out
}

func hasBraille(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF {
			return true
		}
	}
	return false
}

// spinnerStatusWords are the words CLI agents animate in their in-place spinner
// ("⣾ Generating…", "Working…", "Loading…", …). When tmux flattens that
// animation into scrollback, the leading glyph can land on a different column
// than the text, leaving bare staggered fragments like "oading", "king..",
// "enerat", "Worki", "nerati" — each a substring of one of these words.
var spinnerStatusWords = []string{
	"loading", "working", "generating", "thinking", "analyzing", "exploring",
	"reviewing", "confirming", "refining", "investigating", "searching",
	"reading", "writing", "calling", "running", "navigating", "examining",
	"identifying", "saving", "extracting", "discovering", "processing",
	"waiting", "fetching", "building", "planning", "composing", "retrieving",
	"downloading", "uploading", "connecting", "preparing", "finalizing",
}

// spinnerFragmentKind classifies a line as a frame of an animated spinner status
// word (with any leading Braille glyph stripped onto another column):
//
//	"strong" — a multi-char letter fragment (2..14) that is a substring of a
//	           known status word ("oading", "king", "enerat", "worki"). Two or
//	           more of these in a row is unambiguous spinner noise.
//	"weak"   — a dots-only frame ("..", "...") or a single-letter frame ("g").
//	           Too ambiguous to identify spinner noise on its own (a lone "a" or
//	           "I" is real), so it is dropped ONLY when surrounded by a strong run.
//	""       — not a fragment (real content).
func spinnerFragmentKind(line string) string {
	t := strings.TrimSpace(line)
	if t != "" {
		r := []rune(t)
		if r[0] >= 0x2800 && r[0] <= 0x28FF {
			t = strings.TrimSpace(string(r[1:]))
		}
	}
	if t == "" {
		return "" // blank — handled by the blank-run collapser, not here
	}
	core := strings.Trim(t, ". ")
	if core == "" {
		return "weak" // dots-only frame
	}
	if len(core) > 14 {
		return ""
	}
	lower := strings.ToLower(core)
	for _, r := range lower {
		if r < 'a' || r > 'z' {
			return "" // punctuation/structure → real content, not a fragment
		}
	}
	matched := false
	for _, w := range spinnerStatusWords {
		if strings.Contains(w, lower) {
			matched = true
			break
		}
	}
	if !matched {
		return ""
	}
	if len(core) == 1 {
		return "weak" // single letter — ambiguous alone
	}
	return "strong"
}

// pruneSpinnerWordFragments removes runs of spinner-word fragments left behind
// when an in-place spinner animation is flattened into the captured pane. A
// region is treated as spinner noise only when it contains 2+ STRONG fragments
// (multi-char status-word pieces); within that region, weak fragments (dots,
// single letters) and the strong fragments are dropped. Blank lines between
// fragments do not break a run. An isolated short word that merely happens to be
// a substring of a status word is preserved, so real content is never eaten.
func pruneSpinnerWordFragments(lines []string) []string {
	n := len(lines)
	kind := make([]string, n)
	for i, l := range lines {
		kind[i] = spinnerFragmentKind(l)
	}
	drop := make([]bool, n)
	i := 0
	for i < n {
		if kind[i] == "" {
			i++
			continue
		}
		// Extend a run over fragments (strong/weak) and the blanks between them.
		j := i
		strongCount := 0
		last := i
		for j < n {
			if kind[j] != "" {
				if kind[j] == "strong" {
					strongCount++
				}
				last = j
				j++
			} else if strings.TrimSpace(lines[j]) == "" {
				j++ // blank between fragments — tentatively part of the run
			} else {
				break
			}
		}
		if strongCount >= 2 {
			for k := i; k <= last; k++ {
				if kind[k] != "" {
					drop[k] = true
				}
			}
		}
		i = j
	}
	out := make([]string, 0, n)
	for i, l := range lines {
		if !drop[i] {
			out = append(out, l)
		}
	}
	return out
}

// pruneInputBoxTrailer removes the CLI's input-box region and everything below
// it. The input box appears as a run of ─ box-drawing characters (the top
// border), followed by the › prompt line, followed by another ─ run (bottom
// border). Below the bottom border the pane contains empty space and animation
// cursor-positioning artifacts ("oa", "ad", "di" — fragments of a word being
// overwritten mid-frame) that must not be shown to the user.
//
// We find the last run of ≥20 consecutive ─ characters (the bottom border of
// the input box, which is always the wider of the two) and strip from the top
// border (the ─ run just above the › line) onward. Using the last long ─ run
// as the anchor avoids false-positives on ─ dividers inside tool output.
func pruneInputBoxTrailer(lines []string) []string {
	// Find the index of the last box-border line (≥20 ─ chars).
	lastBorderIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 20 && isAllDashes(trimmed) {
			lastBorderIdx = i
		}
	}
	if lastBorderIdx < 0 {
		return lines
	}
	// Walk back from lastBorderIdx to find the matching top border
	// (the ─ line just before the › prompt). The top border is the
	// nearest ─ line above lastBorderIdx.
	topBorderIdx := -1
	for i := lastBorderIdx - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if len(trimmed) >= 20 && isAllDashes(trimmed) {
			topBorderIdx = i
			break
		}
		// Allow only blank lines and a single › prompt line between the two borders.
		if strings.TrimSpace(trimmed) != "" && !strings.HasPrefix(strings.TrimSpace(trimmed), ">") {
			break
		}
	}
	cutAt := lastBorderIdx + 1 // strip from bottom border onward
	if topBorderIdx >= 0 {
		cutAt = topBorderIdx // strip from top border onward (cleaner)
	}
	return lines[:cutAt]
}

func isAllDashes(s string) bool {
	for _, r := range s {
		if r != '─' && r != '-' && r != '━' {
			return false
		}
	}
	return true
}
