package claudecode

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The transcript row is a real compact_boundary record (Claude Code 2.1.226,
// trimmed). The stream-json lines follow the 2.1.289 bundled SDK schema:
// status "compacting" repeats every 30s, then a boundary and a status reset.
func TestClaudeCompactionRecords(t *testing.T) {
	events, _, err := readClaudeTranscriptEventsFromFile("testdata/transcript_compact_boundary.jsonl", 0, time.Time{}, nil)
	if err != nil || len(events) != 1 || events[0].Compaction == nil {
		t.Fatalf("transcript events = %+v, err %v", events, err)
	}
	c := events[0].Compaction
	if c.Phase != llmtypes.ContextCompactionPhaseEnd || c.Trigger != "auto" || c.TokensBefore != 935757 || c.TokensAfter != 21900 || c.DurationMs != 195257 {
		t.Fatalf("transcript compaction = %+v", c)
	}

	var tracker claudeStructuredCompaction
	start := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	var out []llmtypes.ContextCompaction
	for i, line := range []string{
		`{"type":"system","subtype":"status","status":"compacting","session_id":"s"}`,
		`{"type":"system","subtype":"status","status":"compacting","session_id":"s"}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"trigger":"auto","pre_tokens":384000,"post_tokens":92000},"session_id":"s"}`,
		`{"type":"system","subtype":"status","status":null,"compact_result":"success","session_id":"s"}`,
	} {
		var ev claudeStreamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		out = append(out, tracker.observe(ev, start.Add(time.Duration(i)*30*time.Second))...)
	}
	if len(out) != 2 || out[0].Phase != llmtypes.ContextCompactionPhaseStart || out[1].Phase != llmtypes.ContextCompactionPhaseEnd || out[0].ID != out[1].ID {
		t.Fatalf("structured compaction = %+v", out)
	}
	if out[1].TokensBefore != 384000 || out[1].TokensAfter != 92000 || out[1].DurationMs != 60000 {
		t.Fatalf("structured end = %+v", out[1])
	}
}

// Reported windows may differ by subscription/model; helper totals are not the
// main model's capacity, and capacity alone must not fabricate context usage.
func TestClaudeReportedContextWindowUsesNativeModelNotHelpers(t *testing.T) {
	var ev claudeStreamEvent
	if err := json.Unmarshal([]byte(`{"type":"result","modelUsage":{"main-model":{"contextWindow":327680},"helper-model":{"contextWindow":1000000}}}`), &ev); err != nil {
		t.Fatal(err)
	}
	usage := &claudeLiveUsage{model: "main-model"}
	if !usage.observeModelUsage(ev.ModelUsage) {
		t.Fatal("native capacity missing")
	}
	status := usage.statusLine()
	if status.Metadata[llmtypes.ContextWindowTokensMetaKey] != 327680 {
		t.Fatalf("wrong model window: %+v", status.Metadata)
	}
	if _, known := status.Metadata[llmtypes.ContextUsedTokensMetaKey]; known {
		t.Fatal("capacity fabricated current usage")
	}
	for _, raw := range []string{`{"main-model":{"contextWindow":0}}`, `{"main-model":{"contextWindow":-1}}`, `{"main-model":{"contextWindow":1.5}}`, `{"helper-model":{"contextWindow":1000000}}`} {
		var models map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &models); err != nil {
			t.Fatal(err)
		}
		if usage.observeModelUsage(models) || usage.window != 327680 {
			t.Fatalf("invalid/mismatched report replaced known capacity: %s", raw)
		}
	}
}
