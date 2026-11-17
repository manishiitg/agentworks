package codexcli

import "testing"

func TestCodexCurrentModelPricing(t *testing.T) {
	adapter := &CodexCLIAdapter{}
	for _, tt := range []struct {
		model                        string
		context                      int
		input, cached, write, output float64
	}{
		{"gpt-6.2-sol", 1050000, 2, 0.1, 2.5, 10},
		{"gpt-6.2-astra", 1050000, 8, 0.4, 10, 40},
		{"gpt-6.1-sol", 1050000, 2, 0.1, 2.5, 10},
		{"gpt-6.1-terra", 500000, 1, 0.05, 1.25, 5},
		{"gpt-6-sol", 1050000, 2, 0.2, 2.5, 10},
		{"gpt-6-luna", 1050000, 0.1, 0.01, 0.125, 0.5},
	} {
		meta, err := adapter.GetModelMetadata(tt.model)
		if err != nil {
			t.Fatalf("GetModelMetadata(%s): %v", tt.model, err)
		}
		if meta.InputCostPer1MTokens != tt.input || meta.CachedInputCostPer1MTokens != tt.cached ||
			meta.CachedInputCostWritePer1MTokens != tt.write || meta.OutputCostPer1MTokens != tt.output ||
			meta.ContextWindow != tt.context || meta.LongContextThresholdTokens != 272000 {
			t.Errorf("%s metadata = %+v", tt.model, meta)
		}
	}
}
