package picli

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The structured path hardcoded an empty provider override while the
// interactive path passed piProviderFromOptions(opts). A caller overriding the
// provider therefore got it silently ignored in structured mode -- and because
// this same value selects the API-key variable names, it also got the wrong
// credentials injected for whatever provider actually ran.
func TestStructuredHonorsTheProviderOverride(t *testing.T) {
	opts := &llmtypes.CallOptions{}
	WithProvider("google-vertex")(opts)

	adapter := NewPiCLIAdapter("k", "gemini-3.7-flash", &mockLogger{})
	provider, model := adapter.resolveStructuredProviderModel(opts)

	if provider != "google-vertex" {
		t.Fatalf("provider = %q, want the caller's override google-vertex", provider)
	}
	if model != "gemini-3.7-flash" {
		t.Fatalf("model = %q, want gemini-3.7-flash", model)
	}

	// The credential names follow the resolved provider, which is the second
	// half of the bug: an ignored override injects the wrong ones.
	env := piAPIKeyEnv(provider, "k")
	if len(env) == 0 {
		t.Fatal("no API key env produced for the overridden provider")
	}
	for _, entry := range env {
		if entry == "GEMINI_API_KEY=k" || entry == "GOOGLE_API_KEY=k" || entry == "PI_API_KEY=k" {
			return // google-vertex shares google's key names
		}
	}
	t.Fatalf("unexpected credential env for google-vertex: %v", env)
}

// Without an override the resolved provider still comes from the model id, so
// the fix must not change the default path.
func TestStructuredWithoutOverrideStillResolvesFromModelID(t *testing.T) {
	adapter := NewPiCLIAdapter("k", "zai/glm-5.3", &mockLogger{})
	provider, model := adapter.resolveStructuredProviderModel(&llmtypes.CallOptions{})
	if provider != "zai" || model != "glm-5.3" {
		t.Fatalf("resolved %q/%q, want zai/glm-5.3", provider, model)
	}
}
