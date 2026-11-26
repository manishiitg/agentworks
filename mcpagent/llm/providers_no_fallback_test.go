package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultsDoNotExposeRetiredFallbacks(t *testing.T) {
	t.Setenv("OPENROUTER_FALLBACK_MODELS", "backup")
	t.Setenv("OPENROUTER_CROSS_FALLBACK_PROVIDER", "openai")
	t.Setenv("OPENROUTER_CROSS_FALLBACK_MODELS", "backup")
	t.Setenv("BEDROCK_FALLBACK_MODELS", "backup")
	t.Setenv("OPENAI_FALLBACK_MODELS", "backup")
	raw, err := json.Marshal(GetLLMDefaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"fallbacks"`, `"fallback_models"`, `"cross_provider_fallback"`} {
		if strings.Contains(string(raw), key) {
			t.Fatalf("defaults still expose %s", key)
		}
	}
}
