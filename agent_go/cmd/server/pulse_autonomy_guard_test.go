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
	"github.com/manishiitg/mcpagent/mcpclient"
)

// PLAT-697 phase 2: Goal Work's pulse.autonomy levels are held by the tool
// dispatch of its Pulse session (the same binding every direct tool call of
// the session passes), not only by its prompt.
func TestPulsePermissionsIsEnforcedAtToolDispatch(t *testing.T) {
	t.Setenv("MULTI_USER_MODE", "false")
	const session = "schedule-goallead--permissions-g1"
	const bridge = "workflow-session-pulse-permissions"
	mcpclient.GetSessionRegistry().RegisterHTTPSession(session, bridge)
	t.Cleanup(func() { mcpclient.GetSessionRegistry().CloseHTTPSession(session) })
	api := &StreamingAPI{eventStore: events.NewEventStore(10)}
	api.eventStore.SetSessionOwner(session, "alice")
	requestCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "alice", Provider: "local"})
	resolve := api.bindToolExecutionContext(requestCtx, session, QueryRequest{}, false)

	inTurn := func(manifest string, tool string) (context.Context, error) {
		release := beginGoalWorkTurn(session, stepworkflow.PulsePermissions(manifest))
		defer release()
		return resolve(executor.WithSessionID(context.Background(), bridge), tool)
	}

	// run=ask: execute_step is refused and points at a decision request.
	_, err := inTurn(`{"pulse":{"enabled":false}}`, "execute_step")
	if err == nil || !strings.Contains(err.Error(), "Autopilot is off") {
		t.Fatalf("execute_step with run=ask must be refused with a decision-request hint, got %v", err)
	}
	// Delegating an execution is also a run, including a typed function.
	for _, tool := range []string{"call_function", "run_project_workflow"} {
		if _, err := inTurn(`{"pulse":{"enabled":false}}`, tool); err == nil {
			t.Fatalf("%s bypassed Autopilot run authority", tool)
		}
	}
	// run=auto (also the default): allowed.
	if _, err := inTurn(`{"pulse":{"enabled":true}}`, "execute_step"); err != nil {
		t.Fatalf("execute_step with run=auto must run: %v", err)
	}
	// change=ask (the default): a typed plan edit is refused; change=auto allows it.
	if _, err := inTurn(`{}`, "update_agent_step"); err == nil {
		t.Fatal("a plan edit with change=ask must be refused")
	}
	if _, err := inTurn(`{"pulse":{"enabled":true}}`, "update_agent_step"); err != nil {
		t.Fatalf("a plan edit with change=auto must run: %v", err)
	}
	// outward=ask marks the call so argument-dependent senders hold their writes.
	ctx, err := inTurn(`{"pulse":{"enabled":true,"outward_actions":"ask"}}`, "slack")
	if err != nil || !common.OutwardHeld(ctx) {
		t.Fatalf("outward=ask must reach the tool as held, err=%v", err)
	}
	for _, tool := range []string{"send_message", "ask_builder", "schedule_message_wakeup", "create_human_input_request", "record_goal_observations", "send_email", "delete_schedule", "execute_shell_command", "brain_access", "new_future_write_tool"} {
		if _, err := inTurn(`{"pulse":{"enabled":false}}`, tool); err == nil || !strings.Contains(err.Error(), "Autopilot is off") {
			t.Fatalf("OFF tool %s escaped: %v", tool, err)
		}
	}
	if _, err := inTurn(`{"pulse":{"enabled":false}}`, "record_goal_observations"); err == nil {
		t.Fatal("OFF retained report activity")
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
func TestPulsePermissionsHoldsOnTheSessionHTTPToolRoute(t *testing.T) {
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

	release := beginGoalWorkTurn(session, stepworkflow.PulsePermissions(`{"pulse":{"enabled":false}}`))
	if _, err := updateStep(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatal("change=ask: a direct update_step must be refused")
	}
	if out := viaHTTP(); out.Success || !strings.Contains(out.Error, "Autopilot is off") {
		t.Fatalf("change=ask: update_step over the HTTP tool route must be refused, got %+v", out)
	}
	release()
	if edits != 0 {
		t.Fatalf("no edit may land with change=ask, got %d", edits)
	}

	release = beginGoalWorkTurn(session, stepworkflow.PulsePermissions(`{"pulse":{"enabled":true,"outward_actions":"allow"}}`))
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

// Owner, 2026-10-23: autonomy "decide" lets Autopilot direct plan replacement and
// deleting steps or schedules; secrets, access and migrations stay with the owner.
func TestGoalWorkDecideUnlocksPlanToolsButNotSecrets(t *testing.T) {
	allow := stepworkflow.PulsePermissions(`{"pulse":{"enabled":true,"outward_actions":"allow"}}`)
	decide := stepworkflow.PulsePermissions(`{"pulse":{"enabled":true,"outward_actions":"decide"}}`)
	if !decide.Outward || !decide.Decide || allow.Decide {
		t.Fatalf("decide must include outward and plan decisions, allow must not: allow=%+v decide=%+v", allow, decide)
	}
	for _, tool := range []string{"delete_plan_steps", "delete_schedule", "create_plan"} {
		if goalWorkToolRefusal(tool, allow) == nil {
			t.Fatalf("%s must need the owner at allow", tool)
		}
		if err := goalWorkToolRefusal(tool, decide); err != nil {
			t.Fatalf("%s must run at decide: %v", tool, err)
		}
	}
	for _, tool := range []string{"set_workflow_secret", "delete_workflow_secret", "manage_user_access", "migrate_agent_steps",
		"manage_vault_access", "manage_vault_secret_access", "rotate_secret", "manage_laptop_access", "share_dashboard", "publish_relay", "mutate_vault_db"} {
		if goalWorkToolRefusal(tool, decide) == nil {
			t.Fatalf("%s must stay with the owner at decide", tool)
		}
	}
	for _, tool := range []string{"list_secrets", "get_secret_sources", "get_dashboard_share", "send_slack_message"} {
		if goalWorkNeverByName(tool) {
			t.Fatalf("%s is a read tool and must not be held by name", tool)
		}
	}
	if !common.GoalWorkHeld(common.WithGoalWorkHeld(context.Background())) || common.GoalWorkHeld(context.Background()) {
		t.Fatal("GoalWorkHeld must be set only by WithGoalWorkHeld")
	}
}

func TestAutopilotSettingsAreRestoredAfterATurnChangesThem(t *testing.T) {
	f := newExternalToolsFixture(t)
	raw, _ := json.Marshal(WorkflowManifest{ID: "invoices", Label: "Invoices", CreatedBy: "owner", Access: &WorkflowAccess{Owners: []string{"owner"}}, Pulse: &WorkflowPulseConfig{Enabled: true, OutwardActions: "ask"}})
	f.write(t, "Workflow/invoices/workflow.json", string(raw))
	ctx := context.Background()

	restore := restoreAutopilotSettings(ctx, "Workflow/invoices")
	// A Goal Work turn writes workflow.json by any path (here a shell-style raw write).
	changed, _, err := ReadWorkflowManifest(ctx, "Workflow/invoices")
	if err != nil {
		t.Fatal(err)
	}
	changed.Pulse.OutwardActions = "decide"
	if err := writeWorkflowManifest(ctx, "Workflow/invoices", changed); err != nil {
		t.Fatal(err)
	}
	restore()

	after, _, err := ReadWorkflowManifest(ctx, "Workflow/invoices")
	if err != nil || after.Pulse == nil || after.Pulse.OutwardActions != "ask" || !after.Pulse.Enabled {
		t.Fatalf("autonomy must be back to ask and Autopilot on: %+v, %v", after.Pulse, err)
	}
}
