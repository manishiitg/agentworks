package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPlanAgentCodeMigrationsRejectsMixedSequence(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"mixed",
		"type":"agent",
		"title":"Mixed",
		"description":"Conversation followed by code",
		"items":[
			{"id":"ask","type":"user_message","message":"Clarify the input."},
			{"id":"run","type":"code","script_path":"scripts/run.py","output_files":["db/result.json"]}
		]
	}`)

	migrations, blockers, err := planAgentCodeMigrations([]json.RawMessage{raw}, nil)
	if err != nil {
		t.Fatalf("plan migration: %v", err)
	}
	if len(migrations) != 1 {
		t.Fatalf("migrations = %d, want inventory entry for blocked sequence", len(migrations))
	}
	if len(blockers) != 1 || !strings.Contains(blockers[0], "mixes code item(s) with conversational item") {
		t.Fatalf("blockers = %v, want mixed-sequence blocker", blockers)
	}
}

func TestValidateAgentCodeMigrationCompleteAcceptsNoOpPlan(t *testing.T) {
	plan := `{
		"steps":[{
			"id":"plain-sequence",
			"type":"agent",
			"items":[{"id":"review","type":"user_message","message":"Review the result."}]
		}],
		"orphan_steps":[]
	}`
	if err := ValidateAgentCodeMigrationComplete(plan); err != nil {
		t.Fatalf("no-op plan should satisfy v1.0.10 postcondition: %v", err)
	}
}

func TestValidateAgentCodeMigrationCompleteRejectsRemainingCode(t *testing.T) {
	plan := `{
		"steps":[{
			"id":"legacy",
			"type":"agent",
			"items":[{"id":"run","type":"code","script_path":"scripts/run.py","output_files":["db/result.json"]}]
		}]
	}`
	err := ValidateAgentCodeMigrationComplete(plan)
	if err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("validation error = %v, want remaining sequence id", err)
	}
}

func TestConvertLegacyCodeSequenceCreatesStandaloneScriptedSteps(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"legacy",
		"type":"agent",
		"title":"Legacy pipeline",
		"description":"Prepare and summarize deterministic data.",
		"context_dependencies":["seed.json"],
		"items":[
			{
				"id":"prepare-data",
				"type":"code",
				"runtime":"python",
				"script_path":"scripts/prepare.py",
				"input_files":["raw.json"],
				"output_files":["db/prepared.json"],
				"on_failure":{"action":"repair_with_llm","max_retries":2}
			},
			{
				"id":"verify-prepare",
				"type":"prevalidation",
				"validation_schema":{"files":[{"file_name":"db/prepared.json","required":true,"validation_type":"json"}]}
			},
			{
				"id":"summarize-data",
				"type":"code",
				"script_path":"scripts/summarize.py",
				"input_files":["db/prepared.json"],
				"output_files":["reports/summary.json"]
			}
		]
	}`)

	migrations, blockers, err := planAgentCodeMigrations([]json.RawMessage{raw}, nil)
	if err != nil {
		t.Fatalf("plan migration: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("unexpected blockers: %v", blockers)
	}
	if len(migrations) != 1 {
		t.Fatalf("migrations = %d, want 1", len(migrations))
	}

	files := map[string]string{
		"Workflow/test/scripts/prepare.py":   "print('prepare')\n",
		"Workflow/test/scripts/summarize.py": "print('summarize')\n",
	}
	readFile := func(_ context.Context, path string) (string, error) {
		content, ok := files[path]
		if !ok {
			return "", &testMissingMigrationFile{path: path}
		}
		return content, nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	useCode := false
	configs := []StepConfig{{
		ID:    "legacy",
		Title: "Legacy pipeline",
		AgentConfigs: &AgentConfigs{
			UseCodeExecutionMode: &useCode,
		},
		ValidationSchema: &ValidationSchema{Files: []FileValidationRule{{FileName: "reports/summary.json", MustExist: true}}},
	}}

	converted, copied, migratedConfigs, err := convertLegacyCodeSequence(
		context.Background(), "Workflow/test", migrations[0], configs, readFile, writeFile,
	)
	if err != nil {
		t.Fatalf("convert migration: %v", err)
	}
	if len(converted) != 2 || len(copied) != 2 || len(migratedConfigs) != 2 {
		t.Fatalf("converted=%d copied=%d configs=%d, want 2 each", len(converted), len(copied), len(migratedConfigs))
	}
	if files["Workflow/test/learnings/prepare-data/main.py"] != "print('prepare')\n" {
		t.Fatal("prepare script was not copied to its scripted-step location")
	}

	var first, second RegularPlanStep
	if err := json.Unmarshal(converted[0], &first); err != nil {
		t.Fatalf("decode first step: %v", err)
	}
	if err := json.Unmarshal(converted[1], &second); err != nil {
		t.Fatalf("decode second step: %v", err)
	}
	if got := strings.Join(first.ContextDependencies, ","); got != "seed.json,raw.json" {
		t.Fatalf("first dependencies = %q", got)
	}
	if got := strings.Join(second.ContextDependencies, ","); got != "seed.json,db/prepared.json" {
		t.Fatalf("second dependencies = %q", got)
	}
	if first.ValidationSchema == nil {
		t.Fatal("prevalidation was not attached to the preceding scripted step")
	}
	for _, cfg := range migratedConfigs {
		if cfg.AgentConfigs == nil || cfg.AgentConfigs.UseCodeExecutionMode == nil || !*cfg.AgentConfigs.UseCodeExecutionMode {
			t.Fatalf("step %s is not configured as scripted: %+v", cfg.ID, cfg.AgentConfigs)
		}
	}
	// PLAT-061 dropped two derived fields from this migration:
	//   db_access — the runtime discarded it entirely.
	//   learn_code_max_fix_iterations — it wrote 0 for every item without a
	//     legacy repair action, which read as a deliberate "never repair" but was
	//     only the absence of a legacy field. Five hetznerssh steps carried that
	//     0 with no reasoning behind it.
	// Scripted steps now take the uniform default; lock_code is the explicit way
	// to skip the fix loop.
	if migratedConfigs[1].ValidationSchema == nil || len(migratedConfigs[1].ValidationSchema.Files) != 1 {
		t.Fatalf("final migrated step did not preserve parent config validation: %+v", migratedConfigs[1].ValidationSchema)
	}
}

func TestExplicitConversationScriptConversationPlanValidates(t *testing.T) {
	var plan PlanningResponse
	err := json.Unmarshal([]byte(`{
		"steps":[
			{
				"id":"prepare-input",
				"type":"agent",
				"title":"Prepare input",
				"description":"Prepare durable input for the deterministic transform.",
				"context_output":"db/prepared-input.json",
				"items":[{"id":"verify-input","type":"user_message","message":"Verify the durable input and correct it if needed."}],
				"validation_schema":{"files":[{"file_name":"db/prepared-input.json","must_exist":true}]}
			},
			{
				"id":"transform-input",
				"type":"regular",
				"title":"Transform input",
				"description":"Run the standalone deterministic transform from learnings/transform-input/main.py.",
				"context_dependencies":["db/prepared-input.json"],
				"context_output":"db/transformed-output.json",
				"validation_schema":{"files":[{"file_name":"db/transformed-output.json","must_exist":true}]}
			},
			{
				"id":"review-output",
				"type":"agent",
				"title":"Review output",
				"description":"Review the durable transform output and decide the next action.",
				"context_dependencies":["db/transformed-output.json"],
				"items":[{"id":"critique-output","type":"user_message","message":"Critique the transformed output against the workflow goal."}],
				"validation_schema":{"files":[{"file_name":"db/transformed-output.json","must_exist":true}]}
			}
		]
	}`), &plan)
	if err != nil {
		t.Fatalf("decode explicit context-flow plan: %v", err)
	}
	if err := ValidatePlanStructure(&plan); err != nil {
		t.Fatalf("agent -> scripted step -> agent plan should validate: %v", err)
	}
}

func TestAgentValidatorRejectsLegacyCodeItem(t *testing.T) {
	step := &AgentPlanStep{
		Type: StepTypeAgent,
		CommonStepFields: CommonStepFields{
			ID:          "legacy",
			Title:       "Legacy",
			Description: "Old code sequence",
		},
		Items: []AgentItem{{ID: "run", Type: "code"}},
	}
	err := validateAgentStepFieldsTyped(step)
	if err == nil || !strings.Contains(err.Error(), "contract v1.0.10") || !strings.Contains(err.Error(), "migrate_agent_code_items") {
		t.Fatalf("validation error = %v, want actionable v1.0.10 migration error", err)
	}
}

func TestLegacyAgentCodeCanLoadOnlyForMigration(t *testing.T) {
	var plan PlanningResponse
	if err := json.Unmarshal([]byte(`{
		"steps":[{
			"id":"legacy",
			"type":"agent",
			"title":"Legacy",
			"description":"Run deterministic code.",
			"items":[{"id":"run-code","type":"code","script_path":"scripts/run.py","output_files":["db/result.json"]}]
		}]
	}`), &plan); err != nil {
		t.Fatalf("decode legacy plan: %v", err)
	}
	if err := validateLoadedPlanStructure(&plan); err == nil {
		t.Fatal("strict runtime validator accepted a legacy code item")
	}
	if err := validateLoadedPlanStructureAllowLegacyAgentCode(&plan); err != nil {
		t.Fatalf("migration-only loader rejected legacy code item: %v", err)
	}

	plan.Steps = append(plan.Steps, &RegularPlanStep{
		Type: StepTypeRegular,
		CommonStepFields: CommonStepFields{
			ID:          "legacy",
			Title:       "Duplicate",
			Description: "Invalid duplicate id.",
		},
	})
	if err := validateLoadedPlanStructureAllowLegacyAgentCode(&plan); err == nil {
		t.Fatal("migration-only loader ignored an unrelated duplicate-id error")
	}
}

type testMissingMigrationFile struct{ path string }

func (e *testMissingMigrationFile) Error() string { return "missing test file: " + e.path }
