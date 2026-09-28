package step_based_workflow

import (
	"strings"
	"testing"
)

// The live failure: all four evaluation steps in a workflow were pinned to
// provider "claude-code" with model_id "claude-code". Nothing validated it, so
// every eval died at turn 1 with "all LLMs failed (primary + 0 fallbacks)".
func TestValidateStepLLMConfigRejectsProviderNameAsModel(t *testing.T) {
	got := validateStepLLMConfig("execution_llm", "", "claude-code", "claude-code")
	if got == "" {
		t.Fatal("provider name repeated in the model slot must be rejected")
	}
	for _, want := range []string{"provider name repeated", "all LLMs failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("message should explain the failure, missing %q: %s", want, got)
		}
	}
	// A CLI provider gets the tool that actually lists its models.
	if !strings.Contains(got, "list_coding_agent_models") {
		t.Fatalf("CLI provider should be pointed at the CLI model list: %s", got)
	}
}

func TestValidateStepLLMConfigRejectsHalfConfigured(t *testing.T) {
	for name, tc := range map[string]struct{ provider, model, want string }{
		"provider only": {"anthropic", "", "no model_id"},
		"model only":    {"", "claude-sonnet-5", "no provider"},
		"both empty":    {"", "", "set but empty"},
	} {
		t.Run(name, func(t *testing.T) {
			got := validateStepLLMConfig("execution_llm", "", tc.provider, tc.model)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("expected %q in: %s", tc.want, got)
			}
		})
	}
}

// A published id resolves provider and model on its own, so demanding the pair
// as well would reject a perfectly valid override.
func TestValidateStepLLMConfigAcceptsPublishedIDAlone(t *testing.T) {
	if got := validateStepLLMConfig("execution_llm", "pub-123", "", ""); got != "" {
		t.Fatalf("published_llm_id alone is valid, got: %s", got)
	}
}

func TestValidateStepLLMConfigAcceptsRealPairings(t *testing.T) {
	for _, tc := range [][2]string{
		{"anthropic", "claude-sonnet-5"},
		{"claude-code", "claude-sonnet-5"},
		{"openai", "gpt-4o"},
	} {
		if got := validateStepLLMConfig("execution_llm", "", tc[0], tc[1]); got != "" {
			t.Fatalf("%s/%s should be accepted, got: %s", tc[0], tc[1], got)
		}
	}
}

func TestValidateStepLLMConfigEnforcesAgyAlphaGate(t *testing.T) {
	t.Setenv("AGY_ALPHA", "")
	t.Setenv("MULTI_USER_MODE", "")
	if got := validateStepLLMConfig("execution_llm", "", "agy-cli", "gemini-3.8-flash-high"); got == "" {
		t.Fatal("AGY accepted without alpha flag")
	}
	t.Setenv("AGY_ALPHA", "1")
	if got := validateStepLLMConfig("execution_llm", "", "agy-cli", "gemini-3.8-flash-high"); got != "" {
		t.Fatalf("AGY rejected in local alpha mode: %s", got)
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if got := validateStepLLMConfig("execution_llm", "", "agy-cli", "gemini-3.8-flash-high"); got == "" {
		t.Fatal("AGY accepted in multi-user mode")
	}
}

func TestCollectStepLLMConfigsUsesSelectedModel(t *testing.T) {
	cfg := &AgentLLMConfig{Provider: "anthropic", ModelID: "claude-sonnet-5"}
	got := collectStepLLMConfigsForValidation(cfg)
	if len(got) != 1 || got[0].label != "execution_llm" || got[0].provider != cfg.Provider || got[0].modelID != cfg.ModelID {
		t.Fatalf("expected only the selected model, got %+v", got)
	}
}

func TestCollectStepLLMConfigsNilSafe(t *testing.T) {
	if got := collectStepLLMConfigsForValidation(nil); len(got) != 0 {
		t.Fatalf("no override means nothing to validate, got %#v", got)
	}
}
