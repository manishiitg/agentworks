package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestRelayBuilderUsesOwnPromptSkillAndToolGate(t *testing.T) {
	prompt, _, _, err := buildWorkflowPhaseSystemPrompt("workflow-builder", map[string]string{
		"WorkflowKind": "relay", "WorkspacePath": "Workflow/relay-test", "WorkshopMode": "workshop",
	}, promptContext{}, "## Current authenticated user\n- user_id: test")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"You are the Flow Builder", "Workflow/relay-test", "Current authenticated user", "then do what the owner asked in the same", "No confirmation question, wait or extra gate", "existing chat history records it"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("Relay prompt missing %q", required)
		}
	}
	for _, absent := range []string{"## CURRENT MODE:", "## CURRENT STATE", "Autopilot step"} {
		if strings.Contains(prompt, absent) {
			t.Fatalf("Relay inherited AgentWorks instructions %q", absent)
		}
	}

	server, _ := newFakeWorkspaceServer(t)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	logger := loggerv2.NewNoop()
	session, err := workflow.NewWorkshopChatSession(context.Background(), &workflow.WorkshopConfig{Logger: logger, WorkspacePath: "Workflow/relay-test"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	api := &StreamingAPI{logger: logger}
	api.workshopChatSessions.Store("relay-surface", session)
	draft := &productSurfaceDraft{}
	if err := api.installWorkflowPhaseTools(context.Background(), draft, "relay-surface", "test-user", "workflow-builder", "Workflow/relay-test", "", map[string]string{"WorkshopMode": "workshop", "WorkflowKind": "relay"}, nil, nil, nil, nil, nil, QueryRequest{}, false); err != nil {
		t.Fatal(err)
	}
	expectedSkills, _ := relayproduct.ChatSkills("builder")
	if len(draft.skills) != len(expectedSkills) || draft.skills[0].Name != "relay-builder" {
		t.Fatalf("Relay skills: %v", draft.skills)
	}
	for _, name := range []string{"test_relay", "get_relay_run", "manage_workflow_webhook", "get_relay_releases", "publish_relay", "validate_report_html", "preview_report", "get_report_link"} {
		if _, registered := draft.tools[name]; !registered {
			t.Errorf("shared workflow registration did not provide Relay tool %s", name)
		}
	}
	tools, err := relayproduct.BuilderTools()
	if err != nil {
		t.Fatal(err)
	}
	gate := newProductToolGateForAllowlist("relays", tools)
	filtered := map[string]recordedTool{}
	for name, tool := range draft.tools {
		if gate.Allows(name) {
			filtered[name] = tool
		}
	}
	logConfigurationCatalog(t, "flow-builder-phase", filtered)
	logDataCatalog(t, "flow-builder-phase", filtered)
	runTools, err := relayproduct.RunTools()
	if err != nil {
		t.Fatal(err)
	}
	runGate := newProductToolGateForAllowlist("relays-run", runTools)
	runFiltered := map[string]recordedTool{}
	for name, tool := range draft.tools {
		if runGate.Allows(name) {
			runFiltered[name] = tool
		}
	}
	logConfigurationCatalog(t, "flow-run", runFiltered)
	logDataCatalog(t, "flow-run", runFiltered)
	if !gate.Admit("test_relay") || !gate.Admit("publish_relay") || !gate.Admit("preview_report") || !gate.Admit("query_workflow_db") || gate.Admit("create_slack_bot_route") {
		t.Fatal("Relay tool admission differs from product.yaml")
	}
	// The owner's MCP Builder has the same Flow tools as the website Builder.
	claims := &UserClaims{ExternalBuilderOperationID: "operation"}
	gate.DenyWhere(func(name string) bool { return externalBuilderToolDenied(claims, name) })
	for _, name := range []string{"read_file", "list_files", "search_files", "write_file", "test_relay", "publish_relay"} {
		if !gate.Admit(name) {
			t.Errorf("Relay MCP Builder lost Builder tool %s", name)
		}
	}
}

func TestRelayBuilderChatIsSeparateFromRunChat(t *testing.T) {
	prompt, _, _, err := buildWorkflowPhaseSystemPrompt("workflow-builder", map[string]string{
		"WorkflowKind": "relay", "WorkspacePath": "Workflow/relay-test", "WorkshopMode": "run",
	}, promptContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "You are the Flow Builder") {
		t.Fatal("Relay prompt changed with shared workflow mode")
	}
	// Everyone except the Builder has a Run chat (PLAT-856-80); it is a
	// separate prompt, never the Builder's.
	run, err := relayproduct.ChatPrompt("run")
	if err != nil || strings.Contains(run, "You are the Flow Builder") {
		t.Fatalf("Relay Run chat missing or reuses the Builder prompt: %v", err)
	}
}
