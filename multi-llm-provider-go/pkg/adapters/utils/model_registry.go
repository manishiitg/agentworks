package utils

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/anthropic"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/azure"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/bedrock"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/minimax"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/openai"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"
)

// GetAllModelMetadata aggregates model metadata from all supported providers
func GetAllModelMetadata() []*llmtypes.ModelMetadata {
	var allModels []*llmtypes.ModelMetadata

	// OpenAI
	allModels = append(allModels, openai.GetAllOpenAIModels()...)

	// Anthropic
	allModels = append(allModels, anthropic.GetAllAnthropicModels()...)

	// Bedrock
	allModels = append(allModels, bedrock.GetAllBedrockModels()...)

	// Vertex
	allModels = append(allModels, vertex.GetAllVertexGeminiModels()...)

	// MiniMax
	allModels = append(allModels, minimax.GetAllMiniMaxModels()...)

	// MiniMax Coding Plan (uses Anthropic model names)
	allModels = append(allModels, minimax.GetAllMiniMaxCodingPlanModels()...)

	// Azure - static models (dynamic API requires endpoint/key at runtime)
	allModels = append(allModels, azure.GetAllAzureModelMetadata()...)

	// Z.AI
	allModels = append(allModels, zai.GetDefaultVisibleZAIModels()...)

	// Kimi
	allModels = append(allModels, kimi.GetAllKimiModels()...)

	// Claude Code CLI
	allModels = append(allModels, claudecode.GetAllClaudeCodeModels()...)

	// Codex CLI
	allModels = append(allModels, codexcli.GetAllCodexCLIModels()...)

	// Cursor CLI
	allModels = append(allModels, cursorcli.GetAllCursorCLIModels()...)

	// Pi CLI
	allModels = append(allModels, picli.GetAllPiCLIModels()...)

	// OpenRouter - fetch dynamically from API (cached for 24 hours)
	openRouterModels := openai.GetAllOpenRouterModels()
	if len(openRouterModels) > 0 {
		allModels = append(allModels, openRouterModels...)
	} else {
		// Fallback to hardcoded popular models if API fails
		allModels = append(allModels, getPopularOpenRouterModels()...)
	}

	return allModels
}

func getPopularOpenRouterModels() []*llmtypes.ModelMetadata {
	return []*llmtypes.ModelMetadata{
		{
			Provider:              "openrouter",
			ModelID:               "anthropic/claude-3.5-sonnet",
			ModelName:             "Claude 3.5 Sonnet (OpenRouter)",
			InputCostPer1MTokens:  3.00,
			OutputCostPer1MTokens: 15.00,
			ContextWindow:         200000,
			SupportsToolCalls:     true,
		},
		{
			Provider:              "openrouter",
			ModelID:               "openai/gpt-4o",
			ModelName:             "GPT-4o (OpenRouter)",
			InputCostPer1MTokens:  2.50,
			OutputCostPer1MTokens: 10.00,
			ContextWindow:         128000,
			SupportsToolCalls:     true,
		},
		{
			Provider:              "openrouter",
			ModelID:               "google/gemini-pro-1.5",
			ModelName:             "Gemini Pro 1.5 (OpenRouter)",
			InputCostPer1MTokens:  1.25,
			OutputCostPer1MTokens: 5.00,
			ContextWindow:         2000000,
			SupportsToolCalls:     true,
		},
		{
			Provider:              "openrouter",
			ModelID:               "x-ai/grok-2-1212",
			ModelName:             "Grok 2 (OpenRouter)",
			InputCostPer1MTokens:  2.00,
			OutputCostPer1MTokens: 10.00,
			ContextWindow:         128000,
			SupportsToolCalls:     true,
		},
		{
			Provider:              "openrouter",
			ModelID:               "deepseek/deepseek-chat",
			ModelName:             "DeepSeek V3 (OpenRouter)",
			InputCostPer1MTokens:  0.14,
			OutputCostPer1MTokens: 0.28,
			ContextWindow:         64000,
			SupportsToolCalls:     true,
		},
	}
}
