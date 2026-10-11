package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func routingStepWithTargets(targets ...string) *RoutingPlanStep {
	routes := make([]RoutingRoute, 0, len(targets))
	for index, target := range targets {
		routes = append(routes, RoutingRoute{
			RouteID:    "route-" + string(rune('a'+index)),
			RouteName:  "Route " + string(rune('A'+index)),
			Condition:  "condition",
			NextStepID: target,
		})
	}
	return &RoutingPlanStep{
		Type: StepTypeRouting,
		CommonStepFields: CommonStepFields{
			ID:    "router",
			Title: "Router",
		},
		RoutingQuestion: "Which route?",
		Routes:          routes,
		DefaultRouteID:  "route-a",
	}
}

func TestValidatePlanStructureReportsEveryDanglingReference(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		routingStepWithTargets("missing-a", "missing-b"),
		&HumanInputPlanStep{
			Type: StepTypeHumanInput,
			CommonStepFields: CommonStepFields{
				ID:    "approval",
				Title: "Approval",
			},
			Question:        "Approve?",
			ResponseType:    "yesno",
			NextStepID:      "end",
			IfYesNextStepID: "missing-c",
			IfNoNextStepID:  "end",
		},
		&ScriptedPlanStep{
			Type:             StepTypeScripted,
			CommonStepFields: CommonStepFields{ID: "scripted"},
			NextStepID:       "missing-d",
		},
	}}

	err := ValidatePlanStructure(plan)
	if err == nil {
		t.Fatal("expected graph validation error")
	}
	var validationErr *PlanValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error type = %T, want *PlanValidationError", err)
	}
	for _, want := range []string{
		"PLAN_GRAPH_INVALID: 4 next-step reference(s)",
		`route "route-a".next_step_id in step "router" points to missing step "missing-a"`,
		`route "route-b".next_step_id in step "router" points to missing step "missing-b"`,
		`if_yes_next_step_id in step "approval" points to missing step "missing-c"`,
		`next_step_id in step "scripted" points to missing step "missing-d"`,
		"No changes were saved",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validation error missing %q:\n%s", want, err)
		}
	}
}

func TestDeletePlanStepsRejectsReferencedTargetWithoutWriting(t *testing.T) {
	oldPlan := &PlanningResponse{Steps: []PlanStepInterface{
		routingStepWithTargets("target", "target"),
		regularStep("target"),
	}}
	planJSON, err := json.Marshal(oldPlan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	writes := 0
	readFile := func(context.Context, string) (string, error) {
		return string(planJSON), nil
	}
	writeFile := func(context.Context, string, string) error {
		writes++
		return nil
	}
	executor := createDeletePlanStepsExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile, nil)

	_, err = executor(context.Background(), map[string]interface{}{
		"deleted_step_ids": []interface{}{"target"},
		"reason":           "remove obsolete target",
	})
	if err == nil {
		t.Fatal("expected referenced-step deletion to be rejected")
	}
	for _, want := range []string{"step deletion rejected", "PLAN_GRAPH_INVALID: 2", "route-a", "route-b", "left unchanged"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("delete error missing %q:\n%s", want, err)
		}
	}
	if writes != 0 {
		t.Fatalf("write callback called %d times; rejected deletion must be atomic", writes)
	}
}

// TestDeletePlanStepsFlagsWorkflowLevelDriftReview covers PLAT-258's deletion
// coverage gap: a deleted step's own drift_review record is cascade-removed
// along with its step_config.json entry, so CollectPlanDriftCandidates'
// per-step scan structurally cannot see anything requiring review for it.
// Deletion must instead flag the workflow-level WorkflowDriftReviewStepID
// record needs_review, since that survives the deleted step's own removal.
func TestDeletePlanStepsFlagsWorkflowLevelDriftReview(t *testing.T) {
	oldPlan := &PlanningResponse{Steps: []PlanStepInterface{
		regularStep("obsolete-step"),
		regularStep("keeper"),
	}}
	planJSON, err := json.Marshal(oldPlan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	existingConfig := StepConfigFile{Steps: []StepConfig{
		{ID: "obsolete-step", AgentConfigs: &AgentConfigs{DriftReview: &StepDriftReview{NeedsReview: false, ReviewedAt: "2026-08-01T00:00:00Z"}}},
	}}
	existingConfigJSON, err := json.Marshal(existingConfig)
	if err != nil {
		t.Fatalf("marshal step_config.json fixture: %v", err)
	}
	var writtenConfig string
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return string(existingConfigJSON), nil
		}
		return string(planJSON), nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "step_config.json") {
			writtenConfig = content
		}
		return nil
	}
	executor := createDeletePlanStepsExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile, nil)

	if _, err := executor(context.Background(), map[string]interface{}{
		"deleted_step_ids": []interface{}{"obsolete-step"},
		"reason":           "no longer needed",
	}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if writtenConfig == "" {
		t.Fatal("delete did not write step_config.json")
	}

	var file StepConfigFile
	if err := json.Unmarshal([]byte(writtenConfig), &file); err != nil {
		t.Fatalf("decode written step_config.json: %v", err)
	}
	var workflowRecord *StepConfig
	for i := range file.Steps {
		if file.Steps[i].ID == "obsolete-step" {
			t.Fatalf("deleted step's own step_config.json entry must be cascade-removed, found: %+v", file.Steps[i])
		}
		if file.Steps[i].ID == WorkflowDriftReviewStepID {
			workflowRecord = &file.Steps[i]
		}
	}
	if workflowRecord == nil {
		t.Fatalf("workflow-level drift review record was not created, entries: %+v", file.Steps)
	}
	if workflowRecord.AgentConfigs == nil || workflowRecord.AgentConfigs.DriftReview == nil || !workflowRecord.AgentConfigs.DriftReview.NeedsReview {
		t.Fatalf("workflow-level drift review record not flagged needs_review: %+v", workflowRecord)
	}
}

// TestFlagWorkflowDriftReviewOnDeletionPreservesPriorEvidence covers the
// stale-flag semantics: a second deletion must flag the SAME record's
// needs_review again without discarding whatever evidence a prior
// workflow-level review already recorded — exactly like a real step's
// drift_review record.
// TestDeletePlanStepsRetriesWorkflowLevelFlagOnceOnTransientFailure covers a
// transient step_config.json write failure: delete_plan_steps already commits
// plan.json first (an established, documented point of no return this
// codebase has no transactional multi-file write mechanism to undo), so the
// deletion's own drift-trigger write must retry rather than silently drop the
// only remaining signal plan_drift_review has for this deletion.
func TestDeletePlanStepsRetriesWorkflowLevelFlagOnceOnTransientFailure(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{regularStep("obsolete-step"), regularStep("keeper")}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return `{"steps":[]}`, nil
		}
		return string(planJSON), nil
	}
	writeAttempts := 0
	var writtenConfig string
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "step_config.json") {
			writeAttempts++
			if writeAttempts == 1 {
				return errors.New("transient write failure")
			}
			writtenConfig = content
		}
		return nil
	}
	executor := createDeletePlanStepsExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile, nil)

	result, err := executor(context.Background(), map[string]interface{}{
		"deleted_step_ids": []interface{}{"obsolete-step"},
		"reason":           "no longer needed",
	})
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if writeAttempts != 2 {
		t.Fatalf("write attempted %d time(s), want exactly 2 (one retry)", writeAttempts)
	}
	if strings.Contains(result, "FAILED to flag the workflow-level drift review record") {
		t.Fatalf("a transient failure recovered by the retry must not be surfaced as a failure: %s", result)
	}
	var file StepConfigFile
	if err := json.Unmarshal([]byte(writtenConfig), &file); err != nil {
		t.Fatalf("decode written step_config.json: %v", err)
	}
	found := false
	for _, cfg := range file.Steps {
		if cfg.ID == WorkflowDriftReviewStepID && cfg.AgentConfigs != nil && cfg.AgentConfigs.DriftReview != nil && cfg.AgentConfigs.DriftReview.NeedsReview {
			found = true
		}
	}
	if !found {
		t.Fatalf("workflow-level record was not flagged after the successful retry, entries: %+v", file.Steps)
	}
}

// TestDeletePlanStepsSurfacesLoudWarningWhenWorkflowLevelFlagPersistentlyFails
// covers the remaining case: both attempts fail. delete_plan_steps must still
// report success (the plan mutation itself genuinely succeeded and cannot be
// rolled back), but must make the tracking gap impossible to miss in its own
// response rather than only logging it.
func TestDeletePlanStepsSurfacesLoudWarningWhenWorkflowLevelFlagPersistentlyFails(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{regularStep("obsolete-step"), regularStep("keeper")}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return `{"steps":[]}`, nil
		}
		return string(planJSON), nil
	}
	writeAttempts := 0
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "step_config.json") {
			writeAttempts++
			return errors.New("persistent write failure")
		}
		return nil
	}
	executor := createDeletePlanStepsExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile, nil)

	result, err := executor(context.Background(), map[string]interface{}{
		"deleted_step_ids": []interface{}{"obsolete-step"},
		"reason":           "no longer needed",
	})
	if err != nil {
		t.Fatalf("delete must still report success even when the flag write persistently fails: %v", err)
	}
	if writeAttempts != 2 {
		t.Fatalf("write attempted %d time(s), want exactly 2 (one retry, both failing)", writeAttempts)
	}
	if !strings.Contains(result, "FAILED to flag the workflow-level drift review record") {
		t.Fatalf("a persistent flag-write failure must be surfaced loudly in the tool's own response:\n%s", result)
	}
}

func TestFlagWorkflowDriftReviewOnDeletionPreservesPriorEvidence(t *testing.T) {
	configs := []StepConfig{
		{ID: WorkflowDriftReviewStepID, AgentConfigs: &AgentConfigs{DriftReview: &StepDriftReview{
			NeedsReview: false,
			ReviewedAt:  "2026-08-01T00:00:00Z",
			ReviewedBy:  "pulse:plan_drift_review",
			Checks:      []StepDriftCheck{{CheckID: "deleted_step_dependent_artifact_audit", Status: "pass", Evidence: "no dangling references found for the prior deletion batch"}},
		}}},
	}
	updated := flagWorkflowDriftReviewOnDeletion(configs, []string{"another-obsolete-step"})
	if len(updated) != 1 {
		t.Fatalf("expected the single workflow-level record to be updated in place, got %d entries", len(updated))
	}
	record := updated[0].AgentConfigs.DriftReview
	if !record.NeedsReview {
		t.Fatal("expected NeedsReview to be set true by a second deletion")
	}
	if record.ReviewedAt != "2026-08-01T00:00:00Z" || len(record.Checks) != 1 {
		t.Fatalf("prior evidence was not preserved: %+v", record)
	}
}

// TestCleanupOrphanStepConfigsPreservesWorkflowLevelRecord covers the risk
// that cleanup_orphan_step_configs (which removes any step_config.json entry
// not found in plan.json's live step set) would otherwise treat the
// workflow-level sentinel as orphan garbage and delete the exact signal
// flagWorkflowDriftReviewOnDeletion just set — it is never a "live" plan.json
// step by definition, but it is also never orphan garbage.
func TestCleanupOrphanStepConfigsPreservesWorkflowLevelRecord(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{regularStep("keeper")}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	existingConfig := StepConfigFile{Steps: []StepConfig{
		{ID: "keeper"},
		{ID: "truly-orphaned-step"},
		{ID: WorkflowDriftReviewStepID, AgentConfigs: &AgentConfigs{DriftReview: &StepDriftReview{NeedsReview: true}}},
	}}
	existingConfigJSON, err := json.Marshal(existingConfig)
	if err != nil {
		t.Fatalf("marshal step_config.json fixture: %v", err)
	}
	var writtenConfig string
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return string(existingConfigJSON), nil
		}
		return string(planJSON), nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "step_config.json") {
			writtenConfig = content
		}
		return nil
	}
	executor := createCleanupOrphanStepConfigsExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	result, err := executor(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if !strings.Contains(result, "truly-orphaned-step") {
		t.Fatalf("expected the genuinely orphaned entry to be removed: %s", result)
	}
	if strings.Contains(result, WorkflowDriftReviewStepID) {
		t.Fatalf("workflow-level record must not be reported as removed: %s", result)
	}

	var file StepConfigFile
	if err := json.Unmarshal([]byte(writtenConfig), &file); err != nil {
		t.Fatalf("decode written step_config.json: %v", err)
	}
	found := false
	for _, cfg := range file.Steps {
		if cfg.ID == WorkflowDriftReviewStepID {
			found = true
		}
	}
	if !found {
		t.Fatalf("workflow-level record was removed by cleanup_orphan_step_configs, entries: %+v", file.Steps)
	}
}

func TestUpdateRoutingStepCanRepairPreviouslyDanglingGraph(t *testing.T) {
	brokenPlan := &PlanningResponse{Steps: []PlanStepInterface{
		routingStepWithTargets("already-deleted", "end"),
	}}
	planJSON, err := json.Marshal(brokenPlan)
	if err != nil {
		t.Fatalf("marshal broken plan: %v", err)
	}
	var writtenPlan string
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "step_config.json") {
			return "[]", nil
		}
		if strings.Contains(path, "evaluation/") {
			return "", errors.New("not found")
		}
		return string(planJSON), nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "plan.json") {
			writtenPlan = content
		}
		return nil
	}
	executor := createUpdateRoutingStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	result, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "router",
		"routes": []interface{}{
			map[string]interface{}{"route_id": "route-a", "route_name": "Route A", "condition": "condition", "next_step_id": "end"},
			map[string]interface{}{"route_id": "route-b", "route_name": "Route B", "condition": "condition", "next_step_id": "end"},
		},
		"reason": "repair route left dangling by an older deletion",
	})
	if err != nil {
		t.Fatalf("repair update failed: %v", err)
	}
	if !strings.Contains(result, "Successfully updated routing step") {
		t.Fatalf("unexpected update result: %s", result)
	}
	if writtenPlan == "" {
		t.Fatal("repair did not persist the now-valid plan")
	}
	var repaired PlanningResponse
	if err := json.Unmarshal([]byte(writtenPlan), &repaired); err != nil {
		t.Fatalf("decode repaired plan: %v", err)
	}
	if err := ValidatePlanStructure(&repaired); err != nil {
		t.Fatalf("persisted repair is invalid: %v", err)
	}
}
