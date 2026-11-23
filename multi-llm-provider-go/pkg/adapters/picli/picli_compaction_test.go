package picli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Records are Pi 1.0.3's own shapes: the structured lines are the examples in
// Pi's docs/json.md (compaction_start/compaction_end); the marker lines are
// what piMarkerExtensionSource writes from session_before_compact /
// session_compact / session_compact_failed (dist/core/extensions/types.d.ts).
func TestPiCompactionChunks(t *testing.T) {
	t0 := time.UnixMilli(1_790_000_000_000)

	t.Run("structured", func(t *testing.T) {
		var tr piCompactionTracker
		lines := []string{
			`{"type":"compaction_start","reason":"threshold"}`,
			`{"type":"compaction_end","reason":"threshold","result":{"summary":"Summary of conversation...","firstKeptEntryId":"abc123","tokensBefore":150000,"estimatedTokensAfter":32000},"aborted":false,"willRetry":false}`,
			`{"type":"compaction_start","reason":"overflow"}`,
			`{"type":"compaction_end","reason":"overflow","aborted":false,"willRetry":false,"errorMessage":"Context overflow recovery failed: x"}`,
		}
		var got []llmtypes.ContextCompaction
		for i, line := range lines {
			var ev piJSONEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Fatal(err)
			}
			chunk, ok := tr.fromStructuredEvent(ev, t0.Add(time.Duration(i)*1500*time.Millisecond))
			if !ok || chunk.Type != llmtypes.StreamChunkTypeContextCompaction {
				t.Fatalf("line %d: no compaction chunk", i)
			}
			got = append(got, *chunk.ContextCompaction)
		}
		want := []llmtypes.ContextCompaction{
			{Provider: "pi-cli", Phase: "start", ID: "pi-compaction-1790000000000", Trigger: "threshold", StartedAt: t0},
			{Provider: "pi-cli", Phase: "end", ID: "pi-compaction-1790000000000", Trigger: "threshold", Outcome: "success",
				TokensBefore: 150000, TokensAfter: 32000, StartedAt: t0, EndedAt: t0.Add(1500 * time.Millisecond), DurationMs: 1500},
			{Provider: "pi-cli", Phase: "start", ID: "pi-compaction-1790000003000", Trigger: "overflow", StartedAt: t0.Add(3 * time.Second)},
			{Provider: "pi-cli", Phase: "end", ID: "pi-compaction-1790000003000", Trigger: "overflow", Outcome: "failed",
				StartedAt: t0.Add(3 * time.Second), EndedAt: t0.Add(4500 * time.Millisecond), DurationMs: 1500},
		}
		assertCompactions(t, got, want)
	})

}

func assertCompactions(t *testing.T, got, want []llmtypes.ContextCompaction) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if !g.StartedAt.Equal(w.StartedAt) || !g.EndedAt.Equal(w.EndedAt) {
			t.Errorf("chunk %d times: got %v/%v want %v/%v", i, g.StartedAt, g.EndedAt, w.StartedAt, w.EndedAt)
		}
		g.StartedAt, g.EndedAt, w.StartedAt, w.EndedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		if g != w {
			t.Errorf("chunk %d:\n got  %+v\n want %+v", i, g, w)
		}
	}
}
