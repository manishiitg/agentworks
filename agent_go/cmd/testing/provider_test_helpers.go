package testing

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"

	"github.com/joho/godotenv"
)

// Shared helpers for the provider test commands.

func loadTestingEnvFiles() {
	_ = godotenv.Load("agent_go/.env")
	_ = godotenv.Load(".env")
	_ = godotenv.Load("../.env")
}

func parseCSVList(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parseProviderModelOverrides(value string) map[string]string {
	overrides := map[string]string{}
	for _, part := range parseCSVList(value) {
		provider, model, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		provider = strings.ToLower(strings.TrimSpace(provider))
		model = strings.TrimSpace(model)
		if provider != "" && model != "" {
			overrides[provider] = model
		}
	}
	return overrides
}

func loadWorkspaceProviderKeys(ctx context.Context, workspaceURL string) (map[string]string, error) {
	raw, exists, err := services.LoadProviderKeys(ctx, workspaceURL)
	if err != nil || !exists {
		return nil, err
	}
	keys := map[string]string{}
	for key, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			keys[key] = strings.TrimSpace(text)
		}
	}
	return keys, nil
}

func oneLinePreview(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
