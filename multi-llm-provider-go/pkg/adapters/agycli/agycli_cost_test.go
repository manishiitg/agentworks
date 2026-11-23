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
