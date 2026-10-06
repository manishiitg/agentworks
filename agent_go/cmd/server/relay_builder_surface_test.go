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
	for _, required := range []string{"You are the Relay Builder", "Workflow/relay-test", "Current authenticated user"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("Relay prompt missing %q", required)
		}
	}
	for _, absent := range []string{"## CURRENT MODE:", "## CURRENT STATE", "Pulse step"} {
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
	if len(draft.skills) != 1 || draft.skills[0].Name != "relay-builder" {
		t.Fatalf("Relay skills: %v", draft.skills)
	}
	for _, name := range []string{"test_relay", "get_relay_run", "manage_workflow_webhook", "get_relay_releases", "publish_relay"} {
		if _, registered := draft.tools[name]; !registered {
			t.Errorf("shared workflow registration did not provide Relay tool %s", name)
		}
	}
	tools, err := relayproduct.BuilderTools()
	if err != nil {
		t.Fatal(err)
	}
	gate := newProductToolGateForAllowlist("relays", tools)
	if !gate.Admit("test_relay") || !gate.Admit("publish_relay") || gate.Admit("create_slack_bot_route") {
		t.Fatal("Relay tool admission differs from product.yaml")
	}
}

func TestRelayHasOnlyBuilderChat(t *testing.T) {
	prompt, _, _, err := buildWorkflowPhaseSystemPrompt("workflow-builder", map[string]string{
		"WorkflowKind": "relay", "WorkspacePath": "Workflow/relay-test", "WorkshopMode": "run",
	}, promptContext{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "You are the Relay Builder") {
		t.Fatal("Relay prompt changed with shared workflow mode")
	}
	if _, err := relayproduct.ChatPrompt("run"); err == nil {
		t.Fatal("Relay unexpectedly declares a Run chat")
	}
}
