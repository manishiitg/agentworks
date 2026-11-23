package openai

import "testing"

func TestOpenAIGPT55MetadataIncludesPricing(t *testing.T) {
	meta, err := GetOpenAIModelMetadata("gpt-5.5-2026-04-23")
	if err != nil {
		t.Fatalf("GetOpenAIModelMetadata: %v", err)
	}
	if meta.ModelID != "gpt-5.5-2026-04-23" {
		t.Fatalf("ModelID = %q, want original snapshot id", meta.ModelID)
	}
	if meta.InputCostPer1MTokens != 5.00 || meta.OutputCostPer1MTokens != 30.00 || meta.CachedInputCostPer1MTokens != 0.50 {
		t.Fatalf("GPT-5.5 pricing = in %.2f cached %.2f out %.2f, want 5.00/0.50/30.00",
			meta.InputCostPer1MTokens, meta.CachedInputCostPer1MTokens, meta.OutputCostPer1MTokens)
	}
	if meta.ContextWindow != 1050000 {
		t.Fatalf("ContextWindow = %d, want 1050000", meta.ContextWindow)
	}
}

func TestOpenAIGPT6Pricing(t *testing.T) {
	for _, tt := range []struct {
		model                        string
		input, cached, write, output float64
	}{
		{ModelGPT6Sol, 2, 0.2, 2.5, 10},
		{ModelGPT6Luna, 0.1, 0.01, 0.125, 0.5},
	} {
		meta, err := GetOpenAIModelMetadata(tt.model)
		if err != nil {
			t.Fatalf("GetOpenAIModelMetadata(%s): %v", tt.model, err)
		}
		if meta.InputCostPer1MTokens != tt.input || meta.CachedInputCostPer1MTokens != tt.cached ||
			meta.CachedInputCostWritePer1MTokens != tt.write || meta.OutputCostPer1MTokens != tt.output ||
			meta.ContextWindow != 1050000 || meta.LongContextThresholdTokens != 272000 {
			t.Errorf("%s metadata = %+v", tt.model, meta)
		}
	}
}

func TestOpenAICatalogOmitsRetiredGPT56Family(t *testing.T) {
	for _, model := range GetAllOpenAIModels() {
		for _, retired := range []string{"gpt-5.6-sol", "gpt-5.6-luna"} {
			if model.ModelID == retired {
				t.Errorf("OpenAI catalog still lists %q", retired)
			}
		}
	}
}
