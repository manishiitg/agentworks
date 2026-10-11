package step_based_workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func changelogWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	// The fixtures are dated July 2026; read them as recent (PLAT-593 horizon).
	planChangeBacklogNow = func() time.Time { return time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { planChangeBacklogNow = time.Now })
	dir := filepath.Join(root, "Workflow", "testing", PlanningFolderName, "changelog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return "Workflow/testing"
}

const mixedChangelog = `{"entries":[
 {"timestamp":"2026-07-22T09:00:00Z","tool":"update_regular_step","reason":"retire the regression pass","step_ids":["step-a"],"changes":[{"step_id":"step-a","field":"context_dependencies","old_value":"o","new_value":"n"}]},
 {"timestamp":"2026-07-21T09:00:00Z","tool":"update_step_config","reason":"already reconciled","step_ids":["step-b"],"artifact_review":{"done":true,"reviewed_by":"pulse_fixer"}},
 {"timestamp":"2026-07-20T09:00:00Z","tool":"update_validation_schema","reason":"tighten output","step_ids":["step-c"],"changes":[{"step_id":"step-c","field":"validation_schema"}]}
]}`

// A change whose only named field is cosmetic (wording, review notes,
// schedule timing, model/tier settings) cannot orphan a dependent step, eval,
// report, or DB contract, so it must never enter the backlog at all -- not
// even as a listed-but-harmless entry.
const cosmeticOnlyChangelog = `{"entries":[
 {"timestamp":"2026-07-23T09:00:00Z","tool":"update_regular_step","reason":"clarify wording","step_ids":["step-d"],"changes":[{"step_id":"step-d","field":"description","old_value":"o","new_value":"n"}]},
 {"timestamp":"2026-07-22T09:00:00Z","tool":"update_regular_step","reason":"rename the step","step_ids":["step-e"],"changes":[{"step_id":"step-e","field":"title","old_value":"o","new_value":"n"}]},
 {"timestamp":"2026-07-21T09:00:00Z","tool":"write_workflow_manifest","reason":"reschedule","step_ids":[],"changes":[{"field":"workflow.json.schedules"},{"field":"workflow.json.updated_at"}]},
 {"timestamp":"2026-07-20T09:00:00Z","tool":"update_step_config","reason":"pin a model","step_ids":["step-f"],"changes":[{"step_id":"step-f","field":"workflow.json.capabilities.llm_config.builder_llm"}]}
]}`

// The backlog must be exactly the entries nobody has stamped. A stamped entry is
// reconciled work and re-reporting it would make the count meaningless.
func TestCollectPlanChangeBacklogExcludesReviewedEntries(t *testing.T) {
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": mixedChangelog})
	got := CollectPlanChangeBacklog(ws)
	if got == nil {
		t.Fatal("expected a backlog")
	}
	if got.UnreviewedCount != 2 {
		t.Fatalf("unreviewed count = %d, want 2 (the third is stamped done)", got.UnreviewedCount)
	}
	for _, c := range got.Changes {
		if c.Reason == "already reconciled" {
			t.Fatalf("stamped entry leaked into the backlog: %#v", c)
		}
	}
}

// Newest first, and each entry must carry the surface a reviewer traces: the
// reason, the steps, and which fields moved.
func TestCollectPlanChangeBacklogCarriesTriageDetail(t *testing.T) {
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": mixedChangelog})
	got := CollectPlanChangeBacklog(ws)
	if got.Changes[0].At != "2026-07-22T09:00:00Z" {
		t.Fatalf("expected newest first, got %q", got.Changes[0].At)
	}
	first := got.Changes[0]
	if first.Reason != "retire the regression pass" || len(first.StepIDs) != 1 || first.StepIDs[0] != "step-a" {
		t.Fatalf("triage detail missing: %#v", first)
	}
	if len(first.FieldsChanged) != 1 || first.FieldsChanged[0] != "context_dependencies" {
		t.Fatalf("changed fields missing: %#v", first)
	}
	if first.SourceFile != "changelog-a.json" {
		t.Fatalf("source file missing: %#v", first)
	}
	// It must read as evidence, not as a claim that something is broken.
	if !strings.Contains(got.Note, "not a verdict") {
		t.Fatalf("note should disclaim judgement: %q", got.Note)
	}
}

// Description/title wording, schedule timing, and model/tier settings are
// cosmetic: none of them can affect a dependent, so the backlog must be nil,
// not merely small.
func TestCollectPlanChangeBacklogExcludesCosmeticOnlyEntries(t *testing.T) {
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": cosmeticOnlyChangelog})
	if got := CollectPlanChangeBacklog(ws); got != nil {
		t.Fatalf("cosmetic-only changes must produce no backlog, got %#v", got)
	}
}

// An untyped update_step_config call and a step add/delete record no field
// names at all -- the unknown blast radius stays conservative and the entry
// is still counted, unlike a change whose named fields are all cosmetic.
func TestCollectPlanChangeBacklogKeepsEntriesWithNoNamedFields(t *testing.T) {
	untyped := `{"entries":[{"timestamp":"2026-07-22T09:00:00Z","tool":"update_step_config","reason":"tune retries","step_ids":["step-g"]}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": untyped})
	got := CollectPlanChangeBacklog(ws)
	if got == nil || got.UnreviewedCount != 1 {
		t.Fatalf("an entry with no field names must stay in the backlog, got %#v", got)
	}
}

// A workflow that has reconciled everything is the healthy case and must add
// nothing to Pulse's context.
func TestCollectPlanChangeBacklogNilWhenAllReviewed(t *testing.T) {
	allDone := `{"entries":[{"timestamp":"2026-07-22T09:00:00Z","tool":"update_regular_step","reason":"r","artifact_review":{"done":true}}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": allDone})
	if got := CollectPlanChangeBacklog(ws); got != nil {
		t.Fatalf("fully reconciled workflow should produce no backlog, got %#v", got)
	}
}

func TestModernReviewedPlanChangeStillFailsWhenDependencyCoverageIsIncomplete(t *testing.T) {
	modernIncomplete := `{"entries":[{"change_id":"change-modern","timestamp":"2026-08-28T09:00:00Z","tool":"update_agent_step","reason":"change output contract","artifact_review":{"done":true,"surfaces":{"downstream_steps":{"disposition":"updated","evidence":["consumer checked"]}}}}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-modern.json": modernIncomplete})

	backlog := CollectPlanChangeBacklog(ws)
	if backlog == nil || backlog.DependencyCoverageFailureCount != 1 || len(backlog.Changes) != 1 {
		t.Fatalf("incomplete modern dependency receipt disappeared: %#v", backlog)
	}
	problems := strings.Join(backlog.Changes[0].DependencyCoverageProblems, "; ")
	for _, surface := range []string{"validation", "evaluation", "reporting", "database", "learnings_and_knowledge"} {
		if !strings.Contains(problems, surface) {
			t.Fatalf("coverage problems omit %s: %q", surface, problems)
		}
	}
	intake := BuildPlanChangeDependencyIntake(backlog)
	if !intake.Failed || intake.FailureCount != 1 || len(intake.Findings) != 1 {
		t.Fatalf("deterministic intake did not fail: %#v", intake)
	}
}

func TestModernPlanChangeWithCompleteDependencyCoverageIsClosed(t *testing.T) {
	modernComplete := `{"entries":[{"change_id":"change-modern","timestamp":"2026-08-28T09:00:00Z","tool":"update_agent_step","reason":"change output contract","artifact_review":{"done":true,"surfaces":{
		"downstream_steps":{"disposition":"updated","evidence":["consumer contract updated"]},
		"validation":{"disposition":"already_compatible","evidence":["schema covers the new output"]},
		"evaluation":{"disposition":"not_applicable","evidence":["no evaluator consumes this field"]},
		"reporting":{"disposition":"updated","evidence":["report query updated"]},
		"database":{"disposition":"already_compatible","evidence":["column contract unchanged"]},
		"learnings_and_knowledge":{"disposition":"not_applicable","evidence":["no reusable guidance changed"]}
	}}}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-modern.json": modernComplete})
	if backlog := CollectPlanChangeBacklog(ws); backlog != nil {
		t.Fatalf("complete modern dependency receipt remained pending: %#v", backlog)
	}
	if intake := BuildPlanChangeDependencyIntake(nil); intake.Failed || intake.FailureCount != 0 {
		t.Fatalf("clean deterministic intake = %#v", intake)
	}
}

func TestLegacyReviewedPlanChangeIsNotReopenedForMissingModernReceipt(t *testing.T) {
	legacy := `{"entries":[{"timestamp":"2026-07-01T09:00:00Z","tool":"update_regular_step","reason":"legacy change","artifact_review":{"done":true}}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-legacy.json": legacy})
	if backlog := CollectPlanChangeBacklog(ws); backlog != nil {
		t.Fatalf("legacy reviewed change was reopened: %#v", backlog)
	}
}

func TestCollectPlanChangeBacklogNilWhenNoChangelog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	if got := CollectPlanChangeBacklog("Workflow/testing"); got != nil {
		t.Fatalf("missing changelog should produce no backlog, got %#v", got)
	}
}

// One unreadable file must not hide the outstanding changes recorded in the
// others — the count would silently understate the backlog.
func TestCollectPlanChangeBacklogSurvivesMalformedFile(t *testing.T) {
	ws := changelogWorkspace(t, map[string]string{
		"changelog-a.json": mixedChangelog,
		"changelog-b.json": "{not json",
	})
	got := CollectPlanChangeBacklog(ws)
	if got == nil || got.UnreviewedCount != 2 {
		t.Fatalf("malformed sibling should not suppress the real backlog, got %#v", got)
	}
}

// An entry with an unparseable timestamp is still outstanding work; dropping it
// would lose a real change because of a formatting problem.
func TestCollectPlanChangeBacklogKeepsUndateableEntries(t *testing.T) {
	bad := `{"entries":[{"timestamp":"whenever","tool":"update_regular_step","reason":"no date"}]}`
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": bad})
	got := CollectPlanChangeBacklog(ws)
	if got == nil || got.UnreviewedCount != 1 {
		t.Fatalf("undateable entry should still count, got %#v", got)
	}
}

// The count is always exact; only the listing is capped, and it must say so.
func TestCollectPlanChangeBacklogCapsListingNotCount(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"entries":[`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"timestamp":"2026-07-20T09:00:0` + string(rune('0'+i%10)) + `Z","tool":"update_step_config","reason":"r"}`)
	}
	b.WriteString(`]}`)

	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": b.String()})
	got := CollectPlanChangeBacklog(ws)
	if got.UnreviewedCount != 20 {
		t.Fatalf("count must be exact, got %d", got.UnreviewedCount)
	}
	if len(got.Changes) != maxListedPlanChanges {
		t.Fatalf("listing should cap at %d, got %d", maxListedPlanChanges, len(got.Changes))
	}
	if !strings.Contains(got.Note, "Showing the") {
		t.Fatalf("note must disclose truncation: %q", got.Note)
	}
}

// TestWrongStepTypeRefusalNamesTheRightTool covers the mistake that turned a
// solvable finding into a fake blocker.
//
// latency-report's collectors are agent and todo_task steps. A fixer
// tried update_scripted_step, was correctly refused by a message saying only
// "use its type-specific update tool", and concluded the steps were "not
// editable". It filed that as blocked and re-reported it for days, while
// update_agent_step was in its own tool surface throughout.
func TestWrongStepTypeRefusalNamesTheRightTool(t *testing.T) {
	err := wrongStepTypeToolError("step-daily-latency-collect-dev-voice", StepTypeAgent, "update_scripted_step")
	if err == nil {
		t.Fatal("a wrong-tool call was not refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "update_agent_step") {
		t.Fatalf("refusal does not name the tool that works: %q", msg)
	}
	if !strings.Contains(msg, "editable") {
		t.Fatalf("refusal does not say the step is editable, so it still reads as a capability gap: %q", msg)
	}

	for stepType, want := range map[StepType]string{
		StepTypeAgent:      "update_agent_step",
		StepTypeRouting:    "update_routing_step",
		StepTypeHumanInput: "update_human_input_step",
		StepTypeRegular:    "update_scripted_step",
	} {
		if got := updateToolForStepType(stepType); got != want {
			t.Fatalf("step type %q maps to %q, want %q", stepType, got, want)
		}
	}
}

// An old-format change more than 30 days old is left out: the reference map
// checks the current workflow instead. A recent one and any change with a
// change_id stay (PLAT-593).
func TestOldLegacyPlanChangesLeaveTheBacklog(t *testing.T) {
	ws := changelogWorkspace(t, map[string]string{"changelog-a.json": `{"entries":[
 {"timestamp":"2026-05-01T09:00:00Z","tool":"update_validation_schema","reason":"old legacy","step_ids":["step-a"],"changes":[{"step_id":"step-a","field":"validation_schema"}]},
 {"timestamp":"2026-07-20T09:00:00Z","tool":"update_validation_schema","reason":"recent legacy","step_ids":["step-b"],"changes":[{"step_id":"step-b","field":"validation_schema"}]}
]}`})
	got := CollectPlanChangeBacklog(ws)
	if got == nil || got.UnreviewedCount != 1 || got.SupersededLegacyCount != 1 || got.Changes[0].Reason != "recent legacy" {
		t.Fatalf("backlog = %#v, want only the recent legacy change and one superseded", got)
	}
	if !strings.Contains(got.Note, "reference map") {
		t.Fatalf("note must say why older changes are not listed: %q", got.Note)
	}
}
