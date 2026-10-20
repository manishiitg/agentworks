package virtualtools

import (
	"github.com/manishiitg/mcpagent/llm"
	"testing"
)

func TestImageAnalysisAPIKeysWithEnvSuppliesClaudeCodeOAuthToken(t *testing.T) {
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "test-claude-oauth-token")

	keys := imageAnalysisAPIKeysWithEnv(nil)
	if keys == nil || keys.ClaudeCodeOAuthToken == nil {
		t.Fatal("image analysis keys did not include the Claude Code OAuth token")
	}
	if got := *keys.ClaudeCodeOAuthToken; got != "test-claude-oauth-token" {
		t.Fatalf("Claude Code OAuth token = %q, want injected test token", got)
	}
}

func TestHasWorkspaceDefaultImageAnalysisAuthRequiresClaudeCodeOAuthToken(t *testing.T) {
	if hasWorkspaceDefaultImageAnalysisAuth(string(llm.ProviderClaudeCode), nil) {
		t.Fatal("Claude Code must not be selected as an image-analysis default without an OAuth token")
	}
}
