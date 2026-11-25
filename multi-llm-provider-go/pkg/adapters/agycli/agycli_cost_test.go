package agycli

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Regression: AGY turns were recorded as "unpriced" because the effort-tier
// model IDs carried no rates and no estimate was attached.
func TestAgyAttachCostPricesDefaultModel(t *testing.T) {
	gi := &llmtypes.GenerationInfo{}
	agyAttachCost(gi, DefaultModelID, llmtypes.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	cost, _ := gi.Additional["cost_usd_estimated"].(float64)
	if cost < 4.49 || cost > 4.51 {
		t.Fatalf("cost_usd_estimated = %v, want 4.50 (0.75 input + 3.75 output)", cost)
	}
}

// Regression: an Antigravity chat on Gemini 4 Neon or a Claude model through agy
// showed "Cost so far $0.00": only a fixed list of slugs had rates.
func TestAgyPricesSlugsFromTheAccountList(t *testing.T) {
	for _, slug := range []string{"gemini-4-neon-high", "gemini-4-hydrogen-xhigh", "gemini-pro-orchestrator-argon", "claude-opus-5-5-high"} {
		meta, err := GetAgyModelMetadata(slug)
		if err != nil || meta.InputCostPer1MTokens <= 0 || meta.OutputCostPer1MTokens <= 0 {
			t.Fatalf("%s: unpriced (%+v, %v)", slug, meta, err)
		}
	}
	if meta, _ := GetAgyModelMetadata("gpt-oss-120b-medium"); meta.InputCostPer1MTokens != 0 {
		t.Fatalf("a model with no known rate must stay unpriced, got %+v", meta)
	}
}
