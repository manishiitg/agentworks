package orchestrator

import (
	"slices"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestWorkspaceAdvancedCategoryIncludesProviderMediaTools(t *testing.T) {
	names := getToolNamesByCategory("workspace_advanced")

	for _, name := range []string{
		"generate_text_llm",
	} {
		if !names[name] {
			t.Fatalf("workspace_advanced category missing %q", name)
		}
	}
}

func TestBuilderHumanToolSelectionsComeFromProductYAMLWithoutWildcard(t *testing.T) {
	selections := AgentWorksChatHumanToolSelections("builder")
	if slices.Contains(selections, "human_tools:*") {
		t.Fatal("Builder human-tool selection must not use a category wildcard")
	}
	if !slices.Contains(selections, "human_tools:notify_user") || !slices.Contains(selections, "human_tools:get_notification_history") {
		t.Fatalf("Builder selections are missing product-declared notification tools: %v", selections)
	}
	if slices.Contains(selections, "human_tools:human_feedback") {
		t.Fatalf("Builder selections include run-only human_feedback: %v", selections)
	}
}

func TestHumanToolsCategoryIncludesDurableWorkflowDecisionTools(t *testing.T) {
	names := getToolNamesByCategory("human_tools")
	for _, name := range []string{
		"human_feedback",
		"notify_user",
		"get_human_input_request",
		"create_human_input_request",
		"answer_human_input_request",
		"mark_human_input_consumed",
	} {
		if !names[name] {
			t.Fatalf("human_tools category missing %q", name)
		}
	}
}

func TestBaseOrchestratorPreRegistersDelegationCategoriesWithoutMutatingInput(t *testing.T) {
	input := map[string]string{"existing": "workspace"}
	base, err := NewBaseOrchestrator(
		loggerv2.NewNoop(), nil, OrchestratorTypeWorkflow, "", 0, "",
		nil, nil, false, &LLMConfig{}, 1, nil, nil, input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 1 {
		t.Fatalf("constructor mutated caller category map: %#v", input)
	}
	wantCategory := virtualtools.GetSubAgentToolCategory()
	for _, tool := range virtualtools.CreateSubAgentTools() {
		if got := base.ToolCategories[tool.Function.Name]; got != wantCategory {
			t.Fatalf("category for %s=%q, want %q", tool.Function.Name, got, wantCategory)
		}
	}
}
