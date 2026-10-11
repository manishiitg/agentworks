package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestClearDescriptionReviewedAfterPlanUpdate(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{
  "steps": [
    {
      "id": "step-a",
      "agent_configs": {
        "description_reviewed": true,
        "review_notes": "old review"
      }
    }
  ]
}`,
	}

	readFile := func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}

	cleared, err := clearDescriptionReviewedAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "description",
	}}, readFile, writeFile)
	if err != nil {
		t.Fatalf("clearDescriptionReviewedAfterPlanUpdate returned error: %v", err)
	}
	if !cleared {
		t.Fatalf("expected description_reviewed to be cleared")
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	if got := out.Steps[0].AgentConfigs.DescriptionReviewed; got != nil {
		t.Fatalf("description_reviewed = %v, want nil", *got)
	}
	if got := out.Steps[0].AgentConfigs.ReviewNotes; got != "old review" {
		t.Fatalf("review_notes = %q, want preserved review notes", got)
	}
}

func TestClearDescriptionReviewedAfterPlanUpdateSkipsTitleOnly(t *testing.T) {
	ctx := context.Background()
	writeCalled := false
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"description_reviewed":true}}]}`,
	}

	cleared, err := clearDescriptionReviewedAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "title",
	}}, func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}, func(_ context.Context, path, content string) error {
		writeCalled = true
		files[path] = content
		return nil
	})
	if err != nil {
		t.Fatalf("clearDescriptionReviewedAfterPlanUpdate returned error: %v", err)
	}
	if cleared {
		t.Fatalf("title-only changes should not clear description_reviewed")
	}
	if writeCalled {
		t.Fatalf("title-only changes should not write step_config.json")
	}
}

func TestClearDriftReviewAfterPlanUpdate(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{
  "steps": [
    {
      "id": "step-a",
      "agent_configs": {
        "drift_review": {
          "reviewed_at": "2026-08-01T00:00:00Z",
          "reviewed_by": "pulse:plan_drift_review",
          "checks": [{"check_id": "report_query_compatibility", "status": "pass", "evidence": "all 3 report queries ran cleanly"}]
        },
        "review_notes": "old review"
      }
    }
  ]
}`,
	}

	readFile := func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}

	cleared, err := clearDriftReviewAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "context_dependencies",
	}}, readFile, writeFile)
	if err != nil {
		t.Fatalf("clearDriftReviewAfterPlanUpdate returned error: %v", err)
	}
	if !cleared {
		t.Fatalf("expected drift_review to be flagged")
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("updated step_config.json is invalid JSON: %v", err)
	}
	// Stale flag model: the record is flagged, not nulled — its evidence
	// (Checks/ReviewedAt/ReviewedBy) survives until a completed review
	// replaces it.
	got := out.Steps[0].AgentConfigs.DriftReview
	if got == nil {
		t.Fatalf("drift_review = nil, want the record preserved with needs_review=true")
	}
	if !got.NeedsReview {
		t.Fatalf("drift_review.needs_review = false, want true after a dependency-triggering edit")
	}
	if got.ReviewedAt != "2026-08-01T00:00:00Z" || got.ReviewedBy != "pulse:plan_drift_review" || len(got.Checks) != 1 {
		t.Fatalf("drift_review evidence was not preserved: %+v", got)
	}
	if got := out.Steps[0].AgentConfigs.ReviewNotes; got != "old review" {
		t.Fatalf("review_notes = %q, want preserved review notes", got)
	}
}

// A step already flagged (needs_review already true) from an earlier edit in
// the same unreviewed window must not report a new "cleared" event — nothing
// about its due-ness changed.
func TestClearDriftReviewAfterPlanUpdateIsIdempotentWhenAlreadyFlagged(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"needs_review":true,"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}]}`,
	}
	writeCalled := false
	cleared, err := clearDriftReviewAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "context_dependencies",
	}}, func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}, func(_ context.Context, path, content string) error {
		writeCalled = true
		files[path] = content
		return nil
	})
	if err != nil {
		t.Fatalf("clearDriftReviewAfterPlanUpdate returned error: %v", err)
	}
	if cleared {
		t.Fatalf("an already-flagged review should not report a new clear event")
	}
	if writeCalled {
		t.Fatalf("an already-flagged review should not trigger a redundant write")
	}
}

// 2026-09-26: a title- or description-only edit must NOT flag
// drift_review.needs_review. It supersedes the prior "any field, title
// included" contract, which made plan_drift_review due on nearly every plan
// edit — description-only edits were the single largest share of live
// workflows' unreviewed changelog backlog. A wording rewrite occasionally
// hides a real behavior change, but that then shows up as an output or data
// change on the step's next run, which Pulse's own step-output/step-concern
// review already catches.
func TestClearDriftReviewAfterPlanUpdateSkipsTitleAndDescriptionOnly(t *testing.T) {
	ctx := context.Background()
	for _, field := range []string{"title", "description"} {
		t.Run(field, func(t *testing.T) {
			files := map[string]string{
				"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}]}`,
			}
			writeCalled := false
			cleared, err := clearDriftReviewAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
				StepID: "step-a",
				Field:  field,
			}}, func(_ context.Context, path string) (string, error) {
				return files[path], nil
			}, func(_ context.Context, path, content string) error {
				writeCalled = true
				files[path] = content
				return nil
			})
			if err != nil {
				t.Fatalf("clearDriftReviewAfterPlanUpdate returned error: %v", err)
			}
			if cleared || writeCalled {
				t.Fatalf("a %s-only change must not flag drift_review.needs_review", field)
			}
		})
	}
}

// A field that can affect a dependent (here context_dependencies) still
// flags the step, and the prior review's evidence survives untouched — the
// stale-flag model, unchanged by the 2026-09-26 materiality narrowing.
func TestClearDriftReviewAfterPlanUpdateFlagsOnMaterialField(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}]}`,
	}

	cleared, err := clearDriftReviewAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "context_dependencies",
	}}, func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}, func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	})
	if err != nil {
		t.Fatalf("clearDriftReviewAfterPlanUpdate returned error: %v", err)
	}
	if !cleared {
		t.Fatalf("a context_dependencies change must flag drift_review.needs_review")
	}

	var out StepConfigFile
	if err := json.Unmarshal([]byte(files["planning/step_config.json"]), &out); err != nil {
		t.Fatalf("failed to parse updated step_config.json: %v", err)
	}
	got := out.Steps[0].AgentConfigs.DriftReview
	if got == nil || !got.NeedsReview {
		t.Fatalf("drift_review.needs_review should be true after a context_dependencies change, got %#v", got)
	}
	// Evidence from the prior review must survive — the stale flag model
	// preserves it until a completed review replaces it, unlike the earlier
	// null-on-edit design this superseded.
	if len(got.Checks) != 1 || got.ReviewedBy != "pulse:plan_drift_review" {
		t.Fatalf("prior review evidence was not preserved: %+v", got)
	}
}

// A field change that is entirely cosmetic (multiple non-material fields at
// once) still must not flag the step -- materiality is checked per field,
// not just on the first one.
func TestClearDriftReviewAfterPlanUpdateSkipsMultipleNonMaterialFields(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"pulse:plan_drift_review"}}}]}`,
	}
	cleared, err := clearDriftReviewAfterPlanUpdate(ctx, "", "step-a", []PlanFieldChange{
		{StepID: "step-a", Field: "title"},
		{StepID: "step-a", Field: "description"},
	}, func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}, func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	})
	if err != nil {
		t.Fatalf("clearDriftReviewAfterPlanUpdate returned error: %v", err)
	}
	if cleared {
		t.Fatalf("title and description together are still cosmetic-only and must not flag drift_review")
	}
}

func TestArtifactReviewNotices(t *testing.T) {
	updateNotice := buildPlanStepDependentArtifactReviewNotice("step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "description",
	}}, true, true, false)
	for _, want := range []string{
		"Plan change recorded",
		"description",
		"one combined compatibility check",
		"small internal edits",
		"affected dependencies before testing",
		"plan-change-impact.md",
		"review-artifact-drift",
		"drift_review",
		"plan_drift_review",
	} {
		if !strings.Contains(updateNotice, want) {
			t.Fatalf("update notice missing %q:\n%s", want, updateNotice)
		}
	}
	if strings.Contains(updateNotice, "FAILED to flag") {
		t.Fatalf("update notice should not warn about a flag failure when driftReviewFlagFailed is false:\n%s", updateNotice)
	}

	addNotice := buildAddedStepArtifactSetupNotice("new-step", "regular")
	for _, want := range []string{"New step artifact setup required", "planning/step_config.json", "learnings/new-step/main.py"} {
		if !strings.Contains(addNotice, want) {
			t.Fatalf("add notice missing %q:\n%s", want, addNotice)
		}
	}

	deleteNotice := buildDeletedStepArtifactCleanupNotice([]string{"old-step"}, []string{"old-step"}, false)
	for _, want := range []string{"Deleted step artifact cleanup required", "Removed matching planning/step_config.json entries", "learnings/<step-id>"} {
		if !strings.Contains(deleteNotice, want) {
			t.Fatalf("delete notice missing %q:\n%s", want, deleteNotice)
		}
	}
	if strings.Contains(deleteNotice, "FAILED to flag the workflow-level drift review record") {
		t.Fatal("delete notice should not warn about a flag failure when driftReviewFlagFailed is false")
	}

	failedDeleteNotice := buildDeletedStepArtifactCleanupNotice([]string{"old-step"}, nil, true)
	if !strings.Contains(failedDeleteNotice, "FAILED to flag the workflow-level drift review record") {
		t.Fatalf("delete notice missing loud drift-review-flag-failed warning:\n%s", failedDeleteNotice)
	}

	routeNotice := buildOrchestratorRouteArtifactReviewNotice("parent", "route-a", "deleted", true, true, false)
	for _, want := range []string{"Todo route artifact review required", "description_reviewed", "drift_review", "learnings/route-a"} {
		if !strings.Contains(routeNotice, want) {
			t.Fatalf("route notice missing %q:\n%s", want, routeNotice)
		}
	}
}

// These responses go straight back to the editing agent. No mutation path,
// including a failed tracking write, should turn a targeted test into a full
// background audit. Keep the same batching policy across add/update/delete/routes.
func TestPlanEditNoticesUseTargetedChecks(t *testing.T) {
	for name, notice := range map[string]string{
		"internal edit":            buildPlanStepDependentArtifactReviewNotice("step-a", []PlanFieldChange{{Field: "description"}}, true, true, false),
		"output change":            buildPlanStepDependentArtifactReviewNotice("step-a", []PlanFieldChange{{Field: "context_output"}}, true, true, false),
		"new step":                 buildAddedStepArtifactSetupNotice("step-a", "agent"),
		"deleted step":             buildDeletedStepArtifactCleanupNotice([]string{"step-a"}, nil, false),
		"failed deletion tracking": buildDeletedStepArtifactCleanupNotice([]string{"step-a"}, nil, true),
		"route change":             buildOrchestratorRouteArtifactReviewNotice("parent", "route-a", "updated", true, true, false),
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range []string{
				"After related edits are complete",
				"one combined compatibility check in the current agent",
				"before the targeted test",
				"Do not launch a separate drift reviewer for each edit or test retry",
				"scheduled Pulse or an explicit user request",
			} {
				if !strings.Contains(notice, want) {
					t.Errorf("missing %q: %s", want, notice)
				}
			}
			if strings.Contains(notice, "get_workflow_command_guidance(") {
				t.Errorf("mutation response still directs the agent to launch an audit: %s", notice)
			}
		})
	}
	if notice := buildPlanStepDependentArtifactReviewNotice("step-a", nil, false, false, false); notice != "" {
		t.Fatalf("no-op edit should not request a new check: %s", notice)
	}
}

// A persistent flag-write failure (both attempts) must surface loudly in the
// notice text the calling agent actually sees, not just a logger.Warn call
// nothing reads — the concrete fix for the update/flag atomicity gap.
func TestArtifactReviewNoticesSurfaceDriftReviewFlagFailure(t *testing.T) {
	updateNotice := buildPlanStepDependentArtifactReviewNotice("step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "description",
	}}, false, false, true)
	if !strings.Contains(updateNotice, "FAILED to flag") || !strings.Contains(updateNotice, "needs_review") {
		t.Fatalf("update notice does not surface the drift_review flag failure:\n%s", updateNotice)
	}

	routeNotice := buildOrchestratorRouteArtifactReviewNotice("parent", "route-a", "deleted", false, false, true)
	if !strings.Contains(routeNotice, "FAILED to flag") {
		t.Fatalf("route notice does not surface the drift_review flag failure:\n%s", routeNotice)
	}
}

// The retry wrapper must succeed on a second attempt after a first-attempt
// failure, and must not retry (or double-write) once the underlying write
// has already succeeded.
func TestClearDriftReviewAfterPlanUpdateRetriedSucceedsOnSecondAttempt(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}]}`,
	}
	attempt := 0
	readFile := func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		attempt++
		if attempt == 1 {
			return fmt.Errorf("simulated transient write failure")
		}
		files[path] = content
		return nil
	}
	cleared, err := clearDriftReviewAfterPlanUpdateRetried(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "context_dependencies",
	}}, readFile, writeFile, loggerv2.NewNoop())
	if err != nil {
		t.Fatalf("expected the retry to succeed, got error: %v", err)
	}
	if !cleared {
		t.Fatal("expected the retried write to report cleared=true")
	}
	if attempt != 2 {
		t.Fatalf("expected exactly 2 write attempts, got %d", attempt)
	}
}

func TestClearDriftReviewAfterPlanUpdateRetriedReturnsPersistentFailure(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"planning/step_config.json": `{"steps":[{"id":"step-a","agent_configs":{"drift_review":{"reviewed_at":"2026-08-01T00:00:00Z","reviewed_by":"x","checks":[{"check_id":"report_query_compatibility","status":"pass","evidence":"all report queries ran cleanly"}]}}}]}`,
	}
	readFile := func(_ context.Context, path string) (string, error) {
		return files[path], nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		return fmt.Errorf("simulated persistent write failure")
	}
	_, err := clearDriftReviewAfterPlanUpdateRetried(ctx, "", "step-a", []PlanFieldChange{{
		StepID: "step-a",
		Field:  "context_dependencies",
	}}, readFile, writeFile, loggerv2.NewNoop())
	if err == nil {
		t.Fatal("expected a persistent write failure to be returned after both attempts fail")
	}
}

func TestUpdateSingleStepTracksArtifactRelevantFields(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&HumanInputPlanStep{
			CommonStepFields: CommonStepFields{
				ID:    "ask-user",
				Title: "Ask user",
			},
			Question:   "Old question?",
			NextStepID: "step-old",
		},
		&RoutingPlanStep{
			CommonStepFields: CommonStepFields{
				ID:    "route",
				Title: "Route",
			},
			RoutingQuestion: "Old route?",
			Routes: []RoutingRoute{
				{RouteID: "a", RouteName: "A", Condition: "old", NextStepID: "end"},
				{RouteID: "b", RouteName: "B", Condition: "old", NextStepID: "end"},
			},
		},
	}}

	var humanChanges []PlanFieldChange
	if _, _, err := updateSingleStep(plan, PartialPlanStep{
		ExistingStepID: "ask-user",
		Question:       "New question?",
		ResponseType:   "multiple_choice",
		Options:        []string{"A", "B"},
		NextStepID:     "step-new",
	}, &humanChanges); err != nil {
		t.Fatalf("updateSingleStep human input returned error: %v", err)
	}
	for _, want := range []string{"question", "response_type", "options", "next_step_id"} {
		if !hasPlanFieldChange(humanChanges, want) {
			t.Fatalf("human field changes missing %q: %#v", want, humanChanges)
		}
	}

	var routingChanges []PlanFieldChange
	if _, _, err := updateSingleStep(plan, PartialPlanStep{
		ExistingStepID:   "route",
		RoutingQuestion:  "New route?",
		DefaultRouteID:   "b",
		Routes:           []RoutingRoute{{RouteID: "a", RouteName: "A", Condition: "new", NextStepID: "end"}, {RouteID: "b", RouteName: "B", Condition: "new", NextStepID: "end"}},
		ContextOutput:    "route-output.json",
		ValidationSchema: nil,
	}, &routingChanges); err != nil {
		t.Fatalf("updateSingleStep routing returned error: %v", err)
	}
	for _, want := range []string{"routing_question", "routes", "default_route_id", "context_output"} {
		if !hasPlanFieldChange(routingChanges, want) {
			t.Fatalf("routing field changes missing %q: %#v", want, routingChanges)
		}
	}
}

func hasPlanFieldChange(changes []PlanFieldChange, field string) bool {
	for _, change := range changes {
		if change.Field == field {
			return true
		}
	}
	return false
}
