package picli

import (
	"encoding/json"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestApplyPiJSONUsageAcceptsCapturedMessageEndShape(t *testing.T) {
	const captured = `{"type":"message_end","message":{"role":"assistant","usage":{"input":1305,"output":25,"cacheRead":7,"cacheWrite":3,"reasoning":22,"totalTokens":1330}}}`
	var event piJSONEvent
	if err := json.Unmarshal([]byte(captured), &event); err != nil {
		t.Fatalf("decode captured Pi event: %v", err)
	}
	var usage llmtypes.Usage
	var cacheWrite int
	applyPiJSONUsage(event.Message, &usage, &cacheWrite)

	if usage.InputTokens != 1305 || usage.OutputTokens != 25 || usage.TotalTokens != 1330 {
		t.Fatalf("usage = %#v, want input=1305 output=25 total=1330", usage)
	}
	if usage.ReasoningTokens == nil || *usage.ReasoningTokens != 22 {
		t.Fatalf("reasoning tokens = %#v, want 22", usage.ReasoningTokens)
	}
	if usage.CacheTokens == nil || *usage.CacheTokens != 7 || cacheWrite != 3 {
		t.Fatalf("cache read/write = %#v/%d, want 7/3", usage.CacheTokens, cacheWrite)
	}
}
