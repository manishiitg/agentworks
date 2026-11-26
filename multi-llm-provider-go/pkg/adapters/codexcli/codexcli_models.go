package codexcli

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

var knownCodexCLIModels = []string{
	"codex-cli",
	"high",
	"medium",
	"low",
	"gpt-6.2-sol",
	"gpt-6.2-astra",
	"gpt-6-astra",
	"gpt-6.1-sol",
	"gpt-6.1-terra",
	"gpt-6-sol",
	"gpt-6-luna",
}

// gpt-5.3-codex-spark is not in this list on purpose (owner, 2026-10-04): OpenAI refuses it for Codex signed in with a ChatGPT account. Its pricing
// metadata stays in GetModelMetadata so a saved selection still resolves.

// GetAllCodexCLIModels returns the frontend-visible Codex CLI models.
func GetAllCodexCLIModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownCodexCLIModels))
	adapter := &CodexCLIAdapter{}

	for _, modelID := range knownCodexCLIModels {
		meta, err := adapter.GetModelMetadata(modelID)
		if err != nil || meta == nil {
			continue
		}

		switch modelID {
		case "codex-cli":
			meta.ModelName = "Auto (default, pricing varies)"
		case "high":
			meta.ModelName = "High (GPT-6.1 Terra)"
		case "medium":
			meta.ModelName = "Medium (GPT-6.1 Terra)"
		case "low":
			meta.ModelName = "Low (GPT-6 Luna)"
		case "gpt-6.2-sol":
			meta.ModelName = "GPT-6.2 Sol"
		case "gpt-6.2-astra":
			meta.ModelName = "GPT-6.2 Astra"
		case "gpt-6-astra":
			meta.ModelName = "GPT-6 Astra"
		case "gpt-6.1-sol":
			meta.ModelName = "GPT-6.1 Sol"
		case "gpt-6.1-terra":
			meta.ModelName = "GPT-6.1 Terra"
		case "gpt-6-sol":
			meta.ModelName = "GPT-6 Sol"
		case "gpt-6-luna":
			meta.ModelName = "GPT-6 Luna"
		}

		models = append(models, meta)
	}

	return models
}

func resolveCodexCLIModelID(modelID string) string {
	switch strings.TrimSpace(modelID) {
	case "high":
		return "gpt-6.1-terra"
	case "medium":
		return "gpt-6.1-terra"
	case "low":
		return "gpt-6-luna"
	default:
		return strings.TrimSpace(modelID)
	}
}
