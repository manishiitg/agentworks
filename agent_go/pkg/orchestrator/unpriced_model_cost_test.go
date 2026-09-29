package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

// Pi is the execution transport for several model providers. Google-prefixed
// models must resolve through the Gemini rate card instead of inheriting Pi's
// intentionally unpriced generic metadata.
func TestPiGoogleCallsUseGeminiPricing(t *testing.T) {
	modelData := &ModelTokenData{
		Provider:     "pi-cli",
		ModelID:      "google/gemini-3.8-flash",
		InputTokens:  10_000,
		OutputTokens: 2_000,
		LLMCallCount: 4,
	}
	inputCost, outputCost, reasoningCost, cacheCost, totalCost, _, pricingFound := calculatePricingFromModelData(modelData)
	if !pricingFound {
		t.Fatal("Pi-routed Google Gemini models must use the Gemini rate card")
	}
	if inputCost <= 0 || outputCost <= 0 || totalCost <= 0 {
		t.Fatalf("expected non-zero Gemini input/output/total costs, got input=%v output=%v reasoning=%v cache=%v total=%v",
			inputCost, outputCost, reasoningCost, cacheCost, totalCost)
	}

	usage := buildModelTokenUsage(modelData)
	if usage.Unpriced {
		t.Fatal("Pi-routed Gemini usage must not be marked unpriced")
	}

	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(encoded)
	if strings.Contains(body, `"unpriced"`) {
		t.Fatalf("priced Gemini usage must not carry an unpriced marker, got: %s", body)
	}
}

func TestUnknownPiModelsRemainExplicitlyUnpriced(t *testing.T) {
	usage := buildModelTokenUsage(&ModelTokenData{
		Provider: "pi-cli", ModelID: "custom/unknown-model", InputTokens: 10_000, OutputTokens: 2_000,
	})
	if !usage.Unpriced || usage.TotalCost != 0 {
		t.Fatalf("unknown Pi model should remain explicitly unpriced, got %+v", usage)
	}
}

// A model with a real rate card (claude-code/claude-sonnet-5-5) must not carry
// the unpriced marker, and its JSON must not regress by suddenly gaining an
// unpriced key that didn't exist before this change.
func TestPricedProviderCallsAreNotMarkedUnpriced(t *testing.T) {
	modelData := &ModelTokenData{
		Provider:     "claude-code",
		ModelID:      "claude-sonnet-5-5",
		InputTokens:  10_000,
		OutputTokens: 2_000,
		LLMCallCount: 4,
	}
	_, _, _, _, _, _, pricingFound := calculatePricingFromModelData(modelData)
	if !pricingFound {
		t.Fatal("claude-sonnet-5-5 has a real rate card; pricingFound should be true")
	}

	usage := buildModelTokenUsage(modelData)
	if usage.Unpriced {
		t.Fatal("a priced model must not be marked Unpriced")
	}
	if usage.TotalCost <= 0 {
		t.Fatalf("expected a real nonzero total cost, got %v", usage.TotalCost)
	}

	encoded, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"unpriced"`) {
		t.Fatalf("a priced call must not carry the unpriced key at all (omitempty), got: %s", encoded)
	}
}

// TestMuseCLICallsHaveRealPricing pins a real gap found live 2026-09-11:
// getModelMetadata's provider switch had cases for claude-code, codex-cli,
// cursor-cli, and pi-cli but none for muse-cli, so every muse-cli call fell
// through to the "unsupported provider" default and every cost in the
// AgentWorks UI showed $0/unpriced -- even though multi-llm-provider-go's
// musecli.GetMuseModelMetadata already carries real, sourced per-1M-token
// rates for muse-spark-1.3-contributor. Wiring in the missing case is the
// fix; this pins it the same way TestPricedProviderCallsAreNotMarkedUnpriced
// pins claude-code.
func TestMuseCLICallsHaveRealPricing(t *testing.T) {
	modelData := &ModelTokenData{
		Provider:     "muse-cli",
		ModelID:      "muse-spark-1.3-contributor",
		InputTokens:  10_000,
		OutputTokens: 2_000,
		LLMCallCount: 4,
	}
	_, _, _, _, _, _, pricingFound := calculatePricingFromModelData(modelData)
	if !pricingFound {
		t.Fatal("muse-spark-1.3-contributor has a real rate card; pricingFound should be true")
	}

	usage := buildModelTokenUsage(modelData)
	if usage.Unpriced {
		t.Fatal("a priced muse-cli model must not be marked Unpriced")
	}
	if usage.TotalCost <= 0 {
		t.Fatalf("expected a real nonzero total cost, got %v", usage.TotalCost)
	}
}
