package codexcli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A turn with several model requests writes one token_count per request. What the turn spent is the growth of the
// cumulative total, not the last request; a second read of the same rollout must not repeat what was reported.
func TestCodexTurnUsageIsTheGrowthOfTheCumulativeTotal(t *testing.T) {
	base := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	ev := func(at time.Duration, lastIn, lastOut, totalIn, totalOut int) string {
		return fmt.Sprintf(`{"type":"event_msg","timestamp":%q,"payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":%d,"total_tokens":%d},"total_token_usage":{"input_tokens":%d,"cached_input_tokens":0,"output_tokens":%d,"total_tokens":%d}}}}`,
			base.Add(at).Format(time.RFC3339Nano), lastIn, lastOut, lastIn+lastOut, totalIn, totalOut, totalIn+totalOut)
	}
	lines := []string{
		ev(0, 1000, 50, 1000, 50), // an earlier turn
		ev(10*time.Minute, 2000, 100, 3000, 150),
		ev(10*time.Minute+5*time.Second, 2100, 40, 5100, 190),
		ev(10*time.Minute+9*time.Second, 2200, 60, 7300, 250), // the turn made three requests
	}
	path := filepath.Join(t.TempDir(), "rollout-2026-10-14T09-00-00-11111111-2222-4333-8444-555555555555.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turnStart := base.Add(10 * time.Minute)
	gi, _, _, _ := readCodexTranscriptUsageFile(path, turnStart)
	if gi == nil || gi.PromptTokens == nil || gi.CompletionTokens == nil || *gi.PromptTokens != 6300 || *gi.CompletionTokens != 200 {
		t.Fatalf("the turn spent 6300 in / 200 out across three requests, got %+v", gi)
	}
	// Reading the same turn again reports nothing: everything up to the cumulative total was already reported, and the
	// last request must not be reported a second time.
	if again, _, _, _ := readCodexTranscriptUsageFile(path, turnStart); again != nil {
		t.Fatalf("a second read repeated reported usage: %+v", again)
	}
}
