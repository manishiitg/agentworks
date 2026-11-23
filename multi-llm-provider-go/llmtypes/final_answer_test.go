package llmtypes

import "testing"

func TestReconcileFinalAnswer(t *testing.T) {
	// The case this whole mechanism exists for: the pane holds the reply
	// hard-wrapped by the terminal, the transcript holds it as authored.
	const authored = "Let's do this one step at a time.\n\nFirst we need to borrow, because 1/6 is smaller than 5/6, so **5 1/6 becomes 4 7/6**.\n\n- Check the whole numbers\n- Then the fractions"
	const wrapped = "Let's do this one step at a time.\nFirst we need to borrow, because 1/6 is\nsmaller than 5/6, so **5 1/6 becomes 4\n7/6**.\n- Check the whole numbers\n- Then the fractions"

	tests := []struct {
		name       string
		pane       string
		transcript string
		want       string
		why        string
	}{
		{
			name: "wrapped pane, matching transcript -> transcript wins",
			pane: wrapped, transcript: authored, want: authored,
			why: "same words, so it is the same message with better formatting",
		},
		{
			name: "no transcript yet -> pane",
			pane: wrapped, transcript: "", want: wrapped,
			why: "async lag or a provider with no transcript; pane is all there is",
		},
		{
			name: "no pane text -> transcript",
			pane: "", transcript: authored, want: authored,
			why: "forced completion or a failed scrape; better than nothing",
		},
		{
			name: "transcript is a DIFFERENT message -> pane wins",
			pane: "The answer is 3 4/6.", transcript: "Let's look at question 2 instead.",
			want: "The answer is 3 4/6.",
			why:  "the guard that matters: stale transcript must never replace this turn's reply",
		},
		{
			name: "pane holds only the tail of a long reply -> transcript wins",
			pane: "so **5 1/6 becomes 4 7/6**.", transcript: "First we need to borrow, because 1/6 is smaller than 5/6, so **5 1/6 becomes 4 7/6**.",
			want: "First we need to borrow, because 1/6 is smaller than 5/6, so **5 1/6 becomes 4 7/6**.",
			why:  "a clipped/scrolled pane is still the same message",
		},
		{
			name: "pane has words the transcript lacks -> pane wins",
			pane: "Done. Extra pane noise here.", transcript: "Done.",
			want: "Done. Extra pane noise here.",
			why:  "not a suffix match; treat as a different message rather than silently dropping text",
		},
		{
			name: "both empty -> empty",
			pane: "", transcript: "", want: "",
			why: "nothing to return",
		},
		{
			name: "whitespace-only differences beyond wrapping (tabs, runs of spaces)",
			pane: "Nice   work\ton\nfractions!", transcript: "Nice work on fractions!",
			want: "Nice work on fractions!",
			why:  "all whitespace is normalized away, not just newlines",
		},
		{
			name: "punctuation differs -> pane wins",
			pane: "Is that right?", transcript: "Is that right!",
			want: "Is that right?",
			why:  "sentence punctuation stays strict; a mismatch means a different message",
		},
		{
			// Caught by running the live cursor E2E: cursor's TUI RENDERS markdown
			// rather than echoing it, so the pane shows bullet lines with the "- "
			// marker stripped. Comparing raw words judged an identical message to be
			// a different one, fell back to the pane, and silently defeated the fix.
			name:       "TUI stripped the list markers -> still the same message",
			pane:       "Here are the steps:\nfirst thing\nsecond thing",
			transcript: "Here are the steps:\n\n- first thing\n- second thing",
			want:       "Here are the steps:\n\n- first thing\n- second thing",
			why:        "list markers are rendering, not content; the words are identical",
		},
		{
			// Bold is rendered via ANSI and stripped, so the pane keeps the words
			// without the ** markers. Bullets appear at line start, which is where a
			// TUI puts them (and the only place it is safe to strip them — a "-" or
			// "*" mid-sentence is content, not syntax).
			name:       "TUI rendered bold, and used its own bullet glyph at line start",
			pane:       "Use five sixths here:\n• first\n• second",
			transcript: "Use **five sixths** here:\n\n- first\n- second",
			want:       "Use **five sixths** here:\n\n- first\n- second",
			why:        "emphasis characters and leading bullet glyphs are normalized away before comparing",
		},
		{
			name:       "a hyphen mid-sentence is content, not syntax",
			pane:       "It is a well-known trick",
			transcript: "It is a well known trick",
			want:       "It is a well-known trick",
			why:        "only LEADING list markers are stripped, so real hyphenation still differentiates messages",
		},
		{
			name:       "numbered list rendered without its numbering",
			pane:       "Steps:\nread it\nfix it",
			transcript: "Steps:\n\n1. read it\n2. fix it",
			want:       "Steps:\n\n1. read it\n2. fix it",
			why:        "ordered-list numbering is also syntax a TUI may render away",
		},
		{
			// Observed live in SparkQuill: the pane was missing one contiguous block
			// from the MIDDLE of an otherwise identical reply (the TUI had redrawn
			// over it), so head and tail matched but an exact suffix comparison
			// failed — and the reconciliation silently fell back to the wrapped pane.
			name:       "pane lost a chunk from the MIDDLE -> still the same message",
			pane:       "I built the study guide for you. It is open on the right. Tap Give when you are ready to hand it over.",
			transcript: "I built the study guide for you. It is open on the right.\n\nMode: Beginner — the tutor teaches step by step.\n\nTap Give when you are ready to hand it over.",
			want:       "I built the study guide for you. It is open on the right.\n\nMode: Beginner — the tutor teaches step by step.\n\nTap Give when you are ready to hand it over.",
			why:        "a redrawn pane can drop a middle chunk; order-preserving similarity still recognizes the message",
		},
		{
			name:       "genuinely different words still refused despite markdown normalizing",
			pane:       "Here are the steps:\nfirst thing",
			transcript: "Completely different reply about question 9",
			want:       "Here are the steps:\nfirst thing",
			why:        "the guard must survive the looser comparison — a wrong turn differs in WORDS",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ReconcileFinalAnswer(tc.pane, tc.transcript)
			if got != tc.want {
				t.Errorf("ReconcileFinalAnswer() mismatch (%s)\n pane:       %q\n transcript: %q\n got:        %q\n want:       %q",
					tc.why, tc.pane, tc.transcript, got, tc.want)
			}
		})
	}
}

// TestReconcileFinalAnswerPreservesStructure is the product-level assertion:
// after reconciliation the reply must still contain the blank lines and list
// markers that make it render as markdown. A test that only compared strings
// could pass while the thing the caller actually needs is gone.
func TestReconcileFinalAnswerPreservesStructure(t *testing.T) {
	authored := "Intro line.\n\n- first\n- second"
	wrapped := "Intro line.\n- first\n- second"

	got := ReconcileFinalAnswer(wrapped, authored)
	if !containsBlankLine(got) {
		t.Errorf("paragraph break lost — the reply will render as one block: %q", got)
	}
}

func containsBlankLine(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\n' && s[i+1] == '\n' {
			return true
		}
	}
	return false
}
