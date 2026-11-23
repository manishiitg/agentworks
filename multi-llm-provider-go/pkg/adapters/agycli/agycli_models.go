package agycli

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
)

// DefaultModelID tracks the CLI default. Verified against `agy models`
// output on 2026-09-20: Gemini 3.8 Flash (High) is the session default.
const DefaultModelID = "gemini-3.8-flash-high"

type knownAgyModel struct {
	id   string
	name string
	// base is the Gemini API model behind the effort tier; its prices come
	// from the Gemini registry, so dated price changes apply here too.
	base string
}

// The agy slugs listed by Antigravity's `agy models` docs (2026-11-13): the
// Gemini 4 family and Gemini 3.8 Flash (owner, 2026-11-13: "add all models").
var knownAgyCLIModels = []knownAgyModel{
	{id: "gemini-4-hydrogen-xhigh", name: "Gemini 4 Hydrogen (Extra High)", base: vertex.ModelGemini4Hydrogen},
	{id: "gemini-4-hydrogen-high", name: "Gemini 4 Hydrogen (High)", base: vertex.ModelGemini4Hydrogen},
	{id: "gemini-4-hydrogen-medium", name: "Gemini 4 Hydrogen (Medium)", base: vertex.ModelGemini4Hydrogen},
	{id: "gemini-4-hydrogen-low", name: "Gemini 4 Hydrogen (Low)", base: vertex.ModelGemini4Hydrogen},
	{id: "gemini-4-argon-high", name: "Gemini 4 Argon (High)", base: vertex.ModelGemini4Argon},
	{id: "gemini-4-argon-medium", name: "Gemini 4 Argon (Medium)", base: vertex.ModelGemini4Argon},
	{id: "gemini-4-carbon-high", name: "Gemini 4 Carbon (High)", base: vertex.ModelGemini4Carbon},
	{id: "gemini-4-carbon-medium", name: "Gemini 4 Carbon (Medium)", base: vertex.ModelGemini4Carbon},
	{id: "gemini-4-carbon-low", name: "Gemini 4 Carbon (Low)", base: vertex.ModelGemini4Carbon},
	{id: "gemini-3.8-flash-high", name: "Gemini 3.8 Flash (High)", base: vertex.ModelGemini38Flash},
	{id: "gemini-3.8-flash-medium", name: "Gemini 3.8 Flash (Medium)", base: vertex.ModelGemini38Flash},
	{id: "gemini-3.8-flash-low", name: "Gemini 3.8 Flash (Low)", base: vertex.ModelGemini38Flash},
}

// agyReasoningEffortLevels mirrors the effort tiers baked into agy model
// slugs (xhigh/high/medium/low suffixes in `agy models` output).
var agyReasoningEffortLevels = []string{"low", "medium", "high", "xhigh"}

// GetAgyModelMetadata returns catalog metadata for known ids. Unknown ids
// pass through with the provider set (the CLI resolves them); context and
// pricing stay zero there, not guesses.
func GetAgyModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = DefaultModelID
	}
	for _, known := range knownAgyCLIModels {
		if known.id == modelID {
			var input, out, cached float64
			if price, err := vertex.GetVertexGeminiModelMetadata(known.base); err == nil {
				input, out, cached = price.InputCostPer1MTokens, price.OutputCostPer1MTokens, price.CachedInputCostPer1MTokens
			}
			return &llmtypes.ModelMetadata{
				Provider:                   "agy-cli",
				ModelID:                    known.id,
				ModelName:                  known.name,
				ModelSelectionMode:         "dynamic",
				SupportsToolCalls:          true,
				SupportsReasoningEffort:    true,
				ReasoningEffortLevels:      append([]string(nil), agyReasoningEffortLevels...),
				InputCostPer1MTokens:       input,
				OutputCostPer1MTokens:      out,
				CachedInputCostPer1MTokens: cached,
			}, nil
		}
	}
	return &llmtypes.ModelMetadata{
		Provider:  "agy-cli",
		ModelID:   modelID,
		ModelName: "Antigravity (" + modelID + ")",
	}, nil
}

// GetAllAgyCLIModels returns the catalog models for listings and the
// tier-defaults published check.
func GetAllAgyCLIModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownAgyCLIModels))
	adapter := &AgyCLIAdapter{}
	for _, known := range knownAgyCLIModels {
		meta, err := adapter.GetModelMetadata(known.id)
		if err != nil || meta == nil {
			continue
		}
		models = append(models, meta)
	}
	return models
}

// agyAttachCost copies the turn's token counts onto the generation info and
// prices them from the model's rates, so the cost ledger records an estimate
// instead of "unpriced". Unknown or unpriced models keep no estimate.
func agyAttachCost(gi *llmtypes.GenerationInfo, model string, usage llmtypes.Usage) {
	if gi == nil {
		return
	}
	if gi.Additional == nil {
		gi.Additional = map[string]interface{}{}
	}
	if gi.InputTokens == nil && usage.InputTokens > 0 {
		gi.InputTokens = intPtrAgy(usage.InputTokens)
	}
	if gi.OutputTokens == nil && usage.OutputTokens > 0 {
		gi.OutputTokens = intPtrAgy(usage.OutputTokens)
	}
	if gi.CachedContentTokens == nil && usage.CacheTokens != nil && *usage.CacheTokens > 0 {
		gi.CachedContentTokens = intPtrAgy(*usage.CacheTokens)
	}
	if gi.ReasoningTokens == nil && usage.ThoughtsTokens != nil && *usage.ThoughtsTokens > 0 {
		gi.ReasoningTokens = intPtrAgy(*usage.ThoughtsTokens)
	}
	meta, err := GetAgyModelMetadata(model)
	if err != nil || meta == nil {
		return
	}
	if cost := llmtypes.ComputeUSDCostFromMetadata(meta, gi); cost > 0 {
		gi.Additional["cost_usd_estimated"] = cost
		gi.Additional["cost_model_id"] = meta.ModelID
	}
}
