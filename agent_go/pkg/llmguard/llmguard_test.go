package llmguard

import "testing"

func TestAgyUsesTheSameProviderAdmissionAsOtherCLIs(t *testing.T) {
	t.Setenv("AGY_ALPHA", "")
	t.Setenv("AGY_ALPHA_MULTI_USER", "")
	for _, mode := range []string{"", "true"} {
		t.Run("multi_user="+mode, func(t *testing.T) {
			t.Setenv("MULTI_USER_MODE", mode)
			for _, provider := range []string{"agy-cli", "codex-cli"} {
				if err := RequireCodingAgentProvider(provider); err != nil {
					t.Fatalf("CLI %s rejected: %v", provider, err)
				}
			}
			if err := RequireCodingAgentProvider("openai"); err == nil {
				t.Fatal("direct API provider accepted")
			}
		})
	}
}
