package musecli

import (
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Records copied from a real Muse session.jsonl (sessions/2026/10/04/01a10690-…,
// sequences 13628/13815/13816) with the summary, replay messages and cursors
// stripped. Pins the start/end mapping and that a re-read record never doubles.
func TestMuseTranscriptContextCompaction(t *testing.T) {
	lines := []string{
		`{"sequence":13628,"recorded_at":1791129038009394,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"context_compaction_candidate","candidate_id":"candidate-14","trigger":"soft_threshold_async","timing":"pre_turn","status":"running","budget_before":{"estimated_prompt_tokens":386186,"target_met":null,"estimate_source":"heuristic_estimate"},"estimated_budget_after":{"estimated_prompt_tokens":386186,"target_met":null,"estimate_source":"heuristic_estimate"}}}}`,
		`{"sequence":13815,"recorded_at":1791129064000967,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"context_compaction_candidate","candidate_id":"candidate-14","trigger":"soft_threshold_async","timing":"pre_turn","status":"succeeded","budget_before":{"estimated_prompt_tokens":386186,"target_met":null,"estimate_source":"heuristic_estimate"},"estimated_budget_after":{"estimated_prompt_tokens":45511,"target_met":true,"estimate_source":"heuristic_estimate"}}}}`,
		`{"sequence":13816,"recorded_at":1791129064009443,"payload_type":"runtime.session","payload":{"kind":"run","event":{"kind":"context_compaction_installed","install_id":"install-99","candidate_id":"candidate-14","trigger":"soft_threshold_async","timing":"pre_turn","strategy_id":"summary-preserved-suffix/v1","budget_before":{"estimated_prompt_tokens":386186,"target_met":null,"estimate_source":"heuristic_estimate"},"budget_after":{"estimated_prompt_tokens":45511,"target_met":true,"estimate_source":"heuristic_estimate"},"summarizer_usage":{"input_tokens":205028,"output_tokens":3550,"duration_ms":25702}}}}`,
	}
	seen, ended, started := map[string]bool{}, map[string]bool{}, map[string]time.Time{}
	var got []llmtypes.ContextCompaction
	for _, l := range append(lines, lines...) { // second pass = duplicate records
		for _, c := range museTranscriptLineToChunks(l, seen, ended, started) {
			if c.Type == llmtypes.StreamChunkTypeContextCompaction {
				got = append(got, *c.ContextCompaction)
			}
		}
	}
	want := []llmtypes.ContextCompaction{
		{Provider: "muse-cli", Phase: llmtypes.ContextCompactionPhaseStart, ID: "candidate-14", Trigger: "soft_threshold_async",
			TokensBefore: 386186, StartedAt: time.UnixMicro(1791129038009394).UTC()},
		{Provider: "muse-cli", Phase: llmtypes.ContextCompactionPhaseEnd, ID: "candidate-14", Trigger: "soft_threshold_async",
			Outcome: llmtypes.ContextCompactionOutcomeSuccess, TokensBefore: 386186, TokensAfter: 45511,
			EndedAt: time.UnixMicro(1791129064009443).UTC(), DurationMs: 25702},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d compaction chunks, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
