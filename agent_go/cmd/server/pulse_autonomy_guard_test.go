package server

import (
	"context"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// PLAT-697 phase 2: Goal Work's pulse.autonomy levels are held by the tool
// dispatch of its Pulse session (the same binding every direct tool call of
// the session passes), not only by its prompt.
func TestGoalWorkAutonomyIsEnforcedAtToolDispatch(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	const session = "schedule-cron--daily_goal"
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(session, "alice")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "local"})
	resolve := api.bindToolExecutionContext(requestCtx, session, QueryRequest{}, false)

	inTurn := func(manifest string, tool string) (context.Context, error) {
		release := beginGoalWorkTurn(session, stepworkflow.PulseAutonomyPermissions(manifest))
		defer release()
		return resolve(context.Background(), tool)
	}

	// run=ask: execute_step is refused and points at a decision request.
	_, err := inTurn(`{"pulse":{"autonomy":{"run":"ask"}}}`, "execute_step")
	if err == nil || !strings.Contains(err.Error(), "create_human_input_request") {
		t.Fatalf("execute_step with run=ask must be refused with a decision-request hint, got %v", err)
	}
	// run=auto (also the default): allowed.
	if _, err := inTurn(`{"pulse":{"autonomy":{"run":"auto"}}}`, "execute_step"); err != nil {
		t.Fatalf("execute_step with run=auto must run: %v", err)
	}
	// change=ask (the default): a typed plan edit is refused; change=auto allows it.
	if _, err := inTurn(`{}`, "update_message_sequence_step"); err == nil {
		t.Fatal("a plan edit with change=ask must be refused")
	}
	if _, err := inTurn(`{"pulse":{"autonomy":{"change":"auto"}}}`, "update_message_sequence_step"); err != nil {
		t.Fatalf("a plan edit with change=auto must run: %v", err)
	}
	// outward=ask marks the call so argument-dependent senders hold their writes.
	ctx, err := inTurn(`{}`, "slack")
	if err != nil || !common.OutwardHeld(ctx) {
		t.Fatalf("outward=ask must reach the tool as held, err=%v", err)
	}
	// Outside a Goal Work turn the same session's tools are not held.
	if _, err := resolve(context.Background(), "execute_step"); err != nil {
		t.Fatalf("the hold must end with the turn: %v", err)
	}
}
