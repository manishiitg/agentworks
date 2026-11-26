package claudecode

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestClaudeFinalAnswerRepairsWrappingNotJustTruncation guards a gap that a
// length-based check let through for a long time.
//
// The adapter prefers its JSONL transcript over the pane scrape for two reasons:
// truncation (the pane keeps only the LAST assistant text block) and wrapping
// (the pane hard-wraps, destroying markdown structure). The original gate was
// `len(transcript) > len(pane)`, which catches truncation but is blind to
// wrapping — re-wrapping substitutes a newline for a space, so both forms are
// the SAME length and "longer is better" never fires.
//
// llmtypes.ReconcileFinalAnswer compares by words instead, so both are repaired.
// This test pins the wrapping case specifically, because it is the one the old
// gate missed and the one a future refactor is most likely to reintroduce.
func TestClaudeFinalAnswerRepairsWrappingNotJustTruncation(t *testing.T) {
	t.Run("equal-length rewrap is still repaired", func(t *testing.T) {
		authored := "Here is the plan.\n\n- read the file\n- fix the bug"
		// Same words, same LENGTH — only a space traded for a newline.
		wrapped := "Here is the plan.\n\n- read the file\n- fix the bug"
		wrapped = strings.Replace(wrapped, "\n\n", "\n", 1)
		wrapped += " " // keep lengths equal so a length test cannot distinguish them

		if len(wrapped) != len(authored) {
			t.Fatalf("test setup: lengths differ (%d vs %d), so this would not prove the point",
				len(wrapped), len(authored))
		}
		got := llmtypes.ReconcileFinalAnswer(wrapped, authored)
		if !strings.Contains(got, "\n\n") {
			t.Errorf("paragraph break not restored from the transcript; got %q", got)
		}
	})

	t.Run("truncation still repaired", func(t *testing.T) {
		// The historical case: pane holds only the final block.
		pane := "All set."
		transcript := "I read the file first.\n\nAll set."
		if got := llmtypes.ReconcileFinalAnswer(pane, transcript); got != transcript {
			t.Errorf("earlier prose not recovered; got %q, want %q", got, transcript)
		}
	})

	t.Run("a different message is refused", func(t *testing.T) {
		pane := "Done with question 3."
		stale := "Let's begin question 1."
		if got := llmtypes.ReconcileFinalAnswer(pane, stale); got != pane {
			t.Errorf("a stale transcript replaced this turn's reply; got %q, want %q", got, pane)
		}
	})
}
