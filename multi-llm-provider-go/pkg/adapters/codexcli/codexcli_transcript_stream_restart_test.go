package codexcli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCodexTranscriptStreamDoesNotReplayHistoryOnFreshProcess is the codex
// counterpart of cursorcli's restart regression test.
//
// Background: the cursor adapter had a real bug where the first turn of a FRESH
// process replayed the whole chat history as streamed content, because its only
// defence was an in-process map of already-returned message IDs against a
// CUMULATIVE transcript root. A restart emptied the map while the transcript on
// disk still held every prior turn.
//
// Codex is structurally immune for a different reason, and this test pins that
// reason down rather than trusting it: its rollout is an append-only JSONL whose
// rows carry wall-clock timestamps, and readCodexTranscriptEventsFromFile skips
// any row stamped before turnStart. That check reads from the data, not from
// process memory, so a fresh state object starting at offset 0 still cannot emit
// prior-turn rows.
//
// This is therefore a "cannot reproduce" test kept deliberately: if anyone ever
// swaps the timestamp filter for in-process dedup, it fails immediately.
func TestCodexTranscriptStreamDoesNotReplayHistoryOnFreshProcess(t *testing.T) {
	const (
		historical = "PRIOR TURN narration that must never be replayed."
		current    = "Current turn narration."
	)

	dir := t.TempDir()
	path := filepath.Join(dir, "rollout.jsonl")

	turnStart := time.Now()
	before := turnStart.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	after := turnStart.Add(1 * time.Second).Format(time.RFC3339Nano)

	// History first, then this turn's output — the exact on-disk shape a restart
	// mid-conversation leaves behind.
	appendLine(t, path, codexAgentMsg(before, historical)+codexAgentMsg(after, current))

	// A brand-new state object, as a restarted process would build: offset 0,
	// empty dedup maps. Path is pre-set so the test doesn't depend on
	// findCodexRolloutForTurn's ~/.codex layout resolution.
	state := newCodexTranscriptStreamState(turnStart, dir, nil)
	state.path = path

	ch := make(chan llmtypes.StreamChunk, 64)
	state.poll(context.Background(), ch)
	close(ch)

	var streamed strings.Builder
	for chunk := range ch {
		if chunk.Type == llmtypes.StreamChunkTypeContent {
			streamed.WriteString(chunk.Content)
		}
	}
	got := streamed.String()

	if strings.Contains(got, historical) {
		t.Errorf("history replayed on a fresh process: %q\nstreamed: %q", historical, got)
	}
	if !strings.Contains(got, current) {
		t.Errorf("current-turn text was not streamed; the filter is too aggressive.\nstreamed: %q", got)
	}
}
