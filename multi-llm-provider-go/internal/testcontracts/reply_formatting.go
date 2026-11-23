package testcontracts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
)

// ReplyFormattingCriteria is the rubric for judging whether a turn's FINAL reply
// reached the caller with the markdown structure the model actually authored.
//
// Separate from FinalExtractionCriteria, which asks "is this the clean final
// answer rather than terminal chrome?". That question can be answered yes while
// the answer is still structurally ruined: a pane hard-wraps at its width, so a
// reply can be perfectly clean, contain every required fragment, and still
// render as one broken block once a consumer treats it as markdown.
var ReplyFormattingCriteria = []string{
	"EXTRACTED_FINAL preserves the paragraph breaks the model authored — a blank line between paragraphs is still a blank line, not collapsed into a single wrapped block",
	"list items each begin their own line and are not folded into surrounding prose",
	"no line break appears INSIDE a sentence, word, or inline markdown span (`**bold**`, backticks) where the terminal wrapped it",
	"the reply reads as markdown a UI could render directly, not as fixed-width terminal output",
	"content is unchanged — this is about structure only; wording, numbers and punctuation must match what the model wrote",
}

// ReplyFormattingCase is one live provider turn to judge.
type ReplyFormattingCase struct {
	Provider string
	// TmuxScreen is the raw pane capture, recorded as evidence. It is expected
	// to be wrapped — that is the condition being defended against.
	TmuxScreen string
	// Extracted is what the adapter actually returned to the caller.
	Extracted string
	// NativeFinal is the same turn's provider-authored answer, when available
	// independently of the adapter's return value (for example its saved trail).
	NativeFinal string
	// WantParagraphs and WantBullets are the structures the prompt asked the
	// model to produce, asserted deterministically before any judging.
	WantParagraphs int
	WantBullets    int
	UserGoal       string
	ExpectedNote   string
}

// AssertFinalAnswerPreservesStructure is the live contract for
// CertReplyFormattingFidelity.
//
// Deterministic gate first (blank lines and list markers actually survived),
// then the agentreview sign-off for the judgment a substring check cannot make.
//
// The deterministic half deliberately also asserts that the PANE was wrapped.
// Without that, the test silently passes whenever the model happens to answer
// briefly enough not to wrap — proving nothing while looking green, which is the
// failure mode that let this defect ship in the first place.
func AssertFinalAnswerPreservesStructure(t testing.TB, c ReplyFormattingCase) {
	t.Helper()

	extracted := strings.TrimSpace(c.Extracted)
	if extracted == "" {
		t.Fatalf("%s returned an empty final answer", c.Provider)
	}
	if c.NativeFinal != "" && extracted != strings.TrimSpace(c.NativeFinal) {
		t.Fatalf("%s changed the native final answer's text or formatting", c.Provider)
	}
	if c.NativeFinal == "" && strings.TrimSpace(c.TmuxScreen) == "" {
		t.Fatalf("%s reply-formatting case has no pane capture to compare against", c.Provider)
	}

	// The test is only meaningful if the pane really did wrap. A pane line
	// noticeably longer than the extracted reply's longest line means the
	// extraction came from somewhere better than the screen; equal-and-short
	// means we never exercised the defect.
	if c.NativeFinal == "" && !paneLooksWrapped(c.TmuxScreen, extracted) {
		t.Fatalf("%s: the pane does not appear to have wrapped this reply, so this run proves nothing.\n"+
			"Make the prompt ask for longer lines (or shrink the pane) so wrapping is actually exercised.\npane:\n%s\nextracted:\n%s",
			c.Provider, c.TmuxScreen, extracted)
	}

	if c.WantParagraphs > 1 {
		if got := strings.Count(extracted, "\n\n") + 1; got < c.WantParagraphs {
			t.Fatalf("%s: paragraph breaks lost — wanted %d paragraphs, found %d. The reply will render as one block:\n%s",
				c.Provider, c.WantParagraphs, got, extracted)
		}
	}
	if c.WantBullets > 0 {
		got := countBulletLines(extracted)
		if got < c.WantBullets {
			t.Fatalf("%s: list structure lost — wanted %d lines starting with \"- \", found %d:\n%s",
				c.Provider, c.WantBullets, got, extracted)
		}
	}

	output := map[string]any{
		"provider":            c.Provider,
		"user_goal":           c.UserGoal,
		"expected_note":       c.ExpectedNote,
		"want_paragraphs":     c.WantParagraphs,
		"want_bullets":        c.WantBullets,
		"raw_provider_output": truncateForJudge(c.TmuxScreen),
		"extracted_final":     extracted,
	}
	if c.NativeFinal != "" {
		output["native_final"] = c.NativeFinal
	}
	// Fingerprint over the STRUCTURAL outcome, never the reply text.
	//
	// A live model does not reproduce wording byte-for-byte between runs (and the
	// prompt carries a random token), so a text-based fingerprint changes on every
	// single run and no sign-off can ever persist — the gate would be permanently
	// red and reviewing it would be meaningless ceremony. What this certification
	// actually asserts is structural, so that is what the fingerprint captures:
	// a review stays valid while the behaviour holds, and is invalidated the moment
	// paragraphs, list items, or the wrapped-vs-unwrapped outcome change.
	shape := map[string]any{
		"provider":         c.Provider,
		"paragraphs":       strings.Count(extracted, "\n\n") + 1,
		"bullet_lines":     countBulletLines(extracted),
		"unwrapped":        longestLine(extracted) > longestLine(c.TmuxScreen),
		"pane_was_wrapped": c.NativeFinal == "", // asserted above; recorded so the review can see it was checked
	}
	summary := fmt.Sprintf("Reply-formatting fidelity for %s: does EXTRACTED_FINAL keep the markdown structure the model authored, given RAW_PROVIDER_OUTPUT is hard-wrapped by the terminal?", c.Provider)

	if c.NativeFinal != "" {
		shape["native_format_preserved"] = extracted == strings.TrimSpace(c.NativeFinal)
		summary = fmt.Sprintf("Reply-formatting fidelity for %s: does the final answer preserve native message text, paragraphs and lists?", c.Provider)
	}
	rec := agentreview.WriteWithCriteria(t, t.Name(), summary, ReplyFormattingCriteria, output, shape)
	agentreview.RequireReviewed(t, rec)
}

// paneLooksWrapped reports whether the pane capture shows signs of hard-wrapping
// relative to the extracted reply — i.e. the extraction is NOT simply the pane.
//
// Two independent signals, either of which is sufficient:
//   - the extracted reply has a line longer than the pane's longest line (the
//     pane must have broken it), or
//   - the extracted reply has fewer lines than the pane's rendering of it.
func paneLooksWrapped(pane, extracted string) bool {
	paneMax := longestLine(pane)
	exMax := longestLine(extracted)
	if exMax > paneMax {
		return true
	}
	return strings.Count(extracted, "\n") < strings.Count(strings.TrimSpace(pane), "\n")
}

func longestLine(s string) int {
	max := 0
	for _, line := range strings.Split(s, "\n") {
		if n := len([]rune(strings.TrimRight(line, " \t"))); n > max {
			max = n
		}
	}
	return max
}

// countBulletLines counts lines that begin a markdown list item. Shared by the
// assertion and the review fingerprint so the two can never disagree about what
// "the list survived" means.
func countBulletLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			n++
		}
	}
	return n
}
