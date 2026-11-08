package orchestrator

import (
	"slices"
	"strings"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// PLAT-746: the upgrade check missed retired selectors; a missing runtime
// resource must not turn a current static name into a retired name.
func TestSavedCustomToolSelectionsUseStaticDefinitions(t *testing.T) {
	base, err := NewBaseOrchestrator(loggerv2.NewNoop(), nil, OrchestratorTypeWorkflow, "", 0, "", nil, nil, false, &LLMConfig{}, 1, nil, nil, nil)
	if err != nil || len(base.WorkspaceTools) != 0 {
		t.Fatalf("fixture must have no admitted runtime tools: %v", err)
	}
	for _, selection := range []string{"workspace_browser:browser.execute", "workspace_browser:agent_browser", "human_tools:messages.email_send", "human_tools:messages.slack_send", "workspace_advanced:terminal.execute", "workspace_advanced:files.patch", "agent_messaging:send_message", "workspace_advanced:*"} {
		if err := ValidateCustomToolSelection(selection); err != nil {
			t.Fatalf("current definition %s was mistaken for a retired resource: %v", selection, err)
		}
	}
	for selection, guidance := range map[string]string{"human_tools:notify_user": "send_email", "workspace_advanced:files.image": "native image reader", "human_tools:missing_tool": "absent from the current catalog"} {
		if err := ValidateCustomToolSelection(selection); err == nil || !strings.Contains(err.Error(), guidance) {
			t.Fatalf("stale selection %s lacks its repair: %v", selection, err)
		}
	}
}

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
	if !slices.Contains(selections, "human_tools:send_slack_message") {
		t.Fatalf("Builder selections are missing product-declared send tools: %v", selections)
	}
	if slices.Contains(selections, "human_tools:human_feedback") {
		t.Fatalf("Builder selections include run-only human_feedback: %v", selections)
	}
}

func TestHumanToolsCategoryIncludesQuestionAndSendTools(t *testing.T) {
	names := getToolNamesByCategory("human_tools")
	for _, name := range []string{
		"human_feedback",
		"send_email",
		"send_slack_message",
	} {
		if !names[name] {
			t.Fatalf("human_tools category missing %q", name)
		}
	}
}

func TestBaseOrchestratorPreRegistersRuntimeCategoriesWithoutMutatingInput(t *testing.T) {
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
	functionTools := agentworksproduct.ExecutionFunctionTools()
	for _, name := range functionTools.Tools {
		if got := base.ToolCategories[name]; got != functionTools.Category {
			t.Fatalf("category for %s=%q, want %q", name, got, functionTools.Category)
		}
	}
}
