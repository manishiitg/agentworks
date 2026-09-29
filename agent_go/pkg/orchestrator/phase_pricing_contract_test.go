package orchestrator

import (
	"math"
	"testing"
)

func TestPhasePricingUsesImmutableClaudeModelRateCards(t *testing.T) {
	for _, tc := range []struct {
		date                                  string
		model                                 string
		input, output, read, write, totalCost float64
	}{
		{"2026-08-02", "claude-opus-5", 0.5, 10, 0.1, 1.875, 12.475},
		{"2026-09-29", "claude-sonnet-5-5", 0.2, 4, 0.04, 0.75, 4.99},
	} {
		t.Run(tc.date+"/"+tc.model, func(t *testing.T) {
			usage := &ModelTokenUsage{
				Provider:         "claude-code",
				InputTokens:      600_000,
				OutputTokens:     400_000,
				CacheTokens:      500_000,
				CacheReadTokens:  200_000,
				CacheWriteTokens: 300_000,
			}
			EnsureModelTokenUsagePricing(tc.model, usage)
			assertClose := func(name string, got, want float64) {
				t.Helper()
				if math.Abs(got-want) > 1e-9 {
					t.Fatalf("%s %s = %.6f, want %.6f", tc.model, name, got, want)
				}
			}
			assertClose("input", usage.InputCost, tc.input)
			assertClose("output", usage.OutputCost, tc.output)
			assertClose("cache read", usage.CacheReadCost, tc.read)
			assertClose("cache write", usage.CacheWriteCost, tc.write)
			assertClose("total", usage.TotalCost, tc.totalCost)
			if usage.PricingModelID != tc.model || usage.PricingVersion != modelPricingVersion {
				t.Fatalf("pricing identity = %q/%q, want %q/%q", usage.PricingModelID, usage.PricingVersion, tc.model, modelPricingVersion)
			}
		})
	}
}
