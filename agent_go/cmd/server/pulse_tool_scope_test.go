package server

import (
	"context"
	"strings"
	"testing"

	internalevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/executor"
)

// A background agent's tool session for workflow A, started from a chat
// session owned by alice.
func scopeFixture(t *testing.T) (*StreamingAPI, context.Context) {
	t.Helper()
	stub, _ := newScheduleRunWorkspaceStub(t)
	stub.mu.Lock()
	stub.files["Workflow/a/workflow.json"] = `{"id":"wf-a","label":"A","version":"1.0.0"}`
	stub.files["Workflow/b/workflow.json"] = `{"id":"wf-b","label":"B","version":"1.0.0","created_by":"bob"}`
	stub.mu.Unlock()
	store := internalevents.NewEventStore(10)
	store.SetSessionOwner("chat-a", "alice")
	api := &StreamingAPI{eventStore: store}
	t.Cleanup(step.RegisterWorkshopToolSession("tool-a", "Workflow/a", "chat-a"))
	return api, executor.WithSessionID(context.Background(), "tool-a")
}

// The reported attack: a Pulse run of workflow A names workflow B to act as
// B's owner. It must be refused, not resolved to B or B's owner.
func TestPulseToolScopeRefusesAnotherWorkflow(t *testing.T) {
	api, ctx := scopeFixture(t)
	for _, target := range []string{"Workflow/b", "Workflow/a/../b", "/Workflow/b/"} {
		if _, _, err := api.pulseToolScope(ctx, target, false); err == nil || !strings.Contains(err.Error(), "must be this workflow") {
			t.Errorf("workspace_path %q must be refused, got %v", target, err)
		}
	}
	ws, claims, err := api.pulseToolScope(ctx, "", false)
	if err != nil || ws != "Workflow/a" || claims.UserID != "alice" {
		t.Fatalf("the session's own workflow and owner, got %q %+v %v", ws, claims, err)
	}
	if ws, _, err := api.pulseToolScope(ctx, "Workflow/a", false); err != nil || ws != "Workflow/a" {
		t.Fatalf("naming the session's own workflow is fine, got %q %v", ws, err)
	}
}

// No trusted owner means no identity: no fallback to a workflow owner or the
// default user.
func TestPulseToolScopeHasNoOwnerFallback(t *testing.T) {
	api, _ := scopeFixture(t)
	t.Cleanup(step.RegisterWorkshopToolSession("tool-orphan", "Workflow/b", "chat-with-no-owner"))
	ctx := executor.WithSessionID(context.Background(), "tool-orphan")
	if _, _, err := api.pulseToolScope(ctx, "", false); err == nil || !strings.Contains(err.Error(), "no authenticated owner") {
		t.Fatalf("an ownerless session must be refused, got %v", err)
	}
	if _, _, err := api.pulseToolScope(context.Background(), "Workflow/b", false); err == nil {
		t.Fatal("a call with no session must be refused")
	}
	if _, _, err := api.pulseToolScope(executor.WithSessionID(context.Background(), "unknown-session"), "", false); err == nil {
		t.Fatal("a session that is not a workflow session must be refused")
	}
}

// Claims on the context cannot switch the principal away from the session's
// owner.
func TestPulseToolScopeRejectsAMismatchedCaller(t *testing.T) {
	api, ctx := scopeFixture(t)
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "mallory"})
	if _, _, err := api.pulseToolScope(ctx, "", false); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a mismatched caller must be refused, got %v", err)
	}
}

// search_platform and read_crew_calls go through the same scope, so a
// spoofed workspace_path fails before any call is made.
func TestPulsePlatformToolsRefuseASpoofedWorkflow(t *testing.T) {
	api, ctx := scopeFixture(t)
	old := pulsePlatformAPI
	pulsePlatformAPI = api
	t.Cleanup(func() { pulsePlatformAPI = old })

	_, executors, _ := createPulsePlatformTools()
	for name, args := range map[string]map[string]interface{}{
		"search_platform":   {"operation": "list_workflows", "workspace_path": "Workflow/b"},
		"ask_platform_crew": {"operation": "ask_crew", "workspace_path": "Workflow/b", "arguments": map[string]interface{}{"crew_id": "c", "message": "hi"}},
	} {
		run := executors[name].(func(context.Context, map[string]interface{}) (string, error))
		if _, err := run(ctx, args); err == nil || !strings.Contains(err.Error(), "must be this workflow") {
			t.Errorf("%s must refuse another workflow, got %v", name, err)
		}
	}
	api.productSchedules = &ProductScheduleService{}
	if _, err := runReadCrewCalls(ctx, map[string]interface{}{"operation": "list", "workspace_path": "Workflow/b"}); err == nil || !strings.Contains(err.Error(), "must be this workflow") {
		t.Errorf("read_crew_calls must refuse another workflow, got %v", err)
	}
}

// A scheduled run's own turns (Gate, Plan Drift, finalizer) cannot ask a
// Crew; a Goal Work agent's tool session can, even when its ID is derived
// from the scheduled session.
func TestAskPlatformCrewIsRefusedInAScheduledRunsOwnTurns(t *testing.T) {
	root := executor.WithSessionID(context.Background(), "schedule-cron--wf-a")
	if err := refuseUnattendedCrewWork(root); err == nil || !strings.Contains(err.Error(), "scheduled run") {
		t.Fatalf("a scheduled run's own turn must be refused, got %v", err)
	}
	t.Cleanup(step.RegisterWorkshopToolSession("schedule-cron--wf-a-goal-work-1", "Workflow/a", "schedule-cron--wf-a"))
	if err := refuseUnattendedCrewWork(executor.WithSessionID(context.Background(), "schedule-cron--wf-a-goal-work-1")); err != nil {
		t.Fatalf("a Goal Work agent keeps the tool, got %v", err)
	}
	if err := refuseUnattendedCrewWork(executor.WithSessionID(context.Background(), "chat-a")); err != nil {
		t.Fatalf("a Builder chat keeps the tool, got %v", err)
	}
	_, executors, _ := createPulsePlatformTools()
	run := executors["ask_platform_crew"].(func(context.Context, map[string]interface{}) (string, error))
	old := pulsePlatformAPI
	pulsePlatformAPI = &StreamingAPI{}
	t.Cleanup(func() { pulsePlatformAPI = old })
	if _, err := run(root, map[string]interface{}{"operation": "ask_crew"}); err == nil || !strings.Contains(err.Error(), "scheduled run") {
		t.Fatalf("the tool itself must refuse, got %v", err)
	}
}

// A recorded call with no Crew owner is not resolved as the default user.
func TestReadVerifiedCrewCallRefusesAnOwnerlessCall(t *testing.T) {
	_, err := readVerifiedCrewCall(context.Background(), &StreamingAPI{}, "wf-a", "alice", pulseCrewCall{CrewProfileID: "crew", TriggerID: "t"}, 10)
	if err == nil || !strings.Contains(err.Error(), "no recorded Crew owner") {
		t.Fatalf("an ownerless call must be refused, got %v", err)
	}
}
