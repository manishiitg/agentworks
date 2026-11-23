package llmproviders

import (
	"strings"
	"testing"

	kimiadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/kimi"
)

func TestInitializeKimiDefaultsToK26APIAndRequiresAPIKey(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "")

	_, err := initializeKimi(Config{
		Provider: ProviderKimi,
	})
	if err == nil {
		t.Fatal("initializeKimi returned nil error without KIMI_API_KEY")
	}
	if !strings.Contains(err.Error(), "KIMI_API_KEY is required") {
		t.Fatalf("initializeKimi error = %q, want KIMI_API_KEY requirement", err.Error())
	}
	if got := GetDefaultModel(ProviderKimi); got != kimiadapter.ModelKimiK26 {
		t.Fatalf("GetDefaultModel(ProviderKimi) = %q, want %q", got, kimiadapter.ModelKimiK26)
	}
}

func TestInitializeKimiK26AcceptsConfiguredAPIKey(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "sk-test")

	llm, err := initializeKimi(Config{
		Provider: ProviderKimi,
		ModelID:  kimiadapter.ModelKimiK26,
	})
	if err != nil {
		t.Fatalf("initializeKimi returned error for %s with API key: %v", kimiadapter.ModelKimiK26, err)
	}
	if llm == nil {
		t.Fatal("initializeKimi returned nil model")
	}
}

func TestInitializeKimiCodeIsRemoved(t *testing.T) {
	t.Setenv("KIMI_API_KEY", "sk-test")

	_, err := initializeKimi(Config{
		Provider: ProviderKimi,
		ModelID:  kimiadapter.ModelKimiCode,
	})
	if err == nil {
		t.Fatal("initializeKimi returned nil error for removed kimi-code model")
	}
	if !strings.Contains(err.Error(), "use kimi-k2.7-code") {
		t.Fatalf("initializeKimi error = %q, want Kimi replacement guidance", err.Error())
	}
}
