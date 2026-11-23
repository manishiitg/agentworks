package cursorcli

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"testing"
)

func TestResolveCursorCLIModelIDDefaultsToAuto(t *testing.T) {
	for _, modelID := range []string{"", "cursor-cli", "high", "medium", "low"} {
		if got := resolveCursorCLIModelID(modelID); got != "auto" {
			t.Fatalf("resolveCursorCLIModelID(%q) = %q, want auto", modelID, got)
		}
	}
}

func TestResolveCursorCLIModelIDLeavesAutoUnpinned(t *testing.T) {
	if got := resolveCursorCLIModelID("auto"); got != "auto" {
		t.Fatalf("resolveCursorCLIModelID(auto) = %q, want explicit auto selector", got)
	}
}

func TestResolveCursorCLIModelIDKeepsExplicitModel(t *testing.T) {
	if got := resolveCursorCLIModelID("gpt-5"); got != "gpt-5" {
		t.Fatalf("resolveCursorCLIModelID(gpt-5) = %q, want gpt-5", got)
	}
	if got := resolveCursorCLIModelID("composer-2.5"); got != "composer-2.5" {
		t.Fatalf("resolveCursorCLIModelID(composer-2.5) = %q, want composer-2.5", got)
	}
	if got := resolveCursorCLIModelID("grok-4.7"); got != "grok-4.7" {
		t.Fatalf("resolveCursorCLIModelID(grok-4.7) = %q, want grok-4.7", got)
	}
}

// Owner, 2 Nov: Auto is listed, so a person whose Composer allowance ran out can switch to it; it keeps its
// estimated-average pricing label.
func TestCursorModelsListAuto(t *testing.T) {
	for _, model := range GetAllCursorCLIModels() {
		if model.ModelID == "auto" {
			return
		}
	}
	t.Fatal("Cursor's model list has no Auto")
}

func TestGrok47PricingModes(t *testing.T) {
	adapter := &CursorCLIAdapter{}
	for _, tt := range []struct {
		model                       string
		input, cached, output, long float64
	}{
		{"grok-4.7", 2, 0.5, 6, 2},
		{"grok-4.7[context=500k,effort=high,fast=true]", 4, 1, 12, 1.5},
	} {
		meta, err := adapter.GetModelMetadata(tt.model)
		if err != nil {
			t.Fatalf("GetModelMetadata(%s): %v", tt.model, err)
		}
		if meta.InputCostPer1MTokens != tt.input || meta.CachedInputCostPer1MTokens != tt.cached ||
			meta.OutputCostPer1MTokens != tt.output || meta.LongContextThresholdTokens != 256000 ||
			meta.LongContextInputMultiplier != tt.long || meta.LongContextOutputMultiplier != tt.long {
			t.Errorf("%s metadata = %+v", tt.model, meta)
		}
	}
}

func TestNewCursorSelectorsKeepTheirModelAndPricing(t *testing.T) {
	for _, tt := range []struct {
		id                    string
		input, cached, output float64
		context               int
	}{
		{"glm-5.3", 1.4, 0.26, 4.4, 1000000},
		{"glm-5.3-flash", 0.15, 0.029, 0.5, 1000000},
		{"grok-4.6", 2, 0.5, 6, 256000},
		{"grok-4.6[effort=high,fast=true]", 4, 1, 12, 256000},
		{"glm-5.3[effort=max]", 1.4, 0.26, 4.4, 1000000},
	} {
		if got := cursorCLIModelForLaunch(tt.id); got != tt.id {
			t.Fatalf("launch selector = %q, want %q", got, tt.id)
		}
		meta, err := (&CursorCLIAdapter{}).GetModelMetadata(tt.id)
		if err != nil {
			t.Fatal(err)
		}
		if meta.InputCostPer1MTokens != tt.input || meta.CachedInputCostPer1MTokens != tt.cached || meta.OutputCostPer1MTokens != tt.output || meta.ContextWindow != tt.context {
			t.Errorf("%s metadata = %+v", tt.id, meta)
		}
	}
}

func TestCursorModelReasoningPreservesOtherSettings(t *testing.T) {
	for _, tt := range []struct{ model, effort, want string }{
		{"grok-4.6", "low", "grok-4.6[effort=low]"},
		{"grok-4.7[context=500k,effort=high,fast=false]", "xhigh", "grok-4.7[context=500k,fast=false,effort=xhigh]"},
		{"glm-5.3-flash[effort=low]", "max", "glm-5.3-flash[effort=max]"},
		{"glm-5.3", "medium", "glm-5.3"},
		{"auto", "high", "auto"},
		{"composer-2.5", "high", "composer-2.5"},
		{"cursor-grok-4.6-low-fast", "high", "cursor-grok-4.6-low-fast"},
		{"grok-4.6[effort=high,fast=true]", "", "grok-4.6[effort=high,fast=true]"},
	} {
		if got := cursorModelWithReasoning(tt.model, &llmtypes.CallOptions{ReasoningEffort: tt.effort}); got != tt.want {
			t.Errorf("%s + %s = %s, want %s", tt.model, tt.effort, got, tt.want)
		}
	}
}
