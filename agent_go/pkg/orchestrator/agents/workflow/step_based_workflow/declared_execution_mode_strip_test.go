package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func runStripDeclaredMode(t *testing.T, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (string, error) {
	t.Helper()
	exec := createStripDeclaredExecutionModeExecutor(changeStepTypeTestWorkspace, loggerv2.NewNoop(), readFile, writeFile)
	return exec(context.Background(), map[string]interface{}{})
}

func TestStripDeclaredExecutionModeRemovesTheKeysAndKeepsReasonsInTheChangelog(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "scripted", Title: "Scripted", Description: "Deterministic."}},
		testSequenceStep("talk", "Talk"),
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{
		{ID: "scripted", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted, LegacyDeclaredExecutionModeReason: "pure API call", ExecutionTier: "low"}},
		{ID: "talk", AgentConfigs: &AgentConfigs{ExecutionTier: "high"}},
	})
	configPath := normalizePathForWorkspaceAPI("planning/step_config.json", changeStepTypeTestWorkspace)
	if !strings.Contains(files[configPath], "declared_execution_mode") {
		t.Fatal("fixture must start with the retired key on disk")
	}

	out, err := runStripDeclaredMode(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("strip failed: %v", err)
	}
	if !strings.Contains(out, `"status":"migrated"`) || !strings.Contains(out, `"planning_stripped":1`) {
		t.Fatalf("expected one planning entry stripped, got %s", out)
	}
	if strings.Contains(files[configPath], "declared_execution_mode") {
		t.Fatalf("the retired key must be gone from step_config.json, got %s", files[configPath])
	}
	_, configs := readTestPlanAndConfigs(t, readFile)
	if cfg := MatchStepConfigByID("scripted", configs); cfg == nil || cfg.ExecutionTier != "low" || cfg.LegacyDeclaredExecutionMode != "" {
		t.Fatalf("other fields must survive and the key must be cleared, got %+v", cfg)
	}
	if cfg := MatchStepConfigByID("talk", configs); cfg == nil || cfg.ExecutionTier != "high" {
		t.Fatalf("an entry without the key must be untouched, got %+v", cfg)
	}

	entry := findChangelogEntry(t, changeStepTypeTestWorkspace, files, "strip_declared_execution_mode")
	sawMode, sawReason := false, false
	for _, change := range entry.Changes {
		if change.StepID == "scripted" && change.Field == "planning.declared_execution_mode" && change.OldValue == StepModeScripted && change.NewValue == "" {
			sawMode = true
		}
		if change.StepID == "scripted" && change.Field == "planning.declared_execution_mode_reason" && change.OldValue == "pure API call" {
			sawReason = true
		}
	}
	if !sawMode || !sawReason {
		t.Fatalf("changelog must preserve the removed mode and its reason, got %+v", entry.Changes)
	}

	// Idempotent.
	out, err = runStripDeclaredMode(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if !strings.Contains(out, `"status":"no_op"`) {
		t.Fatalf("second run must be a no-op, got %s", out)
	}
}

func TestStripDeclaredExecutionModeRefusesWhileALegacyAgenticScriptedStepRemains(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "legacy", Title: "Legacy", Description: "Ran as a sequence."}},
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{
		{ID: "legacy", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic, LegacyDeclaredExecutionModeReason: "needs judgment"}},
	})
	configPath := normalizePathForWorkspaceAPI("planning/step_config.json", changeStepTypeTestWorkspace)
	before := files[configPath]

	_, err := runStripDeclaredMode(t, readFile, writeFile)
	if err == nil || !strings.Contains(err.Error(), "legacy") || !strings.Contains(err.Error(), "migrate_declared_execution_mode") {
		t.Fatalf("expected a refusal naming the step and the v1.0.38 migration, got err=%v", err)
	}
	if files[configPath] != before {
		t.Fatal("a refusal must leave step_config.json untouched")
	}
}

func TestStripDeclaredExecutionModeLeavesRetiredEvaluationFilesUntouched(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&ScriptedPlanStep{Type: StepTypeScripted, CommonStepFields: CommonStepFields{ID: "scripted", Title: "Scripted", Description: "Deterministic."}},
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{
		{ID: "scripted", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted}},
	})
	evalPlanPath := strings.Trim(changeStepTypeTestWorkspace, "/") + "/evaluation/evaluation_plan.json"
	evalConfigPath := normalizePathForWorkspaceAPI("evaluation/step_config.json", changeStepTypeTestWorkspace)
	files[evalPlanPath] = `{"steps":[{"id":"eval-legacy","title":"Legacy eval"}]}`
	evalConfigJSON, _ := json.Marshal(StepConfigFile{Steps: []StepConfig{
		{ID: "eval-legacy", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted, LegacyDeclaredExecutionModeReason: "retired"}},
	}})
	files[evalConfigPath] = string(evalConfigJSON)

	out, err := runStripDeclaredMode(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("strip failed: %v", err)
	}
	if !strings.Contains(out, `"status":"migrated"`) {
		t.Fatalf("planning entries must still strip, got %s", out)
	}
	if strings.Contains(out, "evaluation_stripped") || strings.Contains(out, "evaluation_marked_scripted") {
		t.Fatalf("output must not mention evaluation stripping, got %s", out)
	}
	if files[evalConfigPath] != string(evalConfigJSON) {
		t.Fatalf("evaluation/step_config.json must be byte-identical, got %s", files[evalConfigPath])
	}
	if files[evalPlanPath] != `{"steps":[{"id":"eval-legacy","title":"Legacy eval"}]}` {
		t.Fatalf("evaluation/evaluation_plan.json must be byte-identical, got %s", files[evalPlanPath])
	}
}

func TestStripDeclaredExecutionModeIsAPlanModificationTool(t *testing.T) {
	if !IsPlanModificationTool("strip_declared_execution_mode") {
		t.Fatal("strip_declared_execution_mode must count as a plan mutation")
	}
}
