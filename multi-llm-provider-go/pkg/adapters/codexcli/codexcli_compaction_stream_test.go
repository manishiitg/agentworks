package codexcli

import (
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Records copied from a real Codex 0.160 rollout (2026-10-03), trimmed to the
// rows this reads. Codex writes no compaction start; the end is an
// item_completed ContextCompaction with started/completed ms, and the context
// size after compaction is a token_count with zero input and total_tokens set.
func TestCodexRolloutCompactionAndLiveUsage(t *testing.T) {
	state := newCodexTranscriptStreamState(time.Time{}, "", nil)
	state.path = "testdata/rollout_compaction_0160.jsonl"
	state.sideOnly = true
	state.liveUsage = &llmtypes.LiveUsageThrottle{}

	chunks := state.readChunksAt(time.Now(), true)
	var compactions []*llmtypes.ContextCompaction
	var status *llmtypes.StatusLine
	for _, chunk := range chunks {
		switch chunk.Type {
		case llmtypes.StreamChunkTypeContextCompaction:
			compactions = append(compactions, chunk.ContextCompaction)
		case llmtypes.StreamChunkTypeStatusLine:
			status = chunk.StatusLine
		default:
			t.Fatalf("side-only read emitted %s", chunk.Type)
		}
	}
	if len(compactions) != 1 {
		t.Fatalf("compactions = %d, want 1", len(compactions))
	}
	c := compactions[0]
	if c.Phase != llmtypes.ContextCompactionPhaseEnd || c.ID != "01a100b0-e2e6-7272-8e17-fde7d69456e5" {
		t.Fatalf("compaction = %+v", c)
	}
	if c.TokensBefore != 235118 || c.TokensAfter != 20754 {
		t.Fatalf("tokens before/after = %d/%d, want 235118/20754", c.TokensBefore, c.TokensAfter)
	}
	if c.DurationMs != 175165 {
		t.Fatalf("duration = %d ms, want 175165", c.DurationMs)
	}

	// Only the newest snapshot is released, and it is the post-compaction call.
	if status == nil {
		t.Fatal("no live usage status line")
	}
	if got := status.Metadata[llmtypes.ContextUsedTokensMetaKey]; got != 36984 {
		t.Fatalf("context used = %v, want 36984", got)
	}
	if got := status.Metadata[llmtypes.ContextWindowTokensMetaKey]; got != 258400 {
		t.Fatalf("context window = %v, want 258400", got)
	}
	if status.Model != "gpt-6.1-sol" || len(status.RateLimitWindows()) == 0 {
		t.Fatalf("status = %+v", status)
	}

	// Re-reading the same file emits nothing new.
	if again := state.readChunksAt(time.Now(), true); len(again) != 0 {
		t.Fatalf("second read emitted %d chunks", len(again))
	}
}
