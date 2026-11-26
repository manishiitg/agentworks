package musecli

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// DefaultModelID tracks the CLI catalog default. Verified against the
// on-disk provider catalog (model-catalog/…meta…json, schema_version 1)
// on 2026-09-10: muse-spark-1.3-contributor is_default + is_current,
// context 1007997, output 128000 for all rows.
const DefaultModelID = "muse-spark-1.3-contributor"

type knownMuseModel struct {
	id      string
	name    string
	context int
	output  int
	// Per-1M-token USD rates (input, output, cached input). Contributor
	// rows carry the data-sharing tier; standard rows the full API rate.
	// Sources 2026-09-10: Vercel AI Gateway changelog + VentureBeat +
	// DataCamp (contributor 0.10/0.20/0.002, standard 1.25/4.25/0.15;
	// standard unchanged 1.2 -> 1.3).
	input  float64
	out    float64
	cached float64
}

var knownMuseCLIModels = []knownMuseModel{
	{id: "muse-wonder-1.0", name: "Muse Wonder"},
	{id: "muse-wonder-full-1.0", name: "Muse Wonder Full (Muse Code subscribers)"},
	// Ember: in the CLI catalog (schema_version 2, visible, released 2026-10-01,
	// context 1007997, output 128000, efforts minimal..max). Price from Meta's
	// announcement (research.meta.ai/blog/muse-ember): 0.10 input, 0.50 output,
	// 0.004 cached input per 1M tokens for prompts up to 200K tokens; longer
	// prompts cost 0.30/1.50/0.012, which this flat rate does not model.
	{id: "muse-ember-1.0", name: "Muse Ember", context: 1007997, output: 128000, input: 0.10, out: 0.50, cached: 0.004},
	{id: "muse-spark-1.3-contributor", name: "Muse Spark 1.3 Contributor", context: 1007997, output: 128000, input: 0.10, out: 0.20, cached: 0.002},
	{id: "muse-spark-1.3", name: "Muse Spark 1.3", context: 1007997, output: 128000, input: 1.25, out: 4.25, cached: 0.15},
}

// museReasoningEffortLevels mirrors `muse exec --help`: Meta reasoning
// effort none|minimal|low|medium|high|xhigh|max|ultra (default high).
var museReasoningEffortLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}

// GetMuseModelMetadata returns catalog metadata for known ids. Unknown ids
// pass through with the provider set (the CLI resolves them); context and
// pricing stay zero there, not guesses.
func GetMuseModelMetadata(modelID string) (*llmtypes.ModelMetadata, error) {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		modelID = DefaultModelID
	}
	for _, known := range knownMuseCLIModels {
		if known.id == modelID {
			return &llmtypes.ModelMetadata{
				Provider:                   "muse-cli",
				ModelID:                    known.id,
				ModelName:                  known.name,
				ContextWindow:              known.context,
				ModelSelectionMode:         "dynamic",
				InputCostPer1MTokens:       known.input,
				OutputCostPer1MTokens:      known.out,
				CachedInputCostPer1MTokens: known.cached,
				SupportsToolCalls:          true,
				SupportsReasoningEffort:    true,
				ReasoningEffortLevels:      append([]string(nil), museReasoningEffortLevels...),
			}, nil
		}
	}
	return &llmtypes.ModelMetadata{
		Provider:  "muse-cli",
		ModelID:   modelID,
		ModelName: "Muse (" + modelID + ")",
	}, nil
}

// GetAllMuseCLIModels returns the catalog models for listings and the
// tier-defaults published check.
func GetAllMuseCLIModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownMuseCLIModels))
	adapter := &MuseCLIAdapter{}
	for _, known := range knownMuseCLIModels {
		meta, err := adapter.GetModelMetadata(known.id)
		if err != nil || meta == nil {
			continue
		}
		models = append(models, meta)
	}
	return models
}
