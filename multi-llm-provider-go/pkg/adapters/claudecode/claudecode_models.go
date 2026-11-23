package claudecode

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// claudeCodeRetiredModels maps a retired model to the one that replaced it.
// Workflows and Crews keep the model they saved; the adapters run and price
// the replacement, so a retirement never leaves a saved config broken or
// unpriced. Claude Sonnet 5 was replaced by Claude Sonnet 5.5 (2026-09-29);
// Claude Haiku 4.5 by Claude Haiku 5.5 (2026-10-08, owner).
var claudeCodeRetiredModels = map[string]string{
	"claude-sonnet-5":           "claude-sonnet-5-5",
	"claude-haiku-4-5":          "claude-haiku-5-5",
	"claude-haiku-4-5-20251001": "claude-haiku-5-5",
}

// CurrentClaudeCodeModel returns modelID, or its replacement when retired.
func CurrentClaudeCodeModel(modelID string) string {
	if replacement, ok := claudeCodeRetiredModels[strings.ToLower(strings.TrimSpace(modelID))]; ok {
		return replacement
	}
	return modelID
}

var knownClaudeCodeModels = []string{
	"claude-code",
	"claude-fable-5-2",
	"claude-fable-5-1",
	"claude-opus-5-5",
	"claude-sonnet-5-5",
	"claude-haiku-5-5",
}

// GetAllClaudeCodeModels returns the frontend-visible Claude Code CLI models.
func GetAllClaudeCodeModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownClaudeCodeModels))
	adapter := NewClaudeCodeInteractiveAdapter("claude-code", nil)

	for _, modelID := range knownClaudeCodeModels {
		meta, err := adapter.GetModelMetadata(modelID)
		if err != nil || meta == nil {
			continue
		}

		switch modelID {
		case "claude-code":
			meta.ModelName = "Auto (default, pricing varies)"
		case "claude-fable-5-2":
			meta.ModelName = "Fable 5.2"
		case "claude-fable-5-1":
			meta.ModelName = "Fable 5.1"
		case "claude-opus-5-5":
			meta.ModelName = "Opus 5.5"
		case "claude-sonnet-5-5":
			meta.ModelName = "Sonnet 5.5"
		case "claude-haiku-5-5":
			meta.ModelName = "Haiku 5.5"
		}

		meta.SupportsReasoningEffort = true
		meta.ReasoningEffortLevels = []string{"low", "medium", "high", "max"}
		models = append(models, meta)
	}

	return models
}
