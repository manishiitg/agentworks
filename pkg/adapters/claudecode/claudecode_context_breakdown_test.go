package claudecode

import (
	"strings"
	"testing"
)

// Claude Code reports one total; the parts must add up to it, start again at a
// compaction, and size a block the host repeats in messages on its own.
func TestContextBreakdownAddsUpAndRestartsAtCompaction(t *testing.T) {
	rules := "[Rules]" + strings.Repeat("r", 3996) + "[Message]"
	transcript := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"old conversation that was compacted away"}}`,
		`{"type":"assistant","message":{"content":[],"usage":{"input_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":900}}}`,
		`{"type":"system","subtype":"compact_boundary"}`,
		`{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"` + strings.Repeat("s", 4000) + `"}}`,
		`{"type":"attachment","attachment":{"type":"skill_listing"},"rendered":[{"content":"` + strings.Repeat("k", 4000) + `"}]}`,
		`{"type":"user","message":{"role":"user","content":"` + rules + `hello"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":2,"cache_read_input_tokens":1000,"cache_creation_input_tokens":3000}}}`,
		`{"type":"user","message":{"role":"user","content":"` + rules + `again"}}`,
		`{"type":"assistant","isSidechain":true,"message":{"content":[],"usage":{"input_tokens":99999}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"cache_read_input_tokens":5000,"cache_creation_input_tokens":998}}}`,
	}, "\n")
	got, ok := contextBreakdown(strings.NewReader(transcript), ContextMarkers{Start: "[Rules]", End: "[Message]"})
	if !ok {
		t.Fatal("no breakdown")
	}
	if got.Total != 6000 || got.Setup != 1000 {
		t.Fatalf("total/setup = %d/%d, want 6000/1000 (setup is the first reply's cache read after the compaction)", got.Total, got.Setup)
	}
	if sum := got.Setup + got.Skills + got.InstructionFiles + got.Marked + got.Summary + got.AppNotes + got.Conversation; sum != got.Total {
		t.Fatalf("parts add up to %d, want %d: %#v", sum, got.Total, got)
	}
	if got.MarkedCount != 2 || got.Marked <= got.Summary || got.Summary == 0 || got.Skills == 0 {
		t.Fatalf("parts = %#v, want the repeated block counted twice and larger than the summary", got)
	}
}
