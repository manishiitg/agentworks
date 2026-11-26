package codexcli

import (
	"strings"
	"testing"
)

// TestCodexHardWrappedPromptEchoDoesNotLeak is a regression test for a real
// production bug (found via agent_go's reply_sanitize.go stopgap, "codex
// reply-leak", 2026-07-24): a long prompt line (this app's own long
// <Subject>/<Topic>/<slug>/ activity paths trigger it) that hard-wraps in the
// tmux pane leaked its wrapped tail into the extracted final answer.
//
// Root cause: isCodexWrapEligibleRawLine required the wrapped continuation
// line to be INDENTED before treating it as absorbable into the "›" prompt-
// echo chrome segment. That indentation signal is real for codex's OWN
// multi-line prompt box (it pads continuation rows of a prompt the user typed
// as several lines) but is NOT present when the terminal itself hard-wraps ONE
// long line at the pane's column width — that continuation starts flush at
// column 0. So a hard-wrapped prompt tail fell through, classified as ordinary
// assistant text, and leaked.
//
// No existing fixture caught this because every prior fixture/live prompt used
// short, single-line paths/commands that never wrap — this is a class of input
// (long enough to hard-wrap) that short fixtures structurally cannot exercise.
func TestCodexHardWrappedPromptEchoDoesNotLeak(t *testing.T) {
	baseline := "Codex ready\n›"
	longPath := "/Subjects/Mathematics/Fractions/introducing-equivalent-fractions/answer_key.txt"
	prompt := "Read the file " + longPath + "\nThen reply with a one-sentence summary."

	// The pane hard-wraps the long path mid-word with NO indentation on the
	// continuation — this is what a real terminal does; codex's own prompt box
	// (which DOES indent) is a different rendering path, covered separately
	// below.
	captured := baseline + "\n" +
		"› Read the file /Subjects/Mathematics/Fractions/introducing-equivalent-fracti\n" +
		"ons/answer_key.txt\n" +
		"Then reply with a one-sentence summary.\n" +
		"• The answer is FORTY_TWO.\n" +
		"❯"

	got := parseCodexInteractiveResponse(captured, baseline, prompt, nil)
	if strings.Contains(got, "answer_key.txt") || strings.Contains(got, "Read the file") {
		t.Fatalf("wrapped prompt-echo leaked into the extracted answer: %q", got)
	}
	if !strings.Contains(got, "FORTY_TWO") {
		t.Fatalf("real answer was lost while fixing the leak: %q", got)
	}
}

// TestCodexIndentedMultiLinePromptStillAbsorbs guards the ORIGINAL wrap-
// continuation fix (see isCodexPromptWrapContinuationLine's doc comment):
// codex's own multi-line prompt box renders a "›" marker line followed by
// INDENTED, marker-less continuation lines for a prompt the user typed as
// several lines. This must keep working after loosening the indentation
// requirement in isCodexWrapEligibleRawLine for the hard-wrap case above.
func TestCodexIndentedMultiLinePromptStillAbsorbs(t *testing.T) {
	baseline := "Codex ready\n›"
	prompt := "line one\nline two\nline three"
	captured := baseline + "\n" +
		"› line one\n" +
		"  line two\n" +
		"  line three\n" +
		"• The answer is SIXTY_NINE.\n" +
		"❯"

	got := parseCodexInteractiveResponse(captured, baseline, prompt, nil)
	if strings.Contains(got, "line one") || strings.Contains(got, "line two") || strings.Contains(got, "line three") {
		t.Fatalf("indented multi-line prompt echo leaked: %q", got)
	}
	if !strings.Contains(got, "SIXTY_NINE") {
		t.Fatalf("real answer was lost: %q", got)
	}
}

// TestCodexWrappedRealAnswerIsNotEaten is the inverse safety check: a real
// multi-line ASSISTANT ANSWER (not a prompt echo) that itself hard-wraps must
// NOT be swallowed now that isCodexWrapEligibleRawLine no longer requires
// indentation. It is protected by a different mechanism
// (isCodexAssistantAnswerContinuationLine, unrelated to the prompt-echo
// absorption path this file's other two tests exercise) — this test proves
// the two mechanisms don't interfere.
func TestCodexWrappedRealAnswerIsNotEaten(t *testing.T) {
	baseline := "Codex ready\n›"
	captured := baseline + "\n" +
		"› short prompt\n" +
		"• This is a long answer line that happens to wrap across the terminal pane wid\n" +
		"th because it is quite long.\n" +
		"❯"

	got := parseCodexInteractiveResponse(captured, baseline, "short prompt", nil)
	if !strings.Contains(got, "wrap across the terminal") {
		t.Fatalf("wrapped real-answer content was lost: %q", got)
	}
}
