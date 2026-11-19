package llmproviders

import (
	"fmt"
	"os"

	kimiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
	zaiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/zai"
)

func GetDefaultModel(provider Provider) string {
	switch provider {
	case ProviderBedrock:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("BEDROCK_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "us.anthropic.claude-sonnet-4-20250514-v1:0"
	case ProviderOpenAI:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("OPENAI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "gpt-4.1-mini"
	case ProviderAnthropic:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("ANTHROPIC_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "claude-sonnet-4-6"
	case ProviderOpenRouter:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("OPENROUTER_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "moonshotai/kimi-k2"
	case ProviderVertex:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("VERTEX_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return vertexadapter.ModelGemini38Flash
	case ProviderAzure:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("AZURE_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "gpt-4o"
	case ProviderZAI:
		if primaryModel := os.Getenv("ZAI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return zaiadapter.ModelGLM51
	case ProviderKimi:
		if primaryModel := os.Getenv("KIMI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return kimiadapter.ModelKimiK26
	case ProviderClaudeCode:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("CLAUDE_CODE_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return "claude-code"
	case ProviderCodexCLI:
		// Get primary model from environment variable
		if primaryModel := os.Getenv("CODEX_CLI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return DefaultCodexCLIModel
	case ProviderCursorCLI:
		if primaryModel := os.Getenv("CURSOR_CLI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return DefaultCursorCLIModel
	case ProviderMuseCLI:
		if primaryModel := os.Getenv("MUSE_CLI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return DefaultMuseCLIModel
	case ProviderAgyCLI:
		if primaryModel := os.Getenv("AGY_CLI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return DefaultAgyCLIModel
	case ProviderACPCLI:
		if primaryModel := os.Getenv("ACP_CLI_PRIMARY_MODEL"); primaryModel != "" {
			return primaryModel
		}
		return DefaultACPCLIModel
	default:
		return ""
	}
}

// ValidateProvider checks if the provider is supported
func ValidateProvider(provider string) (Provider, error) {
	switch Provider(provider) {
	case ProviderBedrock, ProviderOpenAI, ProviderAnthropic, ProviderOpenRouter, ProviderVertex, ProviderAzure, ProviderZAI, ProviderKimi, ProviderClaudeCode, ProviderCodexCLI, ProviderCursorCLI, ProviderPiCLI, ProviderMuseCLI, ProviderAgyCLI, ProviderACPCLI, ProviderMiniMax, ProviderMiniMaxCodingPlan:
		return Provider(provider), nil
	default:
		return "", fmt.Errorf("unsupported provider: %s. Supported providers: bedrock, openai, anthropic, openrouter, vertex, azure, z-ai, kimi, claude-code, codex-cli, cursor-cli, pi-cli, muse-cli, agy-cli, acp-cli, minimax, minimax-coding-plan", provider)
	}
}

// ProviderAwareLLM is a wrapper around LLM that preserves provider information
// and automatically captures token usage in LLM events
