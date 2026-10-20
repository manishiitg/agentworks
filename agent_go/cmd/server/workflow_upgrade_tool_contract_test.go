package server

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// A contract-upgrade prompt is executable product configuration: when it tells
// the scheduled Builder to call a tool, that exact public name must survive
// native registration, plan-tool consolidation, capability admission, and the
// final session catalog. Checking product.yaml against registration alone is
// insufficient: both can agree on a consolidated name while an upgrade prompt
// still calls the hidden native name (the v1.0.42 regression).
var (
	upgradeFunctionCallPattern   = regexp.MustCompile(`\b([a-z][a-z0-9_]+)\s*\(`)
	upgradeImperativeToolPattern = regexp.MustCompile(`(?i)\b(?:call|using|with)\s+(?:the\s+)?([a-z][a-z0-9_]+)\b`)
	upgradeToolVerbPattern       = regexp.MustCompile(`^(?:add|change|configure|consolidate|create|debug|delete|execute|get|install|list|maintain|manage|mark|migrate|perform|preview|query|read|record|reorganize|request|review|run|search|send|set|stop|strip|submit|trigger|uninstall|update|validate)_`)
)

func explicitUpgradeToolReferences(prompt string, callable map[string]bool) []string {
	references := map[string]bool{}
	add := func(name string) {
		name = strings.ToLower(strings.TrimSpace(name))
		// Ordinary prose after "with"/"using" is not a tool reference. Public
		// tool names are snake_case; function-call syntax is handled separately.
		if strings.Contains(name, "_") && upgradeToolVerbPattern.MatchString(name) {
			references[name] = true
		}
	}
	for _, match := range upgradeFunctionCallPattern.FindAllStringSubmatch(prompt, -1) {
		add(match[1])
	}
	for _, match := range upgradeImperativeToolPattern.FindAllStringSubmatch(prompt, -1) {
		add(match[1])
	}
	// Some instructions name a tool declaratively (for example,
	// "update_scripted_step reports an error") rather than with "call". Once a
	// name is in the real catalog, any whole-word mention is a tool reference.
	for name := range callable {
		if strings.Contains(prompt, name) {
			references[name] = true
		}
	}
	result := make([]string, 0, len(references))
	for name := range references {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func scheduledBuilderCallableTools(t *testing.T) map[string]bool {
	t.Helper()
	server, _ := newFakeWorkspaceServer(t)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	const sessionID = "schedule-cron--upgrade-tool-contract"
	logger := loggerv2.NewNoop()
	session, err := workflow.NewWorkshopChatSession(context.Background(), &workflow.WorkshopConfig{
		Logger:        logger,
		WorkspacePath: "Workflow/upgrade-tool-contract",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	api := &StreamingAPI{logger: logger}
	api.workshopChatSessions.Store(sessionID, session)
	draft := &productSurfaceDraft{}
	if err := api.installWorkflowPhaseTools(
		context.Background(), draft, sessionID, "test-user", "workflow-builder",
		"Workflow/upgrade-tool-contract", "", map[string]string{"WorkshopMode": "workshop"},
		nil, nil, nil, nil, nil, QueryRequest{TriggeredBy: "cron"}, false,
	); err != nil {
		t.Fatal(err)
	}

	callable := map[string]bool{}
	for name := range draft.tools {
		callable[name] = true
	}
	// Workspace/human/database tools are installed through the workflow tool
	// pool rather than installWorkflowPhaseTools, but are present in the same
	// live scheduled session.
	baseTools, _, _ := createCustomTools(true, "default", sessionID)
	for _, tool := range baseTools {
		if tool.Function != nil {
			callable[tool.Function.Name] = true
		}
	}
	// These are intrinsic MCP-agent tools, not product-registered custom tools.
	for _, name := range []string{"get_api_spec", "get_prompt", "get_resource", "read_skill"} {
		callable[name] = true
	}
	return callable
}

func TestWorkflowUpgradePromptsOnlyCallScheduledBuilderTools(t *testing.T) {
	callable := scheduledBuilderCallableTools(t)
	upgrades := workflowVersionUpgradePlan(&WorkflowManifest{Version: workflowContractNestedAgentArtifactsVersion})
	if len(upgrades) == 0 {
		t.Fatal("no workflow contract upgrades were collected")
	}
	for _, upgrade := range upgrades {
		for _, name := range explicitUpgradeToolReferences(upgrade.query, callable) {
			if !callable[name] {
				t.Errorf("upgrade %q instructs the scheduled Builder to call unavailable tool %q", upgrade.label, name)
			}
		}
	}
}

func TestExplicitUpgradeToolReferencesFindsHiddenNativeName(t *testing.T) {
	refs := explicitUpgradeToolReferences(
		"Do only this migration. Call migrate_hidden_native_tool once, then stop.",
		map[string]bool{"maintain_plan": true},
	)
	if len(refs) != 1 || refs[0] != "migrate_hidden_native_tool" {
		t.Fatalf("hidden native tool reference was not detected: %v", refs)
	}
}
