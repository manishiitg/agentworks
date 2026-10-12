package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-697 phase 0: the Workflow Review is a pre-run check. An unchanged,
// clean plan starts with no review; a changed plan is reviewed once, then
// runs; a break the review leaves stops the run, and the same plan state is
// never reviewed again.
func TestWorkflowReviewRunsOncePerPlanRevisionBeforeARun(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := "Workflow/prerun-review"
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planning, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stepConfig := func(needsReview bool, reviewedAt string) string {
		flag := "false"
		if needsReview {
			flag = "true"
		}
		return `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":` + flag + `,"reviewed_at":"` + reviewedAt + `","reviewed_by":"workflow-review","contract_version":3,"checks":[]}}}]}`
	}
	write("plan.json", `{"steps":[{"id":"step-a","type":"scripted"}]}`)
	write("step_config.json", stepConfig(false, "2026-10-01T00:00:00Z"))

	reviews := 0
	fix := true
	original := workflowReviewSessionRunner
	t.Cleanup(func() { workflowReviewSessionRunner = original })
	workflowReviewSessionRunner = func(_ *SchedulerService, _ context.Context, _, _, reason string) workflowReviewOutcome {
		reviews++
		if !strings.Contains(reason, "step-a") {
			t.Errorf("review reason does not name the changed step: %q", reason)
		}
		if fix {
			write("step_config.json", stepConfig(false, "2026-10-07T12:00:00Z"))
		}
		return workflowReviewOutcome{completed: true, sessionID: "review-session"}
	}
	s := &SchedulerService{}
	ctx := context.Background()

	// Unchanged and clean: the run starts, no review.
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || gate.Reviewed || reviews != 0 {
		t.Fatalf("clean plan: gate=%+v reviews=%d, want proceed with no review", gate, reviews)
	}

	// Changed: reviewed once, then the run starts on the reviewed plan.
	write("step_config.json", stepConfig(true, "2026-10-01T00:00:00Z"))
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || !gate.Reviewed || reviews != 1 {
		t.Fatalf("changed plan: gate=%+v reviews=%d, want one review then proceed", gate, reviews)
	}
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); !gate.Proceed || reviews != 1 {
		t.Fatalf("after the review: gate=%+v reviews=%d, want proceed without another review", gate, reviews)
	}
	if latest := LatestWorkflowReview(ws); latest == nil || latest.Outcome != workflowReviewOutcomeClean {
		t.Fatalf("latest review = %+v, want clean", latest)
	}

	// A break the review cannot fix stops the run, once, without a loop.
	fix = false
	write("step_config.json", stepConfig(true, "2026-10-07T12:00:00Z"))
	gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron")
	if gate.Proceed || reviews != 2 || !strings.Contains(gate.BlockReason, "step-a") {
		t.Fatalf("unfixable break: gate=%+v reviews=%d, want stopped with the reason after one review", gate, reviews)
	}
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, nil, "cron"); gate.Proceed || reviews != 2 {
		t.Fatalf("same plan state: gate=%+v reviews=%d, want stopped again without a second review", gate, reviews)
	}
	// A run of another step is not held by step-a's break.
	if gate := s.ensureWorkflowReviewedBeforeRun(ctx, ws, []string{"step-b"}, "manual"); !gate.Proceed || reviews != 2 {
		t.Fatalf("unrelated step: gate=%+v reviews=%d, want proceed", gate, reviews)
	}
}

// Drive the registered run tools against saved plan/review files: a test step
// must launch, a full run must offer a choice without launching anything, and
// opting out must not clear the review debt or bypass execution validation.
func TestManualWorkflowDriftReviewChoice(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	ws := serveContractGuardManifest(t, WorkflowContractCurrentVersion)
	planning := filepath.Join(root, ws, "planning")
	if err := os.MkdirAll(planning, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, raw string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(planning, name), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.json", `{"steps":[{"id":"step-a","type":"scripted"}]}`)
	stale := `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":true,"contract_version":3,"checks":[]}}}]}`
	write("step_config.json", stale)
	api := &StreamingAPI{scheduler: &SchedulerService{}}
	for _, tc := range []struct {
		name   string
		req    QueryRequest
		active *ActiveSessionInfo
		want   bool
	}{
		{name: "person Builder", want: true},
		{name: "person Run", req: QueryRequest{PinRunMode: true, TriggeredBy: "manual"}, want: true},
		{name: "function", req: QueryRequest{ParentSessionID: "caller", SessionKind: "workflow_function"}},
		{name: "Relay call", req: QueryRequest{TriggeredBy: "relay"}},
		{name: "Slack", req: QueryRequest{BotPlatform: "slack"}},
		{name: "restored bot", active: &ActiveSessionInfo{BotPlatform: "slack"}},
		{name: "after run", req: QueryRequest{TriggeredBy: "after_run"}},
		{name: "Pulse", req: QueryRequest{PulseLifecycleTurn: true}},
		{name: "notification", req: QueryRequest{IsAutoNotification: true}},
		{name: "MCP Run", req: QueryRequest{PinRunMode: true, TriggeredBy: "external"}},
		{name: "MCP Builder", req: QueryRequest{ExternalBuilderOperationID: "op"}},
		{name: "restored external", active: &ActiveSessionInfo{TriggeredBy: "external"}},
		{name: "synthetic turn", active: &ActiveSessionInfo{IsSyntheticTurn: true}},
		{name: "cron", req: QueryRequest{TriggeredBy: "cron"}},
	} {
		policy := resolveWorkflowChatPolicy("chat", tc.req, tc.active, tc.req.PinRunMode)
		if got := personAttendedWorkflowReview("chat", "workflow-builder", tc.req, tc.active, policy); got != tc.want {
			t.Fatalf("%s review choice = %v, want %v", tc.name, got, tc.want)
		}
	}
	draft := &productSurfaceDraft{}
	// This is the same registrar order as installWorkflowPhaseTools.
	review := workflowReviewPreRunRegistrar{definitionRegistrar: draft, api: api, sessionID: "builder-test", workspacePath: ws, interactive: true}
	guard := workflowContractExecutionGuardRegistrar{definitionRegistrar: review, workspacePath: ws}
	started := 0
	baseSchema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{"group_name": map[string]interface{}{"type": "string"}}}
	for _, name := range []string{"execute_step", "run_full_workflow"} {
		if err := guard.RegisterCustomTool(name, "Run", baseSchema, func(context.Context, map[string]interface{}) (string, error) {
			started++
			return "execution started", nil
		}, "workflow"); err != nil {
			t.Fatal(err)
		}
	}
	if _, mutated := baseSchema["properties"].(map[string]interface{})["review_plan"]; mutated {
		t.Fatal("review choice changed the original schema")
	}
	call := func(name string, args map[string]interface{}) (string, error) {
		t.Helper()
		return draft.tools[name].exec(context.Background(), args)
	}
	if out, err := call("execute_step", map[string]interface{}{"step_id": "step-a"}); err != nil || out != "execution started" || started != 1 {
		t.Fatalf("step test waited for drift: %q %v starts=%d", out, err, started)
	}
	if out, err := call("run_full_workflow", map[string]interface{}{"group_name": "default"}); err != nil || !strings.Contains(out, "Review the workflow first, or run now?") || started != 1 || LatestWorkflowReview(ws) != nil {
		t.Fatalf("full run did not offer a side-effect-free choice: %q %v starts=%d", out, err, started)
	}
	if out, err := call("run_full_workflow", map[string]interface{}{"group_name": "default", "review_plan": false}); err != nil || out != "execution started" || started != 2 {
		t.Fatalf("run now did not launch: %q %v starts=%d", out, err, started)
	}
	if raw, err := os.ReadFile(filepath.Join(planning, "step_config.json")); err != nil || string(raw) != stale || LatestWorkflowReview(ws) != nil {
		t.Fatalf("run now changed review evidence: %s %v", raw, err)
	}
	if _, err := call("run_full_workflow", map[string]interface{}{"review_plan": "false"}); err == nil || started != 2 {
		t.Fatal("invalid review choice started execution")
	}
	// An explicit skip is not a migration bypass.
	blocked := &productSurfaceDraft{}
	oldGuard := workflowContractExecutionGuardRegistrar{definitionRegistrar: blocked, workspacePath: serveContractGuardManifest(t, workflowContractRouteSummariesVersion)}
	if err := oldGuard.RegisterCustomTool("run_full_workflow", "Run", baseSchema, draft.tools["run_full_workflow"].exec, "workflow"); err != nil {
		t.Fatal(err)
	}
	if _, err := blocked.tools["run_full_workflow"].exec(context.Background(), map[string]interface{}{"review_plan": false}); err == nil || !strings.Contains(err.Error(), "migration_required") || started != 2 {
		t.Fatalf("skip bypassed migration: %v starts=%d", err, started)
	}
	// An unattended full-run surface cannot use an unadvertised false argument
	// to bypass an existing unresolved review.
	items, err := collectWorkflowReviewItems(ws)
	if err != nil {
		t.Fatal(err)
	}
	updateWorkflowReviewState(ws, func(state *workflowReviewState) {
		state.ReviewedRevisions = []string{workflowReviewRevision(ws, items)}
	})
	unattended := &productSurfaceDraft{}
	autoReview := workflowReviewPreRunRegistrar{definitionRegistrar: unattended, api: api, sessionID: "function-call", workspacePath: ws}
	if err := autoReview.RegisterCustomTool("run_full_workflow", "Run", baseSchema, func(context.Context, map[string]interface{}) (string, error) {
		started++
		return "execution started", nil
	}, "workflow"); err != nil {
		t.Fatal(err)
	}
	if _, advertised := unattended.tools["run_full_workflow"].params["properties"].(map[string]interface{})["review_plan"]; advertised {
		t.Fatal("unattended surface advertised a person choice")
	}
	if _, err := unattended.tools["run_full_workflow"].exec(context.Background(), map[string]interface{}{"review_plan": false}); err == nil || !strings.Contains(err.Error(), "workflow_review_blocked") || started != 2 {
		t.Fatalf("unattended skip bypassed its review policy: %v starts=%d", err, started)
	}
	// A retained human-chat callback is also fenced when the chat's next turn
	// is synthetic; catalog creation alone must not authorize a later skip.
	api.activeSessions = map[string]*ActiveSessionInfo{"builder-test": {SessionID: "builder-test", IsSyntheticTurn: true}}
	if _, err := call("run_full_workflow", map[string]interface{}{"review_plan": false}); err == nil || !strings.Contains(err.Error(), "workflow_review_blocked") || started != 2 {
		t.Fatalf("retained callback honored a synthetic skip: %v starts=%d", err, started)
	}
	api.setSyntheticTurn("builder-test", false)
	updateWorkflowReviewState(ws, func(state *workflowReviewState) { state.ReviewedRevisions = nil })
	// Opting in launches the existing reviewer, never the workflow at this point.
	reviewStarted, reviewReturned := make(chan struct{}), make(chan struct{})
	original := workflowReviewSessionRunner
	workflowReviewSessionRunner = func(_ *SchedulerService, _ context.Context, _, _, _ string) workflowReviewOutcome {
		close(reviewStarted)
		defer close(reviewReturned)
		return workflowReviewOutcome{completed: true, sessionID: "explicit-review"}
	}
	t.Cleanup(func() {
		select {
		case <-reviewReturned:
		case <-time.After(5 * time.Second):
			t.Error("review did not finish")
		}
		deadline := time.Now().Add(5 * time.Second)
		for workflowReviewRunning(ws) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		workflowReviewSessionRunner = original
	})
	// Restore the current contract after the older-contract rejection check.
	serveContractGuardManifest(t, WorkflowContractCurrentVersion)
	if out, err := call("run_full_workflow", map[string]interface{}{"review_plan": true}); err != nil || !strings.Contains(out, "Reviewing the workflow") || started != 2 {
		t.Fatalf("review first did not start only the review: %q %v starts=%d", out, err, started)
	}
	select {
	case <-reviewStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit review never started")
	}
}
