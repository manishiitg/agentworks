package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestExternalRunScopeGate(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]externalTool{}
	for _, tool := range catalog {
		byName[tool.Name] = tool
	}
	claims := func(scopes ...string) *UserClaims {
		return &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: "t", Scopes: scopes, AllWorkflows: true}}
	}
	readOnly := claims("workflows:read", "files:read")
	executeOnly := claims("runs:execute")
	full := claims("workflows:read", "files:read", "runs:execute")

	// Proxied run tools require runs:execute.
	for _, name := range []string{"run_full_workflow", "stop_step", "stop_all_executions", "trigger_schedule", "run_reply_input"} {
		if _, found := byName[name]; !found {
			t.Fatalf("current run operation %s missing", name)
		}
		if externalTokenAllows(readOnly, byName[name]) {
			t.Fatalf("read-only token allows %s", name)
		}
		if !externalTokenAllows(executeOnly, byName[name]) {
			t.Fatalf("execute-only token denies %s", name)
		}
	}
	// Execution implies workflow visibility, never file content.
	for _, name := range []string{"list_workflows", "get_workflow", "get_script_outline", "list_runs", "get_run", "get_logs", "run_status", "list_executions", "list_schedules", "get_schedule_runs", "get_agent_context"} {
		if !externalTokenAllows(executeOnly, byName[name]) {
			t.Fatalf("execute-only token denies workflow read %s", name)
		}
	}
	for _, name := range []string{"read_file", "list_files", "search_files", "get_file_link", "list_workflow_knowledge", "read_workflow_knowledge"} {
		if externalTokenAllows(executeOnly, byName[name]) {
			t.Fatalf("execute-only token allows file read %s", name)
		}
		if !externalTokenAllows(full, byName[name]) {
			t.Fatalf("full token denies file read %s", name)
		}
	}
}

func TestExternalRunProxyRejectsReadOnlyToken(t *testing.T) {
	f := newExternalToolsFixture(t)
	// The scope gate bites before dispatch: a read-only PAT calling a
	// proxied run tool is rejected without touching the run runtime.
	w := httptest.NewRecorder()
	data := `{"name":"runs","arguments":{"action":"start","workflow_id":"invoices"}}`
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", data, patClaims("owner", []string{"workflows:read", "files:read"}), nil))
	scoped := externalTestBody(t, w, 403)
	if scoped["error"].(map[string]any)["code"] != "insufficient_scope" {
		t.Fatalf("wrong error: %v", scoped)
	}

	// PLAT-746-477: authorized execution must not start an assistant for a retired plan.
	body := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "start", "workflow_id": "invoices"}), 400)
	if body["error"].(map[string]any)["code"] != "workflow_unavailable" {
		t.Fatalf("wrong retirement refusal: %v", body)
	}
	// PLAT-746: inspection used to create an assistant turn through the run
	// proxy. An authorized owner must still get a refusal, not a start receipt.
	for _, action := range []string{"step_runtime", "step_prompts"} {
		inspection := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": action, "workflow_id": "invoices", "step_id": "fetch-invoices"}), 400)
		if inspection["error"].(map[string]any)["code"] != "invalid_arguments" || inspection["session_id"] != nil {
			t.Fatalf("inspection action %s reached run startup: %v", action, inspection)
		}
	}
	if len(f.api.activeSessions) != 0 {
		t.Fatal("a refused read or retired plan started an assistant")
	}
}

func TestExternalRunReplyInputRejectsUnknownSession(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "reply_input", "workflow_id": "invoices", "session_id": "no-such-session", "request_id": "r", "response": "yes"}), 404)
	if body["error"].(map[string]any)["code"] != "session_not_found" {
		t.Fatalf("wrong error: %v", body)
	}
}

func TestExternalRunStatusRejectsUnknownSession(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "status", "workflow_id": "invoices", "session_id": "no-such-session"}), 404)
	if body["error"].(map[string]any)["code"] != "session_not_found" {
		t.Fatalf("wrong error: %v", body)
	}
	externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "status", "workflow_id": "invoices", "session_id": "../escape"}), 400)
}

// PLAT-746-468: direct script runs use workflow mode, rather than a Builder
// phase. Status must admit them without crossing person, project or token.
func TestExternalRunStatusKeepsDirectRunOwnershipAndBinding(t *testing.T) {
	f := newExternalToolsFixture(t)
	owner := patClaims("owner", []string{"workflows:read"})
	owner.AccessToken.ID = "run-owner"
	sessionID := accessTokenSessionPrefix(owner) + externalRunSessionMarker + "direct"
	active := &ActiveSessionInfo{SessionID: sessionID, UserID: "owner", WorkspacePath: "Workflow/invoices", AgentMode: "workflow", Status: "running", TriggeredBy: "external"}
	f.api.activeSessions = map[string]*ActiveSessionInfo{sessionID: active}
	status := func(claims *UserClaims, expected int) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": "runs", "arguments": map[string]any{"action": "status", "workflow_id": "invoices", "session_id": sessionID}})
		w := httptest.NewRecorder()
		f.api.dispatchExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil), false)
		externalTestBody(t, w, expected)
	}
	status(owner, 200)
	otherToken := patClaims("owner", []string{"workflows:read"})
	otherToken.AccessToken.ID = "another-token"
	status(otherToken, 404)
	reader := patClaims("reader", []string{"workflows:read"})
	reader.AccessToken.ID = owner.AccessToken.ID // Isolate person ownership from prefix checks.
	status(reader, 404)
	active.WorkspacePath = "Workflow/another"
	status(owner, 404)
	active.WorkspacePath = "Workflow/invoices"
	active.AgentMode = "multi-agent"
	status(owner, 404)
	active.AgentMode, active.PhaseID = "workflow_phase", "workflow-builder"
	status(owner, 404)
	active.AgentMode, active.PhaseID = "workflow", ""
	delete(f.api.activeSessions, sessionID)
	// The same exact-workflow history reader handles a completed/reaped run.
	persisted := `{"session_id":"` + sessionID + `","user_id":"owner","agent_mode":"workflow","runtime":{"workspace_path":"Workflow/invoices"},"conversation_history":[]}`
	f.write(t, "Workflow/invoices/builder/conversation/2026-11-08/session-"+sessionID+"-conversation.json", persisted)
	status(owner, 200)
	status(reader, 404)
}

func TestExternalListSchedulesEmptyAndUnknownRuns(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.scheduler = NewSchedulerService(f.api) // the schedules tool reads through the scheduler
	body := externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "list", "project": "invoices"}), 200)
	schedules, _ := body["schedules"].([]any)
	if schedules == nil || len(schedules) != 0 {
		t.Fatalf("expected empty schedules, got %v", body)
	}
	body = externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "runs", "project": "invoices", "schedule_id": "nope"}), 404)
	if body["error"].(map[string]any)["code"] != "schedule_not_found" {
		t.Fatalf("wrong error: %v", body)
	}
}

func TestExternalScheduleRunsRemainReadableAfterScheduleDeletion(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.scheduler = NewSchedulerService(f.api) // the schedules tool reads through the scheduler
	runs, err := json.Marshal([]ScheduleRunEntry{{ID: "retained-run", ScheduleID: "deleted-schedule", Status: "success", StartedAt: time.Now().UTC()}})
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, "Workflow/invoices/schedule-runs.json", string(runs))
	body := externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "runs", "project": "invoices", "schedule_id": "deleted-schedule"}), 200)
	if body["schedule_deleted"] != true || body["total"] != float64(1) || body["runs"].([]any)[0].(map[string]any)["id"] != "retained-run" {
		t.Fatalf("deleted schedule history is unavailable: %v", body)
	}
	page := externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "runs", "project": "invoices", "schedule_id": "deleted-schedule", "offset": 1}), 200)
	if page["schedule_deleted"] != true || page["total"] != float64(1) || len(page["runs"].([]any)) != 0 {
		t.Fatalf("deleted schedule history does not paginate: %v", page)
	}
}

func TestExternalScheduleRunsPagesPastTwoHundred(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.scheduler = NewSchedulerService(f.api) // the schedules tool reads through the scheduler
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner"]},"schedules":[{"id":"deploy-hook","name":"Deploy hook","schedule_type":"webhook","enabled":true}]}`)
	runs := make([]ScheduleRunEntry, maxScheduleRuns+5)
	for index := range runs {
		runs[index] = ScheduleRunEntry{ID: fmt.Sprintf("run-%03d", index), ScheduleID: "deploy-hook", Status: "success", StartedAt: time.Now().UTC().Add(-time.Duration(index) * time.Minute)}
	}
	raw, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, "Workflow/invoices/schedule-runs.json", string(raw))
	body := externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "runs", "project": "invoices", "schedule_id": "deploy-hook", "offset": maxScheduleRuns, "limit": 10}), 200)
	if body["total"] != float64(maxScheduleRuns+5) || body["has_more"] != false || len(body["runs"].([]any)) != 5 {
		t.Fatalf("older run history page unavailable: %v", body)
	}
}

func TestExternalWebhookRunExposesAcceptedDeployMetadata(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.scheduler = NewSchedulerService(f.api) // the schedules tool reads through the scheduler
	f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoice processing","created_by":"owner","access":{"owners":["owner"]},"schedules":[{"id":"deploy-hook","name":"Rerun Basic Smoke Suite","schedule_type":"webhook","enabled":true}]}`)
	const sha = "0b8b40e69a1234567890abcdef1234567890abcd"
	sctx := &ScheduleContext{
		Schedule: WorkflowSchedule{Name: "Rerun Basic Smoke Suite"},
		WebhookInput: &WorkflowWebhookDelivery{
			DeliveryID: "delivery-1", ReceivedAt: time.Date(2026, 9, 23, 10, 33, 0, 0, time.UTC),
			Payload: json.RawMessage(`{"component":"chat","env":"staging","commit_sha":"` + sha + `","deployed_at":"2026-09-23T10:32:19Z","secret":"never-return"}`),
		},
	}
	runs, err := json.Marshal([]ScheduleRunEntry{{
		ID: "run-1", ScheduleID: "deploy-hook", TriggerSource: "webhook", RunFolder: "run-6-hook",
		Status: "success", StartedAt: time.Now().UTC(), Webhook: webhookRunMetadata(sctx),
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.write(t, "Workflow/invoices/schedule-runs.json", string(runs))
	f.write(t, "Workflow/invoices/runs/run-6-hook/default/result.txt", "passed")
	for _, call := range []struct {
		name string
		args map[string]any
	}{
		{"automations", map[string]any{"scope": "schedule", "action": "runs", "project": "invoices", "schedule_id": "deploy-hook"}},
		{"runs", map[string]any{"action": "get", "workflow_id": "invoices", "run_folder": "run-6-hook/default"}},
	} {
		body := externalTestBody(t, f.call(t, "owner", call.name, call.args), 200)
		var webhook map[string]any
		if call.name == "automations" {
			listed := body["runs"].([]any)
			webhook = listed[0].(map[string]any)["webhook"].(map[string]any)
		} else {
			var ok bool
			webhook, ok = body["webhook"].(map[string]any)
			if !ok {
				t.Fatalf("%s missing webhook metadata: %v", call.name, body)
			}
			if body["schedule_run_id"] != "run-1" {
				t.Fatalf("%s missing run identity: %v", call.name, body)
			}
		}
		if webhook["commit_sha"] != sha || webhook["component"] != "chat" || webhook["env"] != "staging" || webhook["deployed_at"] != "2026-09-23T10:32:19Z" {
			t.Fatalf("%s missing deploy metadata: %v", call.name, webhook)
		}
		if strings.Contains(bodyString(t, body), "never-return") {
			t.Fatalf("%s exposed unrelated webhook payload", call.name)
		}
	}
}

func bodyString(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExternalTriggerScheduleWithoutScheduler(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "automations", map[string]any{"scope": "schedule", "action": "run_now", "project": "invoices", "schedule_id": "daily"}), 503)
	if body["error"].(map[string]any)["code"] != "scheduler_unavailable" {
		t.Fatalf("wrong error: %v", body)
	}
}

func TestExternalListExecutionsEmpty(t *testing.T) {
	f := newExternalToolsFixture(t)
	body := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "active", "workflow_id": "invoices"}), 200)
	executions, _ := body["executions"].([]any)
	if executions == nil || len(executions) != 0 {
		t.Fatalf("expected empty executions, got %v", body)
	}
}

func TestExternalListExecutionsIncludesRunningSteps(t *testing.T) {
	f := newExternalToolsFixture(t)
	f.api.trackedWorkflowExecutions = map[string]*TrackedWorkflowExecution{
		"step-1":         {ExecutionID: "step-1", SessionID: "session-1", WorkspacePath: "Workflow/invoices", Source: trackedExecutionSourceWorkshopBackground, Kind: "step", Status: trackedExecutionStatusRunning},
		"other-workflow": {ExecutionID: "other-workflow", SessionID: "session-2", WorkspacePath: "Workflow/secret", Source: trackedExecutionSourceWorkshopBackground, Kind: "step", Status: trackedExecutionStatusRunning},
	}
	body := externalTestBody(t, f.call(t, "owner", "runs", map[string]any{"action": "active", "workflow_id": "invoices"}), 200)
	executions, _ := body["executions"].([]any)
	if len(executions) != 1 || executions[0].(map[string]any)["query_id"] != "step-1" {
		t.Fatalf("running workflow steps = %v", executions)
	}
}

func TestReadOnlyForRequest(t *testing.T) {
	if !readOnlyForRequest(WorkflowAccessRead, QueryRequest{}) {
		t.Fatal("reader without pin is not read-only")
	}
	if readOnlyForRequest(WorkflowAccessOwner, QueryRequest{}) {
		t.Fatal("owner without pin is read-only")
	}
	if !readOnlyForRequest(WorkflowAccessOwner, QueryRequest{PinRunMode: true}) {
		t.Fatal("pinned owner turn is not read-only")
	}
	if !readOnlyForRequest(WorkflowAccessWrite, QueryRequest{PinRunMode: true}) {
		t.Fatal("pinned writer turn is not read-only")
	}
}

// A Builder message sent without a session_id once continued the caller's
// latest MCP run-tool session instead of the Builder chat (19 Oct).
func TestExternalRunSessionIsNeverTheBuilderChat(t *testing.T) {
	claims := &UserClaims{AccessToken: &accesstokens.Token{ID: "3315ebc6490dbbea"}}
	run := accessTokenSessionPrefix(claims) + externalRunSessionMarker + "3fb54cc4-abf7-4607-86a3-f4248abd1b46"
	builder := accessTokenSessionPrefix(claims) + "455e81f4-4d14-4f2c-b8e3-ad288067ab94"
	if !separateWorkflowConversation(run) || !separateWorkflowConversation(externalRunSessionMarker+"x") {
		t.Fatal("an MCP run session was eligible as the Builder chat")
	}
	if separateWorkflowConversation(builder) {
		t.Fatal("an MCP Builder chat was treated as a run session")
	}
}
