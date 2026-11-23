package picli

import (
	"strings"
	"testing"
)

// TestPiApplyAssistantEventDoesNotMergeSeparateSegments is the regression
// guard for a real bug: pi delimits each logical assistant text segment with
// its own text_start/text_end pair — verified live against pi 0.80.10,
// including a captured turn with TWO separate text_start/text_end blocks
// (narration before a tool call, then narration after it, both inside one
// turn_start/turn_end pair). The adapter only ever handled text_delta and
// reset its buffer solely at turn_end, so two genuinely separate messages
// within one turn concatenated with NO separator between them. Live capture
// happened to have the second segment empty in the cases observed, so this
// test also covers the two-non-empty-segments shape directly, which the live
// captures didn't produce but the mechanism (text_start is a real, ignored
// boundary) makes clearly possible.
func TestPiApplyAssistantEventDoesNotMergeSeparateSegments(t *testing.T) {
	var buf strings.Builder
	events := []*piAssistantEvent{
		{Type: "text_start"},
		{Type: "text_delta", Delta: "About to read a.txt."},
		{Type: "text_end"},

		{Type: "text_start"},
		{Type: "text_delta", Delta: "The file said hello."},
		{Type: "text_end"},
	}
	for _, e := range events {
		piApplyAssistantEvent(&buf, e)
	}

	got := buf.String()
	if strings.Contains(got, "a.txt.The file") {
		t.Fatalf("two separate assistant messages were merged with no separator: %q", got)
	}
	want := "About to read a.txt.\n\nThe file said hello."
	if got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
}

// TestPiApplyAssistantEventSingleSegmentGetsNoSpuriousSeparator proves the
// common case (one text segment per turn, the vast majority of real turns)
// is unaffected — no separator inserted before the very first segment.
func TestPiApplyAssistantEventSingleSegmentGetsNoSpuriousSeparator(t *testing.T) {
	var buf strings.Builder
	for _, e := range []*piAssistantEvent{
		{Type: "text_start"},
		{Type: "text_delta", Delta: "Hello"},
		{Type: "text_delta", Delta: " world"},
		{Type: "text_end"},
	} {
		piApplyAssistantEvent(&buf, e)
	}
	if got := buf.String(); got != "Hello world" {
		t.Fatalf("buffer = %q, want %q (no spurious separator)", got, "Hello world")
	}
}

// TestPiApplyAssistantEventRealCapturedSequence replays the exact event
// sequence captured live from turn #2 of a real multi-tool-call pi session
// (text_start/text_delta/text_delta/text_end, then a SECOND empty
// text_start/text_delta/text_end, verified live 2026-07-24) and confirms no
// panic and no unwanted separator when the second segment is empty (an empty
// segment must not add a bare "\n\n" with nothing after it in the common
// case this capture showed).
func TestPiApplyAssistantEventRealCapturedSequence(t *testing.T) {
	var buf strings.Builder
	for _, e := range []*piAssistantEvent{
		{Type: "text_start"},
		{Type: "text_delta", Delta: `The file a.txt contained the text "`},
		{Type: "text_delta", Delta: `first content". I am now about to read b.txt.`},
		{Type: "text_delta", Delta: ""},
		{Type: "text_end"},
		{Type: "text_start"},
		{Type: "text_delta", Delta: ""},
		{Type: "text_end"},
	} {
		piApplyAssistantEvent(&buf, e)
	}
	got := buf.String()
	want := `The file a.txt contained the text "first content". I am now about to read b.txt.` + "\n\n"
	if got != want {
		t.Fatalf("buffer = %q, want %q", got, want)
	}
}
