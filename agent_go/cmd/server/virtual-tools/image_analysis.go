package virtualtools

import (
	"fmt"
	"strings"

	llm "github.com/manishiitg/multi-llm-provider-go"
)

// read_image analysis runs only through coding-agent CLIs, which inspect the
// image file directly from the workspace path.
const defaultImageAnalysisProvider = "codex-cli"

func defaultImageAnalysisModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "cursor-cli":
		return "cursor-cli"
	case "claude-code":
		return "claude-code"
	default:
		return "gpt-5.4-mini"
	}
}

func inferImageAnalysisProviderFromModel(modelID string) string {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	// claude-sonnet-5 is retired; the claude-code adapter runs it as 5.5.
	case "claude-code", "claude-sonnet-5-5", "claude-sonnet-5", "claude-sonnet-4-6":
		return "claude-code"
	case "cursor-cli", "gpt-5", "sonnet-4", "sonnet-4-thinking":
		return "cursor-cli"
	case "codex-cli", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex", "gpt-5.3-codex-spark":
		return "codex-cli"
	default:
		return ""
	}
}

func normalizeImageAnalysisProviderAndModel(provider, modelID string) (string, string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	modelID = strings.TrimSpace(modelID)

	if provider == "" && modelID != "" {
		provider = inferImageAnalysisProviderFromModel(modelID)
		if provider == "" {
			return "", "", fmt.Errorf("unsupported image analysis model %q. %s", modelID, supportedImageAnalysisProviderSummary())
		}
	}
	if provider == "" {
		provider = defaultImageAnalysisProvider
	}
	if modelID == "" {
		modelID = defaultImageAnalysisModelForProvider(provider)
	}

	if !pathBasedImageAnalysisProvider(provider) {
		return "", "", fmt.Errorf("unsupported image analysis provider %q. %s", provider, supportedImageAnalysisProviderSummary())
	}
	return provider, modelID, nil
}

func hasImageAnalysisProviderAuth(provider string, _ *llm.ProviderAPIKeys) bool {
	return pathBasedImageAnalysisProvider(provider)
}

func supportedImageAnalysisProviderSummary() string {
	return "Supported image analysis providers: codex-cli (codex-cli, gpt-5.4, gpt-5.4-mini, gpt-5.3-codex, gpt-5.3-codex-spark), cursor-cli (cursor-cli, gpt-5, sonnet-4-thinking, sonnet-4), claude-code (claude-code, claude-sonnet-5-5, claude-sonnet-4-6)"
}

func pathBasedImageAnalysisProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "codex-cli", "cursor-cli", "claude-code":
		return true
	default:
		return false
	}
}
