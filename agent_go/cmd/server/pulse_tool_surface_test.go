package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	mcpexecutor "github.com/manishiitg/mcpagent/executor"
)

// pulseConsolidatedToolNames is the established Pulse state/fixer surface.
//
// The lifecycle surface was consolidated, and the naming rule remains
// derivable and exhaustive: a Pulse
// tool is `get_pulse_*` when it reads and `record_pulse_*` when it writes, and
// there is no third verb. The eight-tool surface had `get_`, `start_`, `mark_`,
// and `record_` for four concepts, and the agent guessed across it — inventing
// close_pulse_fix_attempt, complete_pulse_fix_attempt, consume_human_input,
// resolve_human_input, and update_human_input, none of which ever existed.
//
// resolve_run_concern is outside the consolidation (it was never part of the
// eight) and is asserted separately.
var pulseConsolidatedToolNames = []string{
	"get_pulse_state",
	"record_pulse_worklist",
	"record_pulse_result",
}

var pulseReviewerWriteToolNames = []string{
	"record_pulse_finding",
	"merge_pulse_issues",
}

// pulseScheduleToolNames drive the workflow's own Pulse schedule: a Pulse pass
// chooses its next run, and a normal run's finalizer can ask for it earlier.
var pulseScheduleToolNames = []string{
	"record_pulse_next_run",
	"record_pulse_fast_request",
	// Goal Work items and constraint challenges (docs/design/pulse_goal_work.md).
	"record_pulse_goal_work",
	// The Goal Lead's daily goal check verdict (PLAT-697).
	"record_pulse_goal_check",
}

// pulseRemovedToolNames must never reappear. Each was folded into one of the
// four above; a stale registration would give the agent two ways to say the
// same thing, which is the condition this consolidation removed.
var pulseRemovedToolNames = []string{
	// Compact Pulse stores measurements in workflow/evaluation output and the
	// review conclusion; reviewers no longer maintain a parallel impact ledger.
	"record_pulse_impact",
	"get_pulse_module_state",
	"get_pulse_finding_backlog",
	"get_pulse_review_result",
	"start_pulse_fix_attempt",
	"mark_pulse_module_result",
	"mark_pulse_final_command_result",
	// Folded into get_pulse_state(view="focus_agenda") -- read-only, and
	// already fit get_pulse_state's existing one-tool-many-views shape.
	"get_pulse_review_focus_agenda",
	// Folded into record_pulse_result: manual=true claims an on-demand review,
	// and focuses[] persists the coverage alongside its terminal result.
	"record_pulse_module_due",
	"record_pulse_review_focus",
	// Retired legacy workflow-contract migration tools. Current workflows use
	// the live typed lifecycle and do not expose compatibility maintenance to
	// reviewers.
	"record_pulse_migration_reconciliation",
	"record_pulse_lifecycle_reconciliation",
	"record_pulse_actionable_backlog_reconciliation",
}

func TestPulseToolSurfaceIncludesTypedReviewerWrites(t *testing.T) {
	tools, executors, categories := createPulseWorklistTools()
	registered := map[string]bool{}
	for _, tool := range tools {
		if tool.Function == nil {
			t.Fatalf("tool with nil Function: %+v", tool)
		}
		registered[tool.Function.Name] = true
	}

	for _, name := range pulseConsolidatedToolNames {
		if !registered[name] {
			t.Errorf("consolidated Pulse tool %q is not registered", name)
		}
		if _, ok := executors[name]; !ok {
			t.Errorf("consolidated Pulse tool %q has no executor", name)
		}
		if categories[name] != "workflow" {
			t.Errorf("consolidated Pulse tool %q has category %q, want workflow", name, categories[name])
		}
	}
	for _, name := range pulseReviewerWriteToolNames {
		if !registered[name] {
			t.Errorf("typed reviewer tool %q is not registered", name)
		}
		if _, ok := executors[name]; !ok {
			t.Errorf("typed reviewer tool %q has no executor", name)
		}
		if categories[name] != "workflow" {
			t.Errorf("typed reviewer tool %q has category %q, want workflow", name, categories[name])
		}
	}
	for _, name := range pulseRemovedToolNames {
		if registered[name] {
			t.Errorf("removed Pulse tool %q is still registered", name)
		}
		if _, ok := executors[name]; ok {
			t.Errorf("removed Pulse tool %q still has an executor", name)
		}
		if _, ok := categories[name]; ok {
			t.Errorf("removed Pulse tool %q still has a category entry", name)
		}
	}

	// The surface is the consolidated lifecycle/coverage tools, typed reviewer
	// writes, and resolve_run_concern. merge_pulse_issues is intentionally the
	// one semantic maintenance verb: calling it record_* would conceal that it
	// retires duplicate queue entries while preserving their history.
	expected := map[string]bool{"resolve_run_concern": true}
	for _, name := range pulseConsolidatedToolNames {
		expected[name] = true
	}
	for _, name := range pulseReviewerWriteToolNames {
		expected[name] = true
	}
	for _, name := range pulseScheduleToolNames {
		expected[name] = true
	}
	for name := range registered {
		if !expected[name] {
			t.Errorf("unexpected Pulse tool %q; the surface is %v plus resolve_run_concern",
				name, pulseConsolidatedToolNames)
		}
	}
	if len(registered) != len(expected) {
		t.Errorf("registered %d Pulse tools, want %d", len(registered), len(expected))
	}

	// Every ordinary Pulse tool follows the one naming rule, so the agent can
	// derive a name instead of guessing one. The two explicit semantic actions
	// are documented by name in the returned tool index.
	for name := range registered {
		if name == "resolve_run_concern" || name == "merge_pulse_issues" {
			continue
		}
		if !strings.HasPrefix(name, "get_pulse_") && !strings.HasPrefix(name, "record_pulse_") {
			t.Errorf("Pulse tool %q breaks the verb rule: reads are get_pulse_*, writes are record_pulse_*", name)
		}
	}
}

func pulseStateExecutor(t *testing.T) func(context.Context, map[string]interface{}) (string, error) {
	t.Helper()
	_, executors, _ := createPulseWorklistTools()
	execute, ok := executors["get_pulse_state"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("get_pulse_state executor has unexpected type")
	}
	return execute
}

// TestGetPulseStateViewsReturnWhatTheirPredecessorsReturned pins the merge. Each
// view must still answer the exact question its own tool used to answer.
func TestGetPulseStateViewsReturnWhatTheirPredecessorsReturned(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	ctx := context.Background()
	workspacePath := "Workflow/example"
	execute := pulseStateExecutor(t)

	// view="module" — what get_pulse_module_state returned.
	raw, err := execute(ctx, map[string]interface{}{"workspace_path": workspacePath, "view": "module"})
	if err != nil {
		t.Fatalf(`get_pulse_state(view="module"): %v`, err)
	}
	var moduleView map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &moduleView); err != nil {
		t.Fatalf("decode module view: %v", err)
	}
	for _, key := range []string{
		"modules", "open_concerns", "open_concern_count", "concerns_note",
		"suppressed_concerns", "suppressed_concern_count", "plan_change_backlog",
		"loop_closure", "loop_closure_note", "module_review_history",
		"review_history_note", "impact_ledger", "impact_ledger_note",
	} {
		if _, exists := moduleView[key]; !exists {
			t.Errorf(`view="module" dropped %q, which get_pulse_module_state returned: %s`, key, raw)
		}
	}

	// view="backlog" — what get_pulse_finding_backlog returned.
	recordTestReviewFinding(t, ctx, workspacePath, "pulse-view", pulseModuleTechnicalReview, "the collector writes a null column")
	raw, err = execute(ctx, map[string]interface{}{"workspace_path": workspacePath, "view": "backlog"})
	if err != nil {
		t.Fatalf(`get_pulse_state(view="backlog"): %v`, err)
	}
	var backlogView struct {
		Issues  []map[string]interface{} `json:"issues"`
		Total   int                      `json:"total"`
		Summary map[string]interface{}   `json:"summary"`
		Note    string                   `json:"note"`
	}
	if err := json.Unmarshal([]byte(raw), &backlogView); err != nil {
		t.Fatalf("decode backlog view: %v", err)
	}
	if backlogView.Total != 1 || len(backlogView.Issues) != 1 || backlogView.Note == "" || backlogView.Summary["active_count"] != float64(1) {
		t.Fatalf(`view="backlog" did not return the durable issue backlog: %s`, raw)
	}
	if issueID, _ := backlogView.Issues[0]["issue_id"].(string); issueID == "" {
		t.Fatalf(`view="backlog" must expose PUL issue id: %+v`, backlogView.Issues[0])
	}
	if strings.Contains(raw, `"fingerprint"`) || strings.Contains(raw, `"finding_id"`) || strings.Contains(raw, `"attempt_id"`) {
		t.Fatalf(`view="backlog" leaked an internal lifecycle identity: %s`, raw)
	}
	// The optional module filter still filters, and still names the closed set.
	if _, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "view": "backlog", "module": "bugs",
	}); err == nil || !strings.Contains(err.Error(), "is not a valid Pulse module") ||
		!strings.Contains(err.Error(), pulseModuleTechnicalReview) {
		t.Fatalf(`view="backlog" module rejection must name the closed set: %v`, err)
	}

	// view="review" returns only the compact receipt. Findings and proof are
	// loaded from the lifecycle backlog, never from persisted reviewer prose.
	const reviewRunID = "2026-08-01T00-00-00.000Z_surface"
	if err := step_based_workflow.CompletePulseReview(
		ctx, workspacePath, []string{pulseModuleTechnicalReview}, reviewRunID, "pulse-view", "Clean.", "completed",
	); err != nil {
		t.Fatalf("record review: %v", err)
	}
	raw, err = execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "view": "review",
		"review_run_id": reviewRunID, "module": pulseModuleTechnicalReview,
	})
	if err != nil {
		t.Fatalf(`get_pulse_state(view="review"): %v`, err)
	}
	var reviewView map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &reviewView); err != nil {
		t.Fatalf("decode review view: %v", err)
	}
	for _, key := range []string{"module", "review_run_id", "pulse_run_id", "status", "verdict", "finding_count", "verification_count", "verifications"} {
		if _, exists := reviewView[key]; !exists {
			t.Errorf(`view="review" dropped compact receipt field %q: %s`, key, raw)
		}
	}
	if _, exists := reviewView["markdown"]; exists {
		t.Errorf(`view="review" unexpectedly returned legacy reviewer prose: %s`, raw)
	}
	if verdict, _ := reviewView["verdict"].(string); verdict != "Clean." {
		t.Errorf(`view="review" verdict = %q, want Clean.`, verdict)
	}

	// A not-yet-saved review is the expected result of the pre-discovery check the
	// review stage prompt mandates, so it must read as normal and must not send the
	// caller looking for a different id — the identity was validated just above.
	_, err = execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "view": "review",
		"review_run_id": "2026-08-01T00-00-00.000Z_missing", "module": pulseModuleTechnicalReview,
	})
	if err == nil {
		t.Fatal("missing review returned no error")
	}
	for _, want := range []string{
		"no Pulse review is saved",
		"do not look for a different review_run_id",
		"proceed with discovery and record your result",
		"resume from its completion notification",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing-review message omits %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "identity pair is wrong") {
		t.Errorf("missing-review message still offers a cause ValidatePulseReviewIdentity ruled out: %v", err)
	}
}

// Every rejection on the merged read must name the value set it enforces.
func TestGetPulseStateRejectionsNameTheirClosedSets(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	execute := pulseStateExecutor(t)
	ctx := context.Background()

	_, err := execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example", "view": "backlogs"})
	assertRejectionContains(t, err, `view "backlogs" is not a valid Pulse state view`, "backlog", "module", "review")

	_, err = execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example"})
	assertRejectionContains(t, err, "not a valid Pulse state view", "backlog", "module", "review")

	_, err = execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example", "view": "review"})
	assertRejectionContains(t, err, "review_run_id", "module",
		"review_run_id=missing", "module=missing", "completion notification")
}

// TestRecordPulseResultCoversBothFormerResultTypes pins the write merge: one
// tool, two durable records, selected by which of module or command arrives.
func TestRecordPulseResultCoversBothFormerResultTypes(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	workspacePath := "Workflow/example"
	pulseRunID := "schedule-cron--merged-result"
	sessionID := pulseRunID + "-session"
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)

	if _, err := recordPulseWorklist(context.Background(), workspacePath, pulseRunID,
		completePulseWorklistDecisions(map[string]PulseWorklistDecision{
			pulseModuleTechnicalReview: {Module: pulseModuleTechnicalReview, Due: true, Reason: "A review is required."},
		})); err != nil {
		t.Fatalf("record worklist: %v", err)
	}
	if err := initializePulseFinalCommandStates(context.Background(), workspacePath, pulseRunID); err != nil {
		t.Fatalf("initialize final commands: %v", err)
	}

	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))

	// Module form — what mark_pulse_module_result recorded.
	raw, err := execute(ctx, map[string]interface{}{
		"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
		"module": pulseModuleTechnicalReview, "result": "done",
		"reason": "No finding required a change.",
	})
	if err != nil {
		t.Fatalf("record module result: %v", err)
	}
	if !strings.Contains(raw, `"module"`) || !strings.Contains(raw, `"status":"updated"`) {
		t.Fatalf("module result payload = %s", raw)
	}
	states, err := getPulseModuleStates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("get module states: %v", err)
	}
	recorded := false
	for _, state := range states {
		if state.Module == pulseModuleTechnicalReview {
			recorded = state.LastResult == "done"
		}
	}
	if !recorded {
		t.Fatalf("module result was not persisted: %+v", states)
	}

	// Final-command form — what mark_pulse_final_command_result recorded, with
	// the same running-before-terminal ordering rule.
	commandArgs := func(status string) map[string]interface{} {
		return map[string]interface{}{
			"workspace_path": workspacePath, "pulse_run_id": pulseRunID,
			"command": pulseFinalCommandBackup, "result": status,
			"reason": "Backup " + status,
		}
	}
	if _, err := execute(ctx, commandArgs("done")); err == nil || !strings.Contains(err.Error(), "marked running before done") {
		t.Fatalf("final command ordering rule was lost: %v", err)
	}
	if _, err := execute(ctx, commandArgs("running")); err != nil {
		t.Fatalf("record running command: %v", err)
	}
	raw, err = execute(ctx, commandArgs("done"))
	if err != nil {
		t.Fatalf("record terminal command: %v", err)
	}
	if !strings.Contains(raw, `"command"`) || !strings.Contains(raw, `"status":"updated"`) {
		t.Fatalf("command result payload = %s", raw)
	}
	commands, err := getPulseFinalCommandStates(context.Background(), workspacePath)
	if err != nil {
		t.Fatalf("get final command states: %v", err)
	}
	for _, state := range commands {
		if state.Command == pulseFinalCommandBackup && state.Status != "done" {
			t.Fatalf("final command result was not persisted: %+v", state)
		}
	}
}

// The discriminator is the one thing an agent can get wrong on the merged
// writer, so its rejection names both alternatives and both closed sets.
func TestRecordPulseResultRejectionsNameBothTargets(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	_, executors, _ := createPulseWorklistTools()
	execute := executors["record_pulse_result"].(func(context.Context, map[string]interface{}) (string, error))
	sessionID := "merged-reject-session"
	pulseRunID := "schedule-cron--merged-reject"
	ctx := mcpexecutor.WithSessionID(context.Background(), sessionID)

	base := map[string]interface{}{
		"workspace_path": "Workflow/example", "pulse_run_id": pulseRunID,
		"result": "done", "reason": "Nothing to do.",
	}
	clone := func(extra map[string]interface{}) map[string]interface{} {
		args := map[string]interface{}{}
		for key, value := range base {
			args[key] = value
		}
		for key, value := range extra {
			args[key] = value
		}
		return args
	}

	_, err := execute(ctx, clone(nil))
	assertRejectionContains(t, err, "exactly one of module or command",
		"module=missing", "command=missing", pulseModuleTechnicalReview, pulseFinalCommandBackup)

	_, err = execute(ctx, clone(map[string]interface{}{
		"module": pulseModuleTechnicalReview, "command": pulseFinalCommandBackup,
	}))
	assertRejectionContains(t, err, "exactly one of module or command", "module=set", "command=set")

	// Each target still enforces its own result set, and says which one applies.
	_, err = execute(ctx, clone(map[string]interface{}{"module": pulseModuleTechnicalReview, "result": "running"}))
	assertRejectionContains(t, err, `result "running" is not valid`, "changed", "skipped")

	_, err = execute(ctx, clone(map[string]interface{}{"command": pulseFinalCommandBackup, "result": "changed"}))
	assertRejectionContains(t, err, `result "changed" is not valid`, "running", "skipped")
}

// TestSchedulerPulsePromptsNameNoRemovedTool is the invariant that failed the
// last time a tool signature moved here: a read_skill change landed while 36
// templates still emitted the old form, so every call was schema-rejected until
// the templates caught up. The template side is covered in the guidance package;
// this covers the prompts the scheduler builds in Go.
func TestSchedulerPulsePromptsNameNoRemovedTool(t *testing.T) {
	prompts := map[string]string{}
	for _, step := range pulseLifecycleSteps() {
		prompts[step.label] = step.query
	}
	// Contract-upgrade turns are delivered by the pre-run preflight, not by
	// Pulse, but they name tools too and are built in the same package.
	for _, upgrade := range workflowVersionUpgradePlan(&WorkflowManifest{Version: "1.0.0"}) {
		prompts[upgrade.label+"-upgrade"] = upgrade.query
	}
	if len(prompts) == 0 {
		t.Fatal("no scheduler Pulse prompts were collected")
	}
	for label, prompt := range prompts {
		if strings.Contains(prompt, "read_skill") && strings.Contains(prompt, "},{") {
			t.Errorf("scheduler prompt %q batches multiple read_skill files; issue one call per file", label)
		}
		for _, removed := range pulseRemovedToolNames {
			if strings.Contains(prompt, removed) {
				t.Errorf("scheduler prompt %q still instructs agents to call removed tool %q", label, removed)
			}
		}
	}
}
