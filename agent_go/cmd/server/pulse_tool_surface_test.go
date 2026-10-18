package server

import (
	"context"
	"strings"
	"testing"
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
}

var pulseReviewerWriteToolNames = []string{
	"record_pulse_finding",
	"merge_pulse_issues",
}

// pulseScheduleToolNames drive the workflow's own Pulse schedule: a Pulse pass
// chooses its next run, and a normal run's finalizer can ask for it earlier.
var pulseScheduleToolNames = []string{
	"record_pulse_fast_request",
	// Goal Work items and constraint challenges (docs/design/pulse_goal_work.md).
	"record_pulse_goal_work",
	// The Pulse's daily goal check verdict (PLAT-697).
	"record_pulse_goal_check",
	// Phase 3: a recommendation on a decision (never an answer), the
	// decision's outcome, and goal memory.
	"record_pulse_recommendation",
	"record_pulse_decision_outcome",
	"record_pulse_goal_memory",
	// Phase 4: the Pulse's focus areas and QA requests.
	"record_pulse_focus_area",
}

// pulseRemovedToolNames must never reappear. Each was folded into one of the
// four above; a stale registration would give the agent two ways to say the
// same thing, which is the condition this consolidation removed.
var pulseRemovedToolNames = []string{
	"record_pulse_worklist", "record_pulse_result", "record_pulse_qa_request", "record_pulse_result", "record_pulse_qa_request",
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
	tools, executors, categories := createPulseTools()
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
	_, executors, _ := createPulseTools()
	execute, ok := executors["get_pulse_state"].(func(context.Context, map[string]interface{}) (string, error))
	if !ok {
		t.Fatal("get_pulse_state executor has unexpected type")
	}
	return execute
}

// Every rejection on the merged read must name the value set it enforces.
func TestGetPulseStateRejectionsNameTheirClosedSets(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	execute := pulseStateExecutor(t)
	ctx := context.Background()

	_, err := execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example", "view": "backlogs"})
	assertRejectionContains(t, err, `view "backlogs" is not a valid saved Pulse view`, "backlog", "goal_status", "review")

	_, err = execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example"})
	assertRejectionContains(t, err, "not a valid saved Pulse view", "backlog", "goal_status", "review")

	_, err = execute(ctx, map[string]interface{}{"workspace_path": "Workflow/example", "view": "review"})
	assertRejectionContains(t, err, "review_run_id", "module",
		"review_run_id=missing", "module=missing", "saved historical review")
}

// TestSchedulerPulsePromptsNameNoRemovedTool is the invariant that failed the
// last time a tool signature moved here: a read_skill change landed while 36
// templates still emitted the old form, so every call was schema-rejected until
// the templates caught up. The template side is covered in the guidance package;
// this covers the prompts the scheduler builds in Go.
func TestSchedulerPulsePromptsNameNoRemovedTool(t *testing.T) {
	prompts := map[string]string{"charter": goalLeadCharter("Example", "Workflow/example")}
	for _, step := range afterRunHousekeepingSteps("example", true, true) {
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
