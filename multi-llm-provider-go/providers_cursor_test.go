package llmproviders

import (
	"testing"

	cursorcli "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
)

func TestInitializeCursorCLIWithoutAPIKey(t *testing.T) {
	llm, err := InitializeLLM(Config{
		Provider: ProviderCursorCLI,
		ModelID:  DefaultCursorCLIModel,
	})
	if err != nil {
		t.Fatalf("InitializeLLM(cursor-cli) returned error without API key: %v", err)
	}
	wrapped, ok := llm.(*ProviderAwareLLM)
	if !ok {
		t.Fatalf("InitializeLLM(cursor-cli) returned %T, want *ProviderAwareLLM", llm)
	}
	if _, ok := wrapped.Model.(*cursorcli.CursorCLIAdapter); !ok {
		t.Fatalf("InitializeLLM(cursor-cli) wrapped %T, want *cursorcli.CursorCLIAdapter", wrapped.Model)
	}
}
