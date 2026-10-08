package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	events "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/mcpagent/agent/codeexec"
	"github.com/manishiitg/mcpagent/executor"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
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
	if err == nil || !strings.Contains(err.Error(), "Measure autonomy level") {
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

// Owner's Substack test, 2026-10-08: Pulse's shell blocked a direct edit, so
// it scripted update_step against the session's HTTP tool route
// (/s/<session>/tools/custom/update_step with $MCP_API_TOKEN). That route runs
// the session's registered executor, which carries the same binding
// (agentwrapper.RegisterCustomToolWithTimeout wraps every direct tool with
// ToolExecutionContext), so the levels hold there too: change=ask refuses
// update_step directly and over HTTP; change=auto allows both.
func TestGoalWorkAutonomyHoldsOnTheSessionHTTPToolRoute(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	const session = "schedule-goallead--httproute-g1"
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(session, "alice")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "local"})
	resolve := api.bindToolExecutionContext(requestCtx, session, QueryRequest{}, false)
	edits := 0
	// The binding agentwrapper applies to every registered direct tool.
	updateStep := func(ctx context.Context, args map[string]interface{}) (string, error) {
		if _, err := resolve(ctx, "update_step"); err != nil {
			return "", err
		}
		edits++
		return "updated", nil
	}
	codeexec.InitRegistryForSession(session, map[string]func(context.Context, map[string]interface{}) (string, error){"update_step": updateStep}, nil)
	t.Cleanup(func() { codeexec.CleanupSession(session) })
	handlers := executor.NewExecutorHandlers("", loggerv2.NewNoop())
	viaHTTP := func() executor.CustomExecuteResponse {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/s/"+session+"/tools/custom/update_step", strings.NewReader(`{"step_id":"step-growth-summary","changes":{"reason":"x"}}`))
		r.Header.Set("Content-Type", "application/json")
		// What the /s/{session_id}/tools/custom/{tool} route injects.
		r.Header.Set("X-Session-ID", session)
		r = r.WithContext(context.WithValue(r.Context(), common.ChatSessionIDKey, session))
		w := httptest.NewRecorder()
		handlers.HandlePerToolCustomRequest(w, r, "update_step")
		var out executor.CustomExecuteResponse
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
		return out
	}

	release := beginGoalWorkTurn(session, stepworkflow.PulseAutonomyPermissions(`{"pulse":{"autonomy":{"run":"auto","outward":"auto","change":"ask"}}}`))
	if _, err := updateStep(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("change=ask: a direct update_step must be refused")
	}
	if out := viaHTTP(); out.Success || !strings.Contains(out.Error, "Fix autonomy level") {
		t.Fatalf("change=ask: update_step over the HTTP tool route must be refused, got %+v", out)
	}
	release()
	if edits != 0 {
		t.Fatalf("no edit may land with change=ask, got %d", edits)
	}

	release = beginGoalWorkTurn(session, stepworkflow.PulseAutonomyPermissions(`{"pulse":{"autonomy":{"run":"auto","outward":"auto","change":"auto"}}}`))
	defer release()
	if _, err := updateStep(context.Background(), map[string]interface{}{}); err != nil {
		t.Fatalf("change=auto: a direct update_step must run: %v", err)
	}
	if out := viaHTTP(); !out.Success {
		t.Fatalf("change=auto: update_step over the HTTP tool route must run, got %+v", out)
	}
	if edits != 2 {
		t.Fatalf("edits = %d, want 2", edits)
	}
}
