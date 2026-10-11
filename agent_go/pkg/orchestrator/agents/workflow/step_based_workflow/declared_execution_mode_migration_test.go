package step_based_workflow

import (
	"context"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func runDeclaredModeMigration(t *testing.T, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error) (string, error) {
	t.Helper()
	exec := createMigrateDeclaredExecutionModeExecutor(changeStepTypeTestWorkspace, loggerv2.NewNoop(), readFile, writeFile)
	return exec(context.Background(), map[string]interface{}{})
}

func TestMigrateDeclaredExecutionModeMakesEveryPlanTypeExplicitAndLeavesConfigAlone(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		// Legacy agentic regular: no declared scripted mode -> the runtime ran it as a sequence; becomes one.
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "legacy", Title: "Legacy", Description: "Legacy agentic work."}, NextStepID: "scripted"},
		// True scripted step with its script: untouched.
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "scripted", Title: "Scripted", Description: "Deterministic."}},
		// Plain sequence: untouched.
		testSequenceStep("talk", "Talk"),
	}}
	configs := []StepConfig{
		{ID: "scripted", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted, LegacyDeclaredExecutionModeReason: "pure API call", ExecutionTier: "low"}},
		{ID: "legacy", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: "agentic", LegacyDeclaredExecutionModeReason: "needs judgment"}},
	}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, configs)
	files[normalizePathForWorkspaceAPI("learnings/scripted/main.py", changeStepTypeTestWorkspace)] = "print('ok')"
	configPath := normalizePathForWorkspaceAPI("planning/step_config.json", changeStepTypeTestWorkspace)
	configBefore := files[configPath]

	out, err := runDeclaredModeMigration(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	if !strings.Contains(out, `"status":"migrated"`) {
		t.Fatalf("expected a migrated status, got %s", out)
	}

	updated, updatedConfigs := readTestPlanAndConfigs(t, readFile)
	legacy, _, _ := findStepByID(updated.Steps, "legacy")
	seq, ok := legacy.(*AgentPlanStep)
	if !ok || seq.NextStepID != "scripted" || seq.Description != "Legacy agentic work." || len(seq.Items) != 1 {
		t.Fatalf("legacy agentic regular step must become an explicit sequence with its fields kept, got %T %+v", legacy, legacy)
	}
	if scripted, _, _ := findStepByID(updated.Steps, "scripted"); scripted.StepType() != StepTypeRegular {
		t.Fatalf("a true scripted step must stay regular, got %s", scripted.StepType())
	}
	if talk, _, _ := findStepByID(updated.Steps, "talk"); talk.StepType() != StepTypeAgent {
		t.Fatalf("a plain sequence must be untouched, got %s", talk.StepType())
	}
	// Half 1 only rewrites the plan; the retired key is removed by the v1.0.39
	// strip migration once the runtime no longer needs it as a shim.
	// step_config.json must be byte-for-byte untouched.
	if files[configPath] != configBefore {
		t.Fatal("half 1 must not write step_config.json")
	}
	if cfg := MatchStepConfigByID("scripted", updatedConfigs); cfg == nil || cfg.LegacyDeclaredExecutionMode != StepModeScripted || cfg.LegacyDeclaredExecutionModeReason != "pure API call" {
		t.Fatalf("the scripted step's declaration must survive, got %+v", cfg)
	}

	entry := findChangelogEntry(t, changeStepTypeTestWorkspace, files, "migrate_declared_execution_mode")
	sawType := false
	for _, change := range entry.Changes {
		if change.StepID == "legacy" && change.Field == "type" && change.NewValue == string(StepTypeAgent) {
			sawType = true
		}
	}
	if !sawType {
		t.Fatalf("changelog must record the type change, got %+v", entry.Changes)
	}
	if len(entry.DeletedSteps) != 1 || len(entry.AddedSteps) != 1 {
		t.Fatalf("changelog must carry the old/new step JSON for the one type change, got deleted=%d added=%d", len(entry.DeletedSteps), len(entry.AddedSteps))
	}

	// Idempotent: the converted plan has nothing left to convert.
	out, err = runDeclaredModeMigration(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	if !strings.Contains(out, `"status":"no_op"`) {
		t.Fatalf("second run must be a no-op, got %s", out)
	}
	if again, _, _ := findStepByID(updated.Steps, "scripted"); again.StepType() != StepTypeRegular {
		t.Fatal("a second run must not touch the scripted step")
	}
}

func TestMigrateDeclaredExecutionModeRepairsADeclaredScriptedSequence(t *testing.T) {
	seq := testSequenceStep("drifted", "Drifted")
	plan := &PlanningResponse{Steps: []PlanStepInterface{seq}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "drifted", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted}}})
	files[normalizePathForWorkspaceAPI("learnings/drifted/main.py", changeStepTypeTestWorkspace)] = "print('ok')"

	if _, err := runDeclaredModeMigration(t, readFile, writeFile); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	updated, configs := readTestPlanAndConfigs(t, readFile)
	if step, _, _ := findStepByID(updated.Steps, "drifted"); step.StepType() != StepTypeRegular {
		t.Fatalf("a declared-scripted sequence must become regular (PLAT-280 drift), got %s", step.StepType())
	}
	if cfg := MatchStepConfigByID("drifted", configs); cfg == nil || cfg.LegacyDeclaredExecutionMode != StepModeScripted {
		t.Fatalf("its scripted declaration must survive so the runtime keeps running main.py, got %+v", cfg)
	}
}

func TestMigrateDeclaredExecutionModeRefusesADeclaredScriptedStepWithoutAScript(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "legacy", Title: "Legacy", Description: "Would be converted."}},
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "no-script", Title: "No script", Description: "Declared scripted, no main.py."}},
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "no-script", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted}}})
	planBefore := files[normalizePathForWorkspaceAPI("planning/plan.json", changeStepTypeTestWorkspace)]

	_, err := runDeclaredModeMigration(t, readFile, writeFile)
	if err == nil || !strings.Contains(err.Error(), "no-script") {
		t.Fatalf("expected a refusal naming the broken step, got err=%v", err)
	}
	if files[normalizePathForWorkspaceAPI("planning/plan.json", changeStepTypeTestWorkspace)] != planBefore {
		t.Fatal("a refusal must leave plan.json untouched -- including the legacy step it would otherwise have converted")
	}
}

func TestMigrateDeclaredExecutionModeIsANoOpOnACleanWorkflow(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		testSequenceStep("talk", "Talk"),
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "scripted", Title: "Scripted", Description: "Deterministic."}},
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "scripted", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeScripted}}})
	files[normalizePathForWorkspaceAPI("learnings/scripted/main.py", changeStepTypeTestWorkspace)] = "print('ok')"
	before := len(files)

	out, err := runDeclaredModeMigration(t, readFile, writeFile)
	if err != nil {
		t.Fatalf("no-op migration errored: %v", err)
	}
	if !strings.Contains(out, `"status":"no_op"`) || len(files) != before {
		t.Fatalf("a clean workflow must be a no-op with no writes, got %s with %d files", out, len(files))
	}
}

func TestMigrateDeclaredExecutionModeIsAPlanModificationTool(t *testing.T) {
	if !IsPlanModificationTool("migrate_declared_execution_mode") {
		t.Fatal("migrate_declared_execution_mode must count as a plan mutation")
	}
}
