package server

import (
	"context"
	"strings"
	"testing"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/executor"
)

// A Builder chat on workflow A owned by alice, plus a background agent's tool
// session started from it.
func humanAnswerFixture(t *testing.T) (builderChat context.Context) {
	t.Helper()
	stub, _ := newScheduleRunWorkspaceStub(t)
	stub.mu.Lock()
	stub.files["Workflow/a/workflow.json"] = `{"id":"wf-a","label":"A","version":"1.0.0"}`
	stub.files["Workflow/b/workflow.json"] = `{"id":"wf-b","label":"B","version":"1.0.0","created_by":"bob"}`
	stub.mu.Unlock()
	store := internalevents.NewEventStore(10)
	store.SetSessionOwner("chat-a", "alice")
	store.SetSessionOwner("sched_a_1", "alice")
	api := &StreamingAPI{eventStore: store}
	api.workshopChatSessions.Store("chat-a", &scopeWorkshop{&step.WorkshopConfig{WorkspacePath: "Workflow/a"}})
	api.workshopChatSessions.Store("sched_a_1", &scopeWorkshop{&step.WorkshopConfig{WorkspacePath: "Workflow/a"}})
	old := pulsePlatformAPI
	pulsePlatformAPI = api
	t.Cleanup(func() { pulsePlatformAPI = old })
	t.Cleanup(step.RegisterWorkshopToolSession("tool-a", "Workflow/a", "chat-a"))
	return executor.WithSessionID(context.Background(), "chat-a")
}

func TestHumanAnswerScopeIsThePersonInTheirOwnBuilderChat(t *testing.T) {
	ctx := humanAnswerFixture(t)
	ws, claims, err := humanAnswerScope(ctx, "")
	if err != nil || ws != "Workflow/a" || claims.UserID != "alice" {
		t.Fatalf("the chat's own workflow and owner, got %q %+v %v", ws, claims, err)
	}
	for _, target := range []string{"Workflow/b", "Workflow/a/../b"} {
		if _, _, err := humanAnswerScope(ctx, target); err == nil || !strings.Contains(err.Error(), "must be this workflow") {
			t.Errorf("workspace_path %q must be refused, got %v", target, err)
		}
	}
	spoofed := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "mallory"})
	if _, _, err := humanAnswerScope(spoofed, ""); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("claims that are not the chat's owner must be refused, got %v", err)
	}
}

// The reported attack: a Pulse reviewer (a background agent) or a scheduled
// run answers the decision it proposed and applies it.
func TestHumanAnswerScopeRefusesAgentsAndUnattendedRuns(t *testing.T) {
	humanAnswerFixture(t)
	for name, ctx := range map[string]context.Context{
		"background agent": executor.WithSessionID(context.Background(), "tool-a"),
		"scheduled run":    executor.WithSessionID(context.Background(), "sched_a_1"),
		"no session":       context.Background(),
		"unknown session":  executor.WithSessionID(context.Background(), "chat-unknown"),
	} {
		if _, _, err := humanAnswerScope(ctx, "Workflow/a"); err == nil || !strings.Contains(err.Error(), "only a person") {
			t.Errorf("%s must be refused, got %v", name, err)
		}
	}
}

// The tool itself refuses before touching the decision.
func TestAnswerHumanInputRequestToolRefusesABackgroundAgent(t *testing.T) {
	humanAnswerFixture(t)
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	if _, err := createReportHumanInput(context.Background(), "Workflow/a", ReportHumanInputCreateRequest{
		InputID: "d1", Source: "pulse", Question: "Apply?",
		Options: []ReportHumanInputOption{{ID: "approve", Title: "Approve"}},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, executors, _ := createReportHumanInputTools()
	run := executors["answer_human_input_request"].(func(context.Context, map[string]interface{}) (string, error))
	ctx := executor.WithSessionID(context.Background(), "tool-a")
	if _, err := run(ctx, map[string]interface{}{"workspace_path": "Workflow/a", "input_id": "d1", "selected_option_id": "approve"}); err == nil {
		t.Fatal("a background agent answered a decision")
	}
	pending, err := listReportHumanInputs(context.Background(), "Workflow/a", "pending", "")
	if err != nil || len(pending) != 1 {
		t.Fatalf("the refused call changed the decision: %+v %v", pending, err)
	}
}
