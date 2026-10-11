package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestPlanMedianOtherStepDescriptionLen(t *testing.T) {
	steps := []PlanStepInterface{
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "a", Description: strings.Repeat("x", 100)}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "b", Description: strings.Repeat("x", 200)}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "c", Description: strings.Repeat("x", 300)}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "target", Description: strings.Repeat("x", 99999)}},
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "no-description"}},
	}
	if got := planMedianOtherStepDescriptionLen(steps, "target"); got != 200 {
		t.Fatalf("median = %d, want 200 (target excluded, no-description skipped)", got)
	}
	if got := planMedianOtherStepDescriptionLen(nil, "target"); got != 0 {
		t.Fatalf("median of empty plan = %d, want 0", got)
	}
}

func TestStepDescriptionSizeNudgeThresholds(t *testing.T) {
	// Small plan, small step: no nudge.
	if got := stepDescriptionSizeNudge(500, 600, 500); got != "" {
		t.Fatalf("small step in small plan produced a nudge: %q", got)
	}
	// Relative: well above 2.5x this plan's own median, plan median above the floor.
	if got := stepDescriptionSizeNudge(4335, 22784, 4335); got == "" {
		t.Fatal("5.3x-median step produced no nudge")
	}
	// Absolute floor: even with a tiny plan median, a small absolute size and
	// modest single-edit growth (below descriptionSizeGrowthThreshold) stay silent.
	if got := stepDescriptionSizeNudge(1200, 1800, 731); got != "" {
		t.Fatalf("small absolute size in a tiny-median plan produced a nudge: %q", got)
	}
	// Single-edit growth alone should fire even without exceeding the relative/absolute check.
	if got := stepDescriptionSizeNudge(3000, 4200, 4000); got == "" {
		t.Fatal("a 1200-char single-edit growth produced no nudge")
	}
	// The nudge text names the plan-relative baseline, not a hardcoded number.
	if got := stepDescriptionSizeNudge(4335, 22784, 4335); !strings.Contains(got, "median of 4335") {
		t.Fatalf("nudge text does not surface the plan median: %q", got)
	}
}

func TestAddAgentStepIncludesSizeNudgeRelativeToSiblings(t *testing.T) {
	existingPlan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{
			Type:             StepTypeAgent,
			CommonStepFields: CommonStepFields{ID: "sibling-a", Title: "Sibling A", Description: strings.Repeat("normal ", 100), ContextDependencies: []string{}},
			Items:            []AgentItem{{ID: "verify", Type: "user_message", Message: "Verify the result."}},
		},
	}}
	planJSON, err := json.Marshal(existingPlan)
	if err != nil {
		t.Fatal(err)
	}
	var writtenPlan string
	readFile := func(_ context.Context, path string) (string, error) {
		switch {
		case strings.HasSuffix(path, "planning/plan.json"):
			return string(planJSON), nil
		case strings.HasSuffix(path, "planning/step_config.json"):
			return `{"steps":[]}`, nil
		default:
			return "", errors.New("not found")
		}
	}
	writeFile := func(_ context.Context, path, content string) error {
		if strings.HasSuffix(path, "planning/plan.json") {
			writtenPlan = content
		}
		return nil
	}
	add := createAddAgentStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile, nil)

	bigDescription := strings.Repeat("this is durable technique restated inline instead of in a skill. ", 300)
	result, err := add(context.Background(), map[string]interface{}{
		"id":                   "new-step",
		"title":                "New step",
		"description":          bigDescription,
		"context_dependencies": []interface{}{},
		"items": []interface{}{
			map[string]interface{}{"id": "verify", "type": "user_message", "message": "Verify the result."},
		},
		"insert_after_step_id": "sibling-a",
		"reason":               "test",
	})
	if err != nil {
		t.Fatalf("add_agent_step failed: %v", err)
	}
	if !strings.Contains(result, "Description should stay WHAT to achieve") {
		t.Fatalf("add response missing size nudge for an oversized new step: %s", result)
	}
	if writtenPlan == "" {
		t.Fatal("plan was not persisted")
	}
}

func TestUpdateOrchestratorStepIncludesSizeNudge(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{
			Type:             StepTypeAgent,
			CommonStepFields: CommonStepFields{ID: "sibling-a", Title: "Sibling A", Description: strings.Repeat("normal ", 100), ContextDependencies: []string{}},
			Items:            []AgentItem{{ID: "verify", Type: "user_message", Message: "Verify the result."}},
		},
		&AgentPlanStep{
			Type: StepTypeAgent,
			CommonStepFields: CommonStepFields{
				ID: "target-orchestrator", Title: "Target orchestrator",
				Description: "Investigate the failure.", ContextDependencies: []string{},
			},
			NextStepID: "end",
		},
	}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		if strings.HasSuffix(path, "planning/plan.json") {
			return string(planJSON), nil
		}
		return "", errors.New("not found")
	}
	writeFile := func(_ context.Context, _, _ string) error { return nil }
	update := createUpdateAgentStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	bigDescription := strings.Repeat("this is durable technique restated inline instead of in a skill. ", 300)
	result, err := update(context.Background(), map[string]interface{}{
		"existing_step_id": "target-orchestrator",
		"description":      bigDescription,
		"next_step_id":     "end",
		"reason":           "test",
	})
	if err != nil {
		t.Fatalf("update_orchestrator_step failed: %v", err)
	}
	if !strings.Contains(result, "Description should stay WHAT to achieve") {
		t.Fatalf("orchestrator update response missing size nudge for an oversized description: %s", result)
	}
}

func TestUpdateAgentStepOmitsNudgeWhenDescriptionUntouched(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{
			Type: StepTypeAgent,
			CommonStepFields: CommonStepFields{
				ID: "target", Title: "Target", Description: strings.Repeat("x", 50000),
				ContextDependencies: []string{},
			},
			Items: []AgentItem{{ID: "verify", Type: "user_message", Message: "Verify the result."}},
		},
	}}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		switch {
		case strings.HasSuffix(path, "planning/plan.json"):
			return string(planJSON), nil
		case strings.HasSuffix(path, "planning/step_config.json"):
			return `{"steps":[]}`, nil
		default:
			return "", errors.New("not found")
		}
	}
	writeFile := func(_ context.Context, _, _ string) error { return nil }
	update := createUpdateAgentStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	// Update a field other than description: the already-huge description
	// must not trigger a nudge for an edit that never touched it.
	result, err := update(context.Background(), map[string]interface{}{
		"existing_step_id": "target",
		"context_output":   "new_output.json",
		"reason":           "test",
	})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if strings.Contains(result, "Description should stay WHAT to achieve") {
		t.Fatalf("update response included a size nudge for an edit that never touched description: %s", result)
	}
}

// Far above the plan's normal size the nudge asks for the restructure now, naming the layout and where content goes.
func TestStepDescriptionSizeNudgeOverBudgetAsksForTheMove(t *testing.T) {
	got := stepDescriptionSizeNudge(21191, 21208, 5662)
	for _, want := range []string{"OVER BUDGET", "median of 5662", "Goal, Inputs, Output, Rules, Done when", "knowledgebase note", "Description should stay WHAT to achieve"} {
		if !strings.Contains(got, want) {
			t.Fatalf("over-budget nudge is missing %q: %q", want, got)
		}
	}
	if mild := stepDescriptionSizeNudge(3000, 4200, 4000); strings.Contains(mild, "OVER BUDGET") || !strings.Contains(mild, "knowledgebase note") {
		t.Fatalf("a growth-only nudge must be the normal one with the layout: %q", mild)
	}
}

// The ~700-character compatibility paragraph is sent once per workflow editing session, then a pointer.
func TestPlanEditImpactGuidanceIsSentOncePerSession(t *testing.T) {
	notice := "Plan change recorded.\n- " + planEditImpactGuidance
	now := time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)
	ws := "Workflow/compaction-test-" + t.Name()
	if got := compactRepeatedPlanEditImpactGuidance(ws, notice, now); !strings.Contains(got, planEditImpactGuidance) {
		t.Fatal("the first edit must carry the full paragraph")
	}
	got := compactRepeatedPlanEditImpactGuidance(ws, notice, now.Add(5*time.Minute))
	if strings.Contains(got, planEditImpactGuidance) || !strings.Contains(got, "plan-change-impact.md") {
		t.Fatalf("a repeat within the session must carry the pointer only: %q", got)
	}
	if got := compactRepeatedPlanEditImpactGuidance(ws, notice, now.Add(5*time.Minute+31*time.Minute)); !strings.Contains(got, planEditImpactGuidance) {
		t.Fatal("after 30 idle minutes the full paragraph returns")
	}
	if got := compactRepeatedPlanEditImpactGuidance("", notice, now); !strings.Contains(got, planEditImpactGuidance) {
		t.Fatal("without a workflow path the full paragraph is kept")
	}
}
