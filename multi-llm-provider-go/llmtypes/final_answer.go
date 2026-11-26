package llmtypes

import (
	"regexp"
	"strings"
)

// ReconcileFinalAnswer chooses the best text for a turn's final reply, given
// what was scraped from the terminal pane and what the CLI recorded in its own
// on-disk session transcript.
//
// # Why this exists
//
// A tmux pane is a RENDERING: the CLI lays its reply out for a terminal of some
// width, hard-wrapping as it goes. Once wrapped, the author's own line structure
// is unrecoverable from the pane, because a soft wrap is byte-identical to a
// newline the model actually typed. Any consumer rendering the reply as markdown
// therefore shows broken paragraphs and lists that the model never produced.
// Heuristic "dewrapping" cannot fix it and makes deliberate structure worse.
//
// Every supported CLI also writes the same reply to its own session file
// UNWRAPPED — three natively (it powers their `--resume`), pi via an injected
// hook. So the fix is not to parse the pane better; it is to prefer the file and
// keep the pane for what it is genuinely good at (knowing the turn ended).
//
// # Why it is not just "return the transcript"
//
// Those files are written asynchronously — cursor commits its sqlite root
// seconds after the pane has settled — so at the moment the pane says "done"
// the transcript may hold the PREVIOUS turn's text, or nothing. Returning a
// different turn's reply is a far worse failure than bad wrapping.
//
// So the pane is used as a CHECKSUM rather than a source: the transcript is only
// trusted when it agrees with the pane once both are reduced to their words. If
// they disagree, the pane wins and the caller is no worse off than before this
// function existed.
//
// This is a pure function so the policy can be tested exhaustively in one place
// and stays identical across every adapter, rather than being reimplemented
// three times.
func ReconcileFinalAnswer(paneText, transcriptText string) string {
	pane := strings.TrimSpace(paneText)
	transcript := strings.TrimSpace(transcriptText)

	// Nothing recorded yet (async lag, or a provider with no transcript) — the
	// pane is all we have.
	if transcript == "" {
		return pane
	}
	// No pane text to verify against (forced completion, a scrape that failed).
	// The transcript is strictly better than returning nothing.
	if pane == "" {
		return transcript
	}
	if sameWords(pane, transcript) {
		return transcript // same message, better formatting
	}
	return pane
}

// markdownMarkers matches the syntax a terminal UI commonly RE-RENDERS rather
// than printing literally: leading list bullets (including a TUI's own "•"),
// ordered-list numbering, and inline emphasis/code characters.
var markdownMarkers = regexp.MustCompile(`(?m)^[ \t]*(?:[-*+•]|\d+[.)])[ \t]+|[*_` + "`" + `]`)

// A TUI draws a markdown TABLE with box-drawing glyphs instead of echoing the
// pipes and dashes the model wrote, which is the same substitution the bullet
// handling above already accounts for -- just for tables rather than lists.
// Left alone it is by far the biggest source of false mismatches, because a
// table contributes one separator per column per row: a real cursor-agent reply
// listing 31 schedules put 42 box-drawing bars in the pane, dragging an
// otherwise-identical message to a 0.826 word ratio against a 0.90 threshold,
// so the clean markdown table was discarded in favour of the rendered one.
// Normalising them back scores that same pair at 0.972.
//
// Comparison only -- neither candidate's text is modified by this, so the
// answer that gets returned is always verbatim.
var tableGlyphs = strings.NewReplacer(
	"│", "|", "┃", "|", "┆", "|", "┊", "|", "╎", "|", "║", "|",
)

// The separator row under a table header renders as a run of horizontal
// box-drawing, where the model wrote "---". Collapse both to nothing rather
// than trying to match them up.
var horizontalRule = regexp.MustCompile(`[─━┄┅┈┉╌╍╴╶┼┿╋├┤┌┐└┘╭╮╯╰]+|-{3,}`)

// normalizeForWordCompare removes the syntax a terminal re-renders rather than
// echoing, so two spellings of the same message compare equal.
func normalizeForWordCompare(s string) string {
	s = markdownMarkers.ReplaceAllString(s, "")
	s = tableGlyphs.Replace(s)
	return horizontalRule.ReplaceAllString(s, " ")
}

// sameWords reports whether two texts carry the same visible words in the same
// order, ignoring whitespace AND markdown syntax.
//
// Whitespace must be ignored because wrapping is exactly what changes it — that
// is the whole point. Markdown markers must be ignored for a second reason found
// by running this against real cursor-agent: its TUI RENDERS markdown rather than
// echoing it, so the pane shows "first bullet" where the transcript holds
// "- first bullet". Comparing raw words therefore judged an identical message to
// be a different one, fell back to the pane, and silently defeated the fix. The
// bug this function guards against is a WRONG TURN's reply, and a different turn
// differs in its words, not in its bullet glyphs.
//
// Sentence punctuation is still compared strictly — only list/emphasis syntax is
// normalized away.
//
// It is a SIMILARITY test, not an equality test, because a real pane is neither
// a clean prefix nor a clean suffix of the transcript. Observed live: a pane was
// missing one contiguous 21-word block from the MIDDLE of an otherwise identical
// reply — the TUI had redrawn over it — so head and tail both matched while an
// exact suffix comparison failed and the fix silently fell back to the pane.
// Panes also clip, scroll, and drop rows under load. What stays true across all
// of that is that the same message keeps almost all of its words in order.
const sameMessageWordRatio = 0.9

func sameWords(pane, transcript string) bool {
	p := strings.Fields(normalizeForWordCompare(pane))
	t := strings.Fields(normalizeForWordCompare(transcript))
	if len(p) == 0 || len(t) == 0 {
		return false
	}
	// Ratio is over the PANE's length: it asks "is essentially everything the
	// screen showed also in the transcript, in order?". Dividing by the
	// transcript instead would wrongly reject a legitimately clipped pane, whose
	// whole problem is that it holds less than the transcript.
	matched := orderedCommonWords(p, t)
	return float64(matched)/float64(len(p)) >= sameMessageWordRatio
}

// orderedCommonWords returns the length of the longest common subsequence of two
// word slices — how many of the pane's words appear in the transcript in the same
// order, allowing gaps on either side.
//
// A subsequence rather than a substring because the gaps are the whole point: a
// missing middle chunk, a clipped head, or a dropped row must not disqualify a
// match. Order still matters, so an unrelated reply that happens to reuse common
// words cannot reach the threshold.
func orderedCommonWords(a, b []string) int {
	// Rolling two-row DP: len(a)*len(b) is ~200x220 for a long reply, trivial,
	// and only two rows are ever live.
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
			} else if prev[j] >= cur[j-1] {
				cur[j] = prev[j]
			} else {
				cur[j] = cur[j-1]
			}
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
