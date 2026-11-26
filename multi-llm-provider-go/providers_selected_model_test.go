package llmproviders

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultsIgnoreRetiredFallbackEnvironment(t *testing.T) {
	for _, key := range []string{
		"OPENROUTER_FALLBACK_MODELS", "OPENROUTER_CROSS_FALLBACK_MODELS",
		"OPENROUTER_CROSS_FALLBACK_PROVIDER", "BEDROCK_FALLBACK_MODELS",
		"BEDROCK_CROSS_FALLBACK_MODELS", "BEDROCK_OPENAI_FALLBACK_MODELS",
		"OPENAI_FALLBACK_MODELS", "OPENAI_CROSS_FALLBACK_MODELS",
		"OPENAI_BEDROCK_FALLBACK_MODELS", "CODEX_CLI_FALLBACK_MODELS",
		"ZAI_FALLBACK_MODELS",
	} {
		t.Setenv(key, "retired-backup")
	}
	data, err := json.Marshal(GetLLMDefaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"retired-backup", "fallback_models", "cross_provider_fallback"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("defaults expose %q", forbidden)
		}
	}
}

func TestLegacyBackupCannotChangeSelectedInitialization(t *testing.T) {
	var config Config
	if err := json.Unmarshal([]byte(`{"Provider":"unavailable-provider","ModelID":"selected","FallbackModels":["backup"]}`), &config); err != nil {
		t.Fatal(err)
	}
	model, err := InitializeLLM(config)
	if model != nil || err == nil || !strings.Contains(err.Error(), "unavailable-provider") {
		t.Fatalf("selected initialization error must be returned: model=%v err=%v", model, err)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "FallbackModels") || config.ModelID != "selected" {
		t.Fatalf("legacy backup retained: %s", data)
	}
}
