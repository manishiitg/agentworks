package llmproviders

import (
	"testing"

	vertexadapter "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/vertex"
)

func TestVertexDefaultModelUsesGemini38Flash(t *testing.T) {
	t.Setenv("VERTEX_PRIMARY_MODEL", "")
	if got := GetDefaultModel(ProviderVertex); got != vertexadapter.ModelGemini38Flash {
		t.Fatalf("GetDefaultModel(ProviderVertex) = %q, want %q", got, vertexadapter.ModelGemini38Flash)
	}
}

func TestGeminiCLIProviderIsRemoved(t *testing.T) {
	if _, err := ValidateProvider("gemini-cli"); err == nil {
		t.Fatal("ValidateProvider(gemini-cli) succeeded, want unsupported provider error")
	}
	if _, ok := GetCodingAgentProviderContract(Provider("gemini-cli"), ""); ok {
		t.Fatal("GetCodingAgentProviderContract(gemini-cli) found a removed provider")
	}
}
