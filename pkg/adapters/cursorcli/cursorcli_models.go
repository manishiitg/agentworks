package cursorcli

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var knownCursorCLIModels = []string{
	// Cursor's Auto router is the default (owner, 21 Nov): every Cursor plan has it, while a free account refuses
	// Composer ("plan_required"). An account without Auto answers with Cursor's own error; pick Composer then.
	"auto",
	"composer-2.5",
	"grok-4.7",
	"grok-4.6",
	"glm-5.3",
	"glm-5.3-flash",
}

// GetAllCursorCLIModels returns the frontend-visible Cursor Agent CLI models.
func GetAllCursorCLIModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownCursorCLIModels))
	adapter := &CursorCLIAdapter{}

	for _, modelID := range knownCursorCLIModels {
		meta, err := adapter.GetModelMetadata(modelID)
		if err != nil || meta == nil {
			continue
		}
		meta.ModelSelectionMode = "dynamic"
		models = append(models, meta)
	}

	return models
}

// resolveCursorCLIModelID maps the LLM-config-side model alias to the concrete
// --model arg passed to cursor-agent.
//
// Generic selectors and no model run on Cursor's Auto router, the one model
// every Cursor plan has (a free account refuses Composer). It is passed as
// `--model auto` because omitting the flag can retain the account's current
// pinned model. Grok 4.7 is a canonical ID.
// Explicit Cursor model IDs such as
// composer-2.5, gpt-5, or sonnet-4-thinking still pass through unchanged.
func resolveCursorCLIModelID(modelID string) string {
	switch strings.TrimSpace(modelID) {
	case "", "cursor-cli", "high", "medium", "low", "auto":
		return "auto"
	default:
		return strings.TrimSpace(modelID)
	}
}
