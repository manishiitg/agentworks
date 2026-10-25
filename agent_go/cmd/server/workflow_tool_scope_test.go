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

// The reported attack: a workflow decision tool of workflow A names workflow B to act as
// B's owner. It must be refused, not resolved to B or B's owner.
func TestWorkflowToolScopeRefusesAnotherWorkflow(t *testing.T) {
	api, ctx := scopeFixture(t)
	for _, target := range []string{"Workflow/b", "Workflow/a/../b", "/Workflow/b/"} {
		if _, _, err := api.workflowToolScope(ctx, target, false); err == nil || !strings.Contains(err.Error(), "must be this workflow") {
			t.Errorf("workspace_path %q must be refused, got %v", target, err)
		}
	}
	ws, claims, err := api.workflowToolScope(ctx, "", false)
	if err != nil || ws != "Workflow/a" || claims.UserID != "alice" {
		t.Fatalf("the session's own workflow and owner, got %q %+v %v", ws, claims, err)
	}
	if ws, _, err := api.workflowToolScope(ctx, "Workflow/a", false); err != nil || ws != "Workflow/a" {
		t.Fatalf("naming the session's own workflow is fine, got %q %v", ws, err)
	}
}

// No trusted owner means no identity: no fallback to a workflow owner or the
// default user.
func TestWorkflowToolScopeHasNoOwnerFallback(t *testing.T) {
	api, _ := scopeFixture(t)
	t.Cleanup(step.RegisterWorkshopToolSession("tool-orphan", "Workflow/b", "chat-with-no-owner"))
	ctx := executor.WithSessionID(context.Background(), "tool-orphan")
	if _, _, err := api.workflowToolScope(ctx, "", false); err == nil || !strings.Contains(err.Error(), "no authenticated owner") {
		t.Fatalf("an ownerless session must be refused, got %v", err)
	}
	if _, _, err := api.workflowToolScope(context.Background(), "Workflow/b", false); err == nil {
		t.Fatal("a call with no session must be refused")
	}
	if _, _, err := api.workflowToolScope(executor.WithSessionID(context.Background(), "unknown-session"), "", false); err == nil {
		t.Fatal("a session that is not a workflow session must be refused")
	}
}

// Claims on the context cannot switch the principal away from the session's
// owner.
func TestWorkflowToolScopeRejectsAMismatchedCaller(t *testing.T) {
	api, ctx := scopeFixture(t)
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "mallory"})
	if _, _, err := api.workflowToolScope(ctx, "", false); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a mismatched caller must be refused, got %v", err)
	}
}

// search_platform and read_crew_calls go through the same scope, so a
// spoofed workspace_path fails before any call is made.
