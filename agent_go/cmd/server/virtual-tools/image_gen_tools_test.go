package virtualtools

import (
	"strings"
	"testing"
)

// Neither Vertex nor codex-cli has a real structured API parameter for
// GPT Image 2.5's quality/size/background — imageRequirementsSuffix is the
// only way to pass them through at all, folded into the prompt text.
func TestImageRequirementsSuffixFoldsIntoPromptText(t *testing.T) {
	tests := []struct {
		name string
		args map[string]interface{}
		want string
	}{
		{"no hints at all", map[string]interface{}{}, ""},
		{
			"auto is a no-op, not worth cluttering the prompt",
			map[string]interface{}{"quality": "auto", "size": "auto", "background": "auto"},
			"",
		},
		{
			"one hint",
			map[string]interface{}{"quality": "high"},
			"\n\nImage requirements — quality: high.",
		},
		{
			"all three, in a stable order",
			map[string]interface{}{"quality": "xhigh", "size": "2048x2048", "background": "transparent"},
			"\n\nImage requirements — quality: xhigh, exact output size: 2048x2048, background: transparent.",
		},
		{
			"non-string values are ignored rather than panicking",
			map[string]interface{}{"quality": 5},
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageRequirementsSuffix(tc.args); got != tc.want {
				t.Fatalf("imageRequirementsSuffix(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestNormalizeImageProviderAndModelProviderAliasDefaultsModel(t *testing.T) {
	tests := []struct {
		name      string
		provider  string
		modelID   string
		wantModel string
	}{
		{
			name:      "codex alias",
			provider:  "codex-cli",
			modelID:   "codex-cli",
			wantModel: "codex-cli",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, modelID, err := normalizeImageProviderAndModel(tt.provider, tt.modelID)
			if err != nil {
				t.Fatalf("normalizeImageProviderAndModel returned error: %v", err)
			}
			if provider != tt.provider {
				t.Fatalf("provider = %q, want %q", provider, tt.provider)
			}
			if modelID != tt.wantModel {
				t.Fatalf("modelID = %q, want %q", modelID, tt.wantModel)
			}
		})
	}
}

func TestNormalizeImageProviderAndModelRejectsWrongModelForProvider(t *testing.T) {
	_, _, err := normalizeImageProviderAndModel("codex-cli", "gemini-3.1-flash-image")
	if err == nil {
		t.Fatal("normalizeImageProviderAndModel returned nil error for unsupported provider/model pair")
	}
}

// image_gen/image_edit generation dropped Vertex (see supportedImageProviderSummary);
// codex-cli is the only generation provider now.
func TestNormalizeImageProviderAndModelRejectsVertexAsGenerationProvider(t *testing.T) {
	_, _, err := normalizeImageProviderAndModel("vertex", "gemini-3.1-flash-image")
	if err == nil {
		t.Fatal("normalizeImageProviderAndModel returned nil error for retired vertex generation provider")
	}
	if !strings.Contains(err.Error(), "unsupported image generation provider") {
		t.Fatalf("error = %v, want unsupported provider", err)
	}
}

func TestNormalizeImageProviderAndModelRejectsMiniMaxCodingPlan(t *testing.T) {
	_, _, err := normalizeImageProviderAndModel("minimax-coding-plan", "")
	if err == nil {
		t.Fatal("normalizeImageProviderAndModel returned nil error for removed minimax-coding-plan provider")
	}
}

func TestNormalizeImageProviderAndModelRejectsTierLabelsAsImageModels(t *testing.T) {
	for _, tier := range []string{"low", "medium", "high", "auto"} {
		t.Run(tier, func(t *testing.T) {
			_, _, err := normalizeImageProviderAndModel("codex-cli", tier)
			if err == nil {
				t.Fatal("normalizeImageProviderAndModel returned nil error for tier label")
			}
			if !strings.Contains(err.Error(), "unsupported image generation model") {
				t.Fatalf("error = %v, want unsupported model", err)
			}
		})
	}
}
