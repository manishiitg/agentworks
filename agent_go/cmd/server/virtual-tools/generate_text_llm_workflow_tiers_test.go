package virtualtools

import (
	"strings"
	"testing"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestLoadWorkflowTierModelUsesOnlyBoundWorkflowConfig(t *testing.T) {
	t.Setenv("DELEGATION_TIER_HIGH_PROVIDER", "must-not-be-used")
	t.Setenv("DELEGATION_TIER_HIGH_MODEL", "global-model")

	workflowModel := &TierModel{Provider: "cursor-cli", ModelID: "auto"}
	got, err := loadWorkflowTierModel(&WorkflowLLMTierConfig{High: workflowModel}, "high")
	if err != nil {
		t.Fatalf("loadWorkflowTierModel() error = %v", err)
	}
	if got.Provider != "cursor-cli" || got.ModelID != "auto" {
		t.Fatalf("loadWorkflowTierModel() = %+v, want current workflow model", got)
	}
}

func TestLoadWorkflowTierModelFailsWithoutCurrentWorkflow(t *testing.T) {
	t.Setenv("DELEGATION_TIER_HIGH_PROVIDER", "must-not-be-used")
	t.Setenv("DELEGATION_TIER_HIGH_MODEL", "global-model")

	_, err := loadWorkflowTierModel(nil, "high")
	if err == nil || !strings.Contains(err.Error(), "requires a current workflow") {
		t.Fatalf("loadWorkflowTierModel() error = %v, want current-workflow error", err)
	}
}

func TestLoadWorkflowTierModelFailsWhenRequestedWorkflowTierIsMissing(t *testing.T) {
	_, err := loadWorkflowTierModel(&WorkflowLLMTierConfig{
		High: &TierModel{Provider: "cursor-cli", ModelID: "auto"},
	}, "low")
	if err == nil || !strings.Contains(err.Error(), "current workflow's capabilities.llm_config") {
		t.Fatalf("loadWorkflowTierModel() error = %v, want missing workflow-tier error", err)
	}
}

func TestStructuredOneShotCallOptionsForceEveryCodingCLI(t *testing.T) {
	tests := []struct {
		provider llm.Provider
		key      string
	}{
		{provider: llm.ProviderClaudeCode, key: "claude_code_structured_transport"},
		{provider: llm.ProviderCodexCLI, key: "codex_structured_transport"},
		{provider: llm.ProviderCursorCLI, key: "cursor_structured_transport"},
	}

	for _, tc := range tests {
		t.Run(string(tc.provider), func(t *testing.T) {
			opts := &llmtypes.CallOptions{}
			for _, option := range structuredOneShotCallOptions(tc.provider) {
				option(opts)
			}
			if opts.Metadata == nil || opts.Metadata.Custom == nil || opts.Metadata.Custom[tc.key] != true {
				t.Fatalf("structuredOneShotCallOptions(%q) metadata = %+v, want %s=true", tc.provider, opts.Metadata, tc.key)
			}
		})
	}

	if options := structuredOneShotCallOptions(llm.ProviderOpenAI); len(options) != 0 {
		t.Fatalf("API provider received CLI transport options: %d", len(options))
	}
}
