package step_based_workflow

import (
	"strings"
	"testing"
)

func TestRenderResolvedWorkflowLLMRolesIncludesEveryProviderProfileRole(t *testing.T) {
	manifest := `{"capabilities":{"llm_config":{"schema_version":2,"mode":"provider_profile","provider":"claude-code"}}}`
	got, err := renderResolvedWorkflowLLMRoles(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"builder", "execution_high", "execution_medium", "execution_low", "pulse", "provider_profile:claude-code"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderResolvedWorkflowLLMRolesShowsReasoningSourceAndOverride(t *testing.T) {
	model := `{"provider":"codex-cli","model_id":"gpt-5.6","options":{"reasoning_effort":"high"}}`
	manifest := `{"capabilities":{"llm_config":{"schema_version":2,"mode":"explicit","builder_llm":` + model + `,"background_llm":` + model + `,"tiered_config":{"tier_1":` + model + `,"tier_2":` + model + `,"tier_3":` + model + `}}}}`
	got, err := renderResolvedWorkflowLLMRoles(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"codex-cli/gpt-5.6", "high", "explicit_override", "true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}
