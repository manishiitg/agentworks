package claudecode

import (
	"reflect"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestClaudeStructuredProcessEnvUsesAdapterOAuthToken(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=ambient-api-key",
		"ANTHROPIC_AUTH_TOKEN=ambient-auth-token",
		"CLAUDE_CODE_OAUTH_TOKEN=ambient-oauth-token",
		"KEEP=value",
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCodingAgentSecretEnvironment(map[string]string{
		"SECRET_WORKFLOW": "secret-value",
	})(opts)

	got := claudeStructuredProcessEnv(base, opts, "  workflow-oauth-token  ")
	want := []string{
		"PATH=/usr/bin",
		"KEEP=value",
		"SECRET_WORKFLOW=secret-value",
		"CLAUDE_CODE_OAUTH_TOKEN=workflow-oauth-token",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured Claude environment = %#v, want %#v", got, want)
	}
}

func TestClaudeStructuredProcessEnvWithoutAdapterTokenPreservesExistingAuth(t *testing.T) {
	base := []string{"PATH=/usr/bin", "CLAUDE_CODE_OAUTH_TOKEN=ambient-oauth-token"}
	got := claudeStructuredProcessEnv(base, nil, "  ")
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("structured Claude environment = %#v, want unchanged %#v", got, base)
	}
}

// PLAT-872: SDK and structured mode share account isolation while the server
// account retains its explicit API/gateway configuration.
func TestClaudeSDKAccountDoesNotInheritAmbientCredentials(t *testing.T) {
	base := []string{"HOME=/server", "ANTHROPIC_API_KEY=ambient", "ANTHROPIC_AUTH_TOKEN=ambient", "ANTHROPIC_BASE_URL=https://example.com", "CLAUDE_CODE_OAUTH_TOKEN=ambient"}
	if got := claudeStructuredProcessEnv(base, nil, ""); !reflect.DeepEqual(got, base) {
		t.Fatal("server account lost configured credentials")
	}
	opts := &llmtypes.CallOptions{}
	llmtypes.WithProviderAccountEnvironment(map[string]string{"HOME": "/selected"})(opts)
	for _, token := range []string{"", "scoped"} {
		got := claudeStructuredProcessEnv(base, opts, token)
		want := []string{"HOME=/selected"}
		if token != "" {
			want = append(want, "CLAUDE_CODE_OAUTH_TOKEN=scoped")
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("private account environment not isolated: %#v", got)
		}
	}
}
