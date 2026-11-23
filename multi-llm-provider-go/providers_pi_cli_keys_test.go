package llmproviders

import "testing"

func TestPiCLIAPIKeyForProviderUsesSelectedProvider(t *testing.T) {
	googleKey := "google-key"
	zaiKey := "zai-key"
	kimiKey := "kimi-key"
	minimaxKey := "minimax-key"
	openRouterKey := "openrouter-key"
	keys := &ProviderAPIKeys{
		PiCLI:      &googleKey,
		ZAI:        &zaiKey,
		Kimi:       &kimiKey,
		MiniMax:    &minimaxKey,
		OpenRouter: &openRouterKey,
		PiProviderKeys: map[string]string{
			"zai-coding-cn": "zai-cn-key",
			"deepseek":      "deepseek-key",
		},
	}

	tests := []struct {
		provider string
		want     string
	}{
		{provider: "google", want: "google-key"},
		{provider: "zai", want: "zai-key"},
		{provider: "zai-coding-cn", want: "zai-cn-key"},
		{provider: "kimi-coding", want: "kimi-key"},
		{provider: "minimax", want: "minimax-key"},
		{provider: "openrouter", want: "openrouter-key"},
		{provider: "deepseek", want: "deepseek-key"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got, _ := piCLIAPIKeyForProvider(keys, tt.provider)
			if got != tt.want {
				t.Fatalf("piCLIAPIKeyForProvider(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPiCLIAPIKeyForProviderDoesNotReuseGeminiKeyForZAI(t *testing.T) {
	googleKey := "google-key"
	keys := &ProviderAPIKeys{PiCLI: &googleKey}

	got, _ := piCLIAPIKeyForProvider(keys, "zai")
	if got != "" {
		t.Fatalf("zai key = %q, want empty when only generic Pi/Gemini auth exists", got)
	}
}
