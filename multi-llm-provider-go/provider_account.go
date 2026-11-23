package llmproviders

import (
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type accountBoundModel struct {
	llmtypes.Model
	environment map[string]string
	credentials map[string]string
}

func (m *accountBoundModel) GenerateContent(ctx context.Context, messages []llmtypes.MessageContent, options ...llmtypes.CallOption) (*llmtypes.ContentResponse, error) {
	scoped := append([]llmtypes.CallOption(nil), options...)
	scoped = append(scoped, llmtypes.WithProviderAccountEnvironment(m.environment), llmtypes.WithProviderAccountCredentials(m.credentials))
	return m.Model.GenerateContent(ctx, messages, scoped...)
}

func accountCredentials(config Config) map[string]string {
	result := map[string]string{}
	put := func(name string, key *string) {
		if key != nil && *key != "" {
			result[name] = *key
		}
	}
	switch config.Provider {
	case ProviderCodexCLI:
		put("OPENAI_API_KEY", config.APIKeys.CodexCLI)
		put("CODEX_API_KEY", config.APIKeys.CodexCLI)
	case ProviderClaudeCode:
		put("CLAUDE_CODE_OAUTH_TOKEN", config.APIKeys.ClaudeCodeOAuthToken)
	case ProviderCursorCLI:
		put("CURSOR_API_KEY", config.APIKeys.CursorCLI)
	case ProviderMuseCLI:
		put("META_API_KEY", config.APIKeys.MuseCLI)
	}
	return result
}
