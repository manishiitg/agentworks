package claudecode

import (
	"encoding/json"
	"testing"
)

// A live /compact result (claude 2.1.x): usage is all zeros, the summary
// call's tokens are only in modelUsage.
func TestClaudeCompactCostComesFromModelUsage(t *testing.T) {
	var ev claudeStreamEvent
	raw := `{"type":"result","subtype":"success","usage":{"input_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":0},"modelUsage":{"claude-sonnet-4-6":{"inputTokens":1021,"outputTokens":849,"cacheReadInputTokens":27435,"cacheCreationInputTokens":28868,"contextWindow":200000}},"result":""}`
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	var totals claudeModelUsageTotals
	totals.add(ev.ModelUsage)
	usage, read, write := totals.usage()
	if read != 27435 || write != 28868 || usage.InputTokens != 1021 || usage.OutputTokens != 849 || usage.TotalTokens != 1870 {
		t.Fatalf("usage=%+v read=%d write=%d", usage, read, write)
	}
	res := claudeIdleCompactObserved{ended: true, before: 27445, after: 1192}.result(read, write)
	if !res.Compacted || res.CacheReadTokens != 27435 || res.CacheWriteTokens != 28868 {
		t.Fatalf("result=%+v", res)
	}
}
