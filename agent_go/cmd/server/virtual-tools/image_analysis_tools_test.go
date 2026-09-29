package virtualtools

import "testing"

func TestNormalizeImageAnalysisProviderAndModelRejectsDirectAPIProviders(t *testing.T) {
	for _, provider := range []string{"vertex", "kimi", "z-ai", "openai", "anthropic"} {
		if _, _, err := normalizeImageAnalysisProviderAndModel(provider, ""); err == nil {
			t.Fatalf("normalizeImageAnalysisProviderAndModel(%q) returned nil error", provider)
		}
	}
}

func TestNormalizeImageAnalysisProviderAndModelDefaultsToCodex(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("", "")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "codex-cli" || modelID != "gpt-5.4-mini" {
		t.Fatalf("got %s/%s, want codex-cli/gpt-5.4-mini", provider, modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelRejectsMiniMax(t *testing.T) {
	_, _, err := normalizeImageAnalysisProviderAndModel("minimax-coding-plan", "")
	if err == nil {
		t.Fatal("normalizeImageAnalysisProviderAndModel returned nil error for minimax-coding-plan")
	}
}

func TestNormalizeImageAnalysisProviderAndModelCodexDefault(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("codex-cli", "")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "codex-cli" {
		t.Fatalf("provider = %q, want codex-cli", provider)
	}
	if modelID != "gpt-5.4-mini" {
		t.Fatalf("modelID = %q, want gpt-5.4-mini", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelClaudeCodeDefault(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("claude-code", "")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "claude-code" {
		t.Fatalf("provider = %q, want claude-code", provider)
	}
	if modelID != "claude-code" {
		t.Fatalf("modelID = %q, want claude-code", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelCursorDefault(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("cursor-cli", "")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "cursor-cli" {
		t.Fatalf("provider = %q, want cursor-cli", provider)
	}
	if modelID != "cursor-cli" {
		t.Fatalf("modelID = %q, want cursor-cli", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelInfersCodexFromModel(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("", "gpt-5.4-mini")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "codex-cli" {
		t.Fatalf("provider = %q, want codex-cli", provider)
	}
	if modelID != "gpt-5.4-mini" {
		t.Fatalf("modelID = %q, want gpt-5.4-mini", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelInfersCursorFromModel(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("", "sonnet-4-thinking")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "cursor-cli" {
		t.Fatalf("provider = %q, want cursor-cli", provider)
	}
	if modelID != "sonnet-4-thinking" {
		t.Fatalf("modelID = %q, want sonnet-4-thinking", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelInfersClaudeCodeFromSonnet5(t *testing.T) {
	provider, modelID, err := normalizeImageAnalysisProviderAndModel("", "claude-sonnet-5-5")
	if err != nil {
		t.Fatalf("normalizeImageAnalysisProviderAndModel returned error: %v", err)
	}
	if provider != "claude-code" {
		t.Fatalf("provider = %q, want claude-code", provider)
	}
	if modelID != "claude-sonnet-5-5" {
		t.Fatalf("modelID = %q, want claude-sonnet-5-5", modelID)
	}
}

func TestNormalizeImageAnalysisProviderAndModelRejectsUnknownModel(t *testing.T) {
	if _, _, err := normalizeImageAnalysisProviderAndModel("", "kimi-k2.6"); err == nil {
		t.Fatal("normalizeImageAnalysisProviderAndModel returned nil error for a non-CLI vision model")
	}
}

func TestPathBasedImageAnalysisProviderIncludesNativeCLIs(t *testing.T) {
	for _, provider := range []string{"codex-cli", "cursor-cli", "claude-code"} {
		if !pathBasedImageAnalysisProvider(provider) {
			t.Fatalf("pathBasedImageAnalysisProvider(%q) = false, want true", provider)
		}
	}
	if pathBasedImageAnalysisProvider("vertex") {
		t.Fatal("pathBasedImageAnalysisProvider(vertex) = true, want false")
	}
}
