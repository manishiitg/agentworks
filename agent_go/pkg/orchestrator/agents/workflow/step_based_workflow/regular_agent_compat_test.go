package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestScriptedToolNamesArePlanMutationsAndLegacyNamesAreNot(t *testing.T) {
	for _, name := range []string{"add_scripted_step", "update_scripted_step"} {
		if !IsPlanModificationTool(name) {
			t.Fatalf("expected %q to be recognized as a plan mutation", name)
		}
	}
	for _, name := range []string{"add_regular_step", "update_regular_step"} {
		if IsPlanModificationTool(name) {
			t.Fatalf("legacy authoring tool %q must not remain callable", name)
		}
	}
}

func TestScriptedToolSchemasExposeNextStepID(t *testing.T) {
	for name, schema := range map[string]string{
		"add_scripted_step":    getAddRegularStepSchema(),
		"update_scripted_step": getUpdateRegularStepSchema(),
	} {
		var decoded struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal([]byte(schema), &decoded); err != nil {
			t.Fatalf("%s schema is invalid JSON: %v", name, err)
		}
		if _, ok := decoded.Properties["next_step_id"]; !ok {
			t.Fatalf("%s schema does not expose next_step_id", name)
		}
	}
}

func TestScriptedStepNextStepIDRoundTripAndUpdate(t *testing.T) {
	original := &RegularPlanStep{
		Type:             StepTypeRegular,
		CommonStepFields: CommonStepFields{ID: "script-a"},
		NextStepID:       "script-b",
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RegularPlanStep
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.NextStepID != "script-b" {
		t.Fatalf("round-trip next_step_id = %q", decoded.NextStepID)
	}

	updated := mergePartialStepUpdate(original, PartialPlanStep{NextStepID: "shared"}).(*RegularPlanStep)
	if updated.NextStepID != "shared" {
		t.Fatalf("updated next_step_id = %q, want shared", updated.NextStepID)
	}
}

func TestPrepareScriptedStepUpdateTarget(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "scripted"}},
		&RegularPlanStep{CommonStepFields: CommonStepFields{ID: "legacy-agentic"}},
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "sequence"}},
	}}
	configs := []StepConfig{
		{ID: "scripted", AgentConfigs: &AgentConfigs{}},
		{ID: "legacy-agentic", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic}},
	}

	if downgraded, err := prepareScriptedStepUpdateTarget(plan, configs, "scripted"); err != nil {
		t.Fatalf("declared scripted step was rejected: %v", err)
	} else if downgraded {
		t.Fatal("an already-regular step must not be reported as downgraded")
	}
	if _, err := prepareScriptedStepUpdateTarget(plan, configs, "legacy-agentic"); err == nil {
		t.Fatal("legacy agentic regular step must not be editable through update_scripted_step")
	} else if !strings.Contains(err.Error(), "update_agent_step") {
		t.Fatalf("legacy rejection must identify the working compatibility path: %v", err)
	}
	if _, err := prepareScriptedStepUpdateTarget(plan, configs, "sequence"); err == nil {
		t.Fatal("a genuine (non-scripted) agent step must use its type-specific update tool")
	}
}

func TestPrepareAgentUpdateTargetUpgradesLegacyAgenticRegular(t *testing.T) {
	legacy := &RegularPlanStep{
		Type: StepTypeRegular,
		CommonStepFields: CommonStepFields{
			ID:          "legacy-agentic",
			Title:       "Collect latency",
			Description: "Collect the latency evidence.",
		},
		NextStepID: "end",
	}
	plan := &PlanningResponse{Steps: []PlanStepInterface{legacy}}
	configs := []StepConfig{{
		ID:           legacy.ID,
		AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic},
	}}

	upgraded, err := prepareAgentUpdateTarget(plan, configs, legacy.ID)
	if err != nil {
		t.Fatalf("legacy compatibility upgrade failed: %v", err)
	}
	if !upgraded {
		t.Fatal("expected legacy agentic regular step to be upgraded")
	}
	sequence, ok := plan.Steps[0].(*AgentPlanStep)
	if !ok {
		t.Fatalf("upgraded step type = %T, want *AgentPlanStep", plan.Steps[0])
	}
	if sequence.ID != legacy.ID || sequence.Description != legacy.Description || sequence.NextStepID != "end" {
		t.Fatalf("compatibility upgrade lost existing fields: %#v", sequence)
	}
	if len(sequence.Items) != 1 || sequence.Items[0].ID != normalizedRegularSequenceItemID {
		t.Fatalf("compatibility upgrade did not preserve effective runtime queue: %#v", sequence.Items)
	}
}

func TestPrepareScriptedStepUpdateTargetNeverConvertsASequence(t *testing.T) {
	// Before PLAT-287 a agent whose step_config declared scripted
	// was silently downgraded to regular here. The plan type is now the only
	// source of truth, so the conversion is change_step_type's job and this
	// path refuses instead.
	sequence := &AgentPlanStep{
		Type: StepTypeAgent,
		CommonStepFields: CommonStepFields{
			ID:          "search-save-jobs",
			Title:       "Record shortlisted jobs to db",
			Description: "Execute the checked-in deterministic implementation in learnings/search-save-jobs/main.py for this run.",
		},
		NextStepID: "end",
		Items: []AgentItem{{
			ID:      "verify-scripted-result",
			Type:    "user_message",
			Message: "Run the checked-in script and verify its output.",
		}},
	}
	plan := &PlanningResponse{Steps: []PlanStepInterface{sequence}}
	configs := []StepConfig{{
		ID:           sequence.ID,
		AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted},
	}}

	downgraded, err := prepareScriptedStepUpdateTarget(plan, configs, sequence.ID)
	if err == nil || !strings.Contains(err.Error(), "change_step_type") {
		t.Fatalf("a agent must be refused with change_step_type guidance, got err=%v", err)
	}
	if downgraded {
		t.Fatal("a agent must never be converted by update_scripted_step")
	}
	if _, ok := plan.Steps[0].(*AgentPlanStep); !ok {
		t.Fatalf("refused step was mutated to %T", plan.Steps[0])
	}
}

func TestPrepareScriptedStepUpdateTargetRejectsNonScriptedSequence(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{CommonStepFields: CommonStepFields{ID: "sequence"}},
	}}

	downgraded, err := prepareScriptedStepUpdateTarget(plan, nil, "sequence")
	if err == nil || !strings.Contains(err.Error(), "update_agent_step") {
		t.Fatalf("non-scripted sequence rejection = %v, want update_agent_step guidance", err)
	}
	if downgraded {
		t.Fatal("a genuine agent step must never be converted")
	}
	if _, ok := plan.Steps[0].(*AgentPlanStep); !ok {
		t.Fatalf("rejected agent step was mutated to %T", plan.Steps[0])
	}
}

func TestPrepareAgentUpdateTargetRejectsScriptedRegular(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{
			Type:             StepTypeRegular,
			CommonStepFields: CommonStepFields{ID: "scripted"},
		},
	}}
	// No retired key at all: a regular step is scripted by type (PLAT-287).
	configs := []StepConfig{{ID: "scripted", AgentConfigs: &AgentConfigs{}}}

	upgraded, err := prepareAgentUpdateTarget(plan, configs, "scripted")
	if err == nil || !strings.Contains(err.Error(), "update_scripted_step") {
		t.Fatalf("scripted regular rejection = %v, want update_scripted_step guidance", err)
	}
	if upgraded {
		t.Fatal("declared scripted step must never be converted")
	}
	if _, ok := plan.Steps[0].(*RegularPlanStep); !ok {
		t.Fatalf("rejected scripted step was mutated to %T", plan.Steps[0])
	}
}

func TestUpdateAgentExecutorAtomicallyPersistsLegacyUpgrade(t *testing.T) {
	legacyPlan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{
			Type: StepTypeRegular,
			CommonStepFields: CommonStepFields{
				ID:                  "voice-latency-collector",
				Title:               "Voice latency collector",
				Description:         "Collect voice latency.",
				ContextDependencies: []string{"input.json"},
				ContextOutput:       FlexibleContextOutput("latency.json"),
			},
			NextStepID: "end",
		},
	}}
	planJSON, err := json.Marshal(legacyPlan)
	if err != nil {
		t.Fatalf("marshal legacy plan: %v", err)
	}
	stepConfigJSON := `{"steps":[{"id":"voice-latency-collector","agent_configs":{"declared_execution_mode":"agentic"}}]}`
	var writtenPlan string
	readFile := func(_ context.Context, path string) (string, error) {
		switch {
		case strings.HasSuffix(path, "planning/plan.json"):
			return string(planJSON), nil
		case strings.HasSuffix(path, "planning/step_config.json"):
			return stepConfigJSON, nil
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
	executor := createUpdateAgentStepExecutor("workflow", loggerv2.NewNoop(), readFile, writeFile)

	result, err := executor(context.Background(), map[string]interface{}{
		"existing_step_id": "voice-latency-collector",
		"description":      "Collect voice latency and verify per-language sample coverage.",
		"items": []interface{}{
			map[string]interface{}{
				"id":      "verify-language-coverage",
				"type":    "user_message",
				"message": "Verify every supported language has non-zero samples and repair missing evidence.",
			},
		},
		"reason": "repair the recurring missing per-language measurement evidence",
	})
	if err != nil {
		t.Fatalf("legacy agent update failed: %v", err)
	}
	if !strings.Contains(result, "upgraded its saved legacy regular type to agent") {
		t.Fatalf("update result did not disclose compatibility upgrade: %s", result)
	}
	if writtenPlan == "" {
		t.Fatal("updated plan was not persisted")
	}

	var persisted PlanningResponse
	if err := json.Unmarshal([]byte(writtenPlan), &persisted); err != nil {
		t.Fatalf("decode persisted plan: %v", err)
	}
	sequence, ok := persisted.Steps[0].(*AgentPlanStep)
	if !ok {
		t.Fatalf("persisted step type = %T, want *AgentPlanStep", persisted.Steps[0])
	}
	if sequence.Description != "Collect voice latency and verify per-language sample coverage." {
		t.Fatalf("description was not updated: %q", sequence.Description)
	}
	if len(sequence.Items) != 1 || sequence.Items[0].ID != "verify-language-coverage" {
		t.Fatalf("items were not updated: %#v", sequence.Items)
	}
	if sequence.ContextOutput.String() != "latency.json" || sequence.NextStepID != "end" {
		t.Fatalf("legacy fields were not preserved: %#v", sequence)
	}
}

func TestNonScriptedRegularStepNormalizesToAgent(t *testing.T) {
	validation := &ValidationSchema{}
	config := &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic}
	regular := &RegularPlanStep{
		Type: StepTypeRegular,
		CommonStepFields: CommonStepFields{
			ID:                  "analyze-results",
			Title:               "Analyze results",
			Description:         "Analyze the saved evidence and explain the result.",
			ContextDependencies: []string{"fetch.json"},
			ContextOutput:       FlexibleContextOutput("analysis.json"),
			ValidationSchema:    validation,
		},
		NextStepID:   "shared",
		AgentConfigs: config,
	}

	if !shouldNormalizeRegularStepToAgent(regular) {
		t.Fatal("expected a non-scripted regular step to use agent normalization")
	}
	sequence := normalizeRegularStepToAgent(regular)
	if sequence == nil || sequence.StepType() != StepTypeAgent {
		t.Fatalf("expected agent normalization, got %#v", sequence)
	}
	if sequence.ID != regular.ID || sequence.Title != regular.Title || sequence.Description != regular.Description {
		t.Fatalf("normalization changed step identity or instructions: %#v", sequence)
	}
	if sequence.ContextOutput != regular.ContextOutput || sequence.ValidationSchema != validation || sequence.AgentConfigs != config {
		t.Fatal("normalization did not preserve output, validation, or agent configuration")
	}
	if sequence.NextStepID != "shared" {
		t.Fatalf("normalization lost next_step_id: %q", sequence.NextStepID)
	}
	if len(sequence.Items) != 1 || sequence.Items[0].ID != normalizedRegularSequenceItemID || sequence.Items[0].Type != "user_message" {
		t.Fatalf("expected one normalized work turn, got %#v", sequence.Items)
	}
	if got := effectiveRuntimeStepType(regular); got != string(StepTypeAgent) {
		t.Fatalf("effective runtime step type = %q, want %q", got, StepTypeAgent)
	}
}

func TestScriptedRegularStepDoesNotNormalizeToAgent(t *testing.T) {
	regular := &RegularPlanStep{
		CommonStepFields: CommonStepFields{ID: "fetch-data"},
		AgentConfigs:     &AgentConfigs{},
	}
	if shouldNormalizeRegularStepToAgent(regular) {
		t.Fatal("scripted regular step must retain the saved-script execution path")
	}
	if got := effectiveRuntimeStepType(regular); got != string(StepTypeRegular) {
		t.Fatalf("effective runtime step type = %q, want %q", got, StepTypeRegular)
	}
}

func TestUpsertNewScriptedRegularStepConfig(t *testing.T) {
	configs := []StepConfig{{ID: "fetch-data", AgentConfigs: nil}}
	configs = upsertNewScriptedRegularStepConfig(configs, "fetch-data", "Fetch data")
	if len(configs) != 1 {
		t.Fatalf("expected existing config to be updated, got %d entries", len(configs))
	}
	cfg := configs[0]
	if cfg.Title != "Fetch data" || cfg.AgentConfigs == nil {
		t.Fatalf("missing updated scripted config: %#v", cfg)
	}
	if cfg.AgentConfigs.LegacyDeclaredExecutionMode != "" {
		t.Fatalf("a new scripted step must not write the retired declared_execution_mode, got %q", cfg.AgentConfigs.LegacyDeclaredExecutionMode)
	}
	if cfg.AgentConfigs.UseCodeExecutionMode == nil || !*cfg.AgentConfigs.UseCodeExecutionMode {
		t.Fatal("scripted mode did not enable code execution")
	}

	configs = upsertNewScriptedRegularStepConfig(configs, "normalize-data", "Normalize data")
	if len(configs) != 2 || configs[1].ID != "normalize-data" || configs[1].AgentConfigs == nil {
		t.Fatalf("expected a new scripted config entry, got %#v", configs)
	}
}

func TestCollectRegularPlanStepsIncludesNestedTodoRoutes(t *testing.T) {
	regular := &RegularPlanStep{CommonStepFields: CommonStepFields{ID: "fetch-data", Title: "Fetch data"}}
	sequence := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "analyze-data"}}
	nested := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "nested"},
		PredefinedRoutes: []PlanOrchestrationRoute{
			{RouteID: "fetch-data", SubAgentStep: regular},
			{RouteID: "analyze-data", SubAgentStep: sequence},
		},
	}
	root := &AgentPlanStep{
		CommonStepFields: CommonStepFields{ID: "root"},
		PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "nested", SubAgentStep: nested}},
	}

	got := collectRegularPlanSteps(root)
	if len(got) != 1 || got[0] != regular {
		t.Fatalf("expected only the nested regular boundary, got %#v", got)
	}
}

func TestAgentRejectsIncompleteAgentRoute(t *testing.T) {
	step := &AgentPlanStep{
		CommonStepFields: CommonStepFields{
			ID:          "orchestrate",
			Title:       "Orchestrate",
			Description: "Delegate work.",
		},
		Items:      []AgentItem{{ID: "delegate", Type: "user_message", Message: "Delegate work."}},
		NextStepID: "end",
		PredefinedRoutes: []PlanOrchestrationRoute{{
			RouteID:   "analyze",
			RouteName: "Analyze",
			SubAgentStep: &AgentPlanStep{CommonStepFields: CommonStepFields{
				ID:          "analyze",
				Title:       "Analyze",
				Description: "Analyze the evidence.",
			}},
		}},
	}

	if err := validateAgentStepFieldsTyped(step); err == nil {
		t.Fatal("expected an empty agent route to fail validation")
	}
	step.PredefinedRoutes[0].SubAgentStep.(*AgentPlanStep).Items = []AgentItem{{
		ID:      "analyze",
		Type:    "user_message",
		Message: "Analyze the evidence and save the result.",
	}}
	if err := validateAgentStepFieldsTyped(step); err != nil {
		t.Fatalf("expected a complete agent route to pass validation: %v", err)
	}
}
