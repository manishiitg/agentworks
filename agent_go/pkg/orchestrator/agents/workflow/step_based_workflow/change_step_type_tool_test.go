package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

const changeStepTypeTestWorkspace = "Workflow/testing"

// testSequenceStep builds a message_sequence that satisfies plan validation
// on read (title, description, at least one item).
func testSequenceStep(id, title string) *AgentPlanStep {
	return &AgentPlanStep{
		Type:             StepTypeAgent,
		CommonStepFields: CommonStepFields{ID: id, Title: title, Description: title + " step."},
		Items:            []AgentItem{{ID: "work", Type: "user_message", Kind: "execution", Message: "Do the work."}},
	}
}

// changeStepTypeHarness seeds an in-memory workspace with a plan and step
// configs and returns the readFile/writeFile callbacks plan-mod executors get.
func changeStepTypeHarness(t *testing.T, plan *PlanningResponse, configs []StepConfig) (map[string]string, func(context.Context, string) (string, error), func(context.Context, string, string) error) {
	t.Helper()
	files := map[string]string{}
	planJSON, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	files[normalizePathForWorkspaceAPI("planning/plan.json", changeStepTypeTestWorkspace)] = string(planJSON)
	if configs != nil {
		configJSON, err := json.Marshal(StepConfigFile{Steps: configs})
		if err != nil {
			t.Fatal(err)
		}
		files[normalizePathForWorkspaceAPI("planning/step_config.json", changeStepTypeTestWorkspace)] = string(configJSON)
	}
	readFile := func(_ context.Context, path string) (string, error) {
		content, ok := files[path]
		if !ok {
			return "", fmt.Errorf("not found: %s", path)
		}
		return content, nil
	}
	writeFile := func(_ context.Context, path, content string) error {
		files[path] = content
		return nil
	}
	return files, readFile, writeFile
}

func runChangeStepType(t *testing.T, readFile func(context.Context, string) (string, error), writeFile func(context.Context, string, string) error, stepID, target string) (string, error) {
	t.Helper()
	exec := createChangeStepTypeExecutor(changeStepTypeTestWorkspace, loggerv2.NewNoop(), readFile, writeFile)
	return exec(context.Background(), map[string]interface{}{
		"step_id":     stepID,
		"target_type": target,
		"reason":      "the work is a deterministic CLI call",
	})
}

func readTestPlanAndConfigs(t *testing.T, readFile func(context.Context, string) (string, error)) (*PlanningResponse, []StepConfig) {
	t.Helper()
	plan, err := readPlanFromFile(context.Background(), changeStepTypeTestWorkspace, readFile)
	if err != nil {
		t.Fatalf("read plan back: %v", err)
	}
	configs, err := readStepConfigViaFileCallback(context.Background(), changeStepTypeTestWorkspace, readFile)
	if err != nil {
		t.Fatalf("read configs back: %v", err)
	}
	return plan, configs
}

func TestChangeStepTypeConvertsASequenceToScriptedInPlace(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&AgentPlanStep{
			Type:             StepTypeAgent,
			CommonStepFields: CommonStepFields{ID: "place-paper-trades", Title: "Place paper trades", Description: "Place the day's paper trades through the alpaca CLI."},
			NextStepID:       "verify-fills",
			Items: []AgentItem{
				{ID: "work", Type: "user_message", Kind: "execution", Message: "Place the trades."},
				{ID: "verify", Type: "user_message", Kind: "execution", Message: "Verify the fills."},
			},
		},
		testSequenceStep("verify-fills", "Verify fills"),
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "place-paper-trades", AgentConfigs: &AgentConfigs{ExecutionTier: "high"}}})

	out, err := runChangeStepType(t, readFile, writeFile, "place-paper-trades", "scripted")
	if err != nil {
		t.Fatalf("change_step_type failed: %v", err)
	}
	if !strings.Contains(out, "Dropped 2 conversational item(s)") || !strings.Contains(out, "does NOT exist yet") {
		t.Fatalf("result should report dropped items and the missing script, got: %s", out)
	}

	updated, configs := readTestPlanAndConfigs(t, readFile)
	step, _, _ := findStepByID(updated.Steps, "place-paper-trades")
	regular, ok := step.(*RegularPlanStep)
	if !ok {
		t.Fatalf("step type after conversion = %T, want *RegularPlanStep", step)
	}
	if regular.Title != "Place paper trades" || regular.Description == "" || regular.NextStepID != "verify-fills" {
		t.Fatalf("conversion must keep id/title/description/next_step_id, got %+v", regular)
	}
	cfg := MatchStepConfigByID("place-paper-trades", configs)
	if cfg == nil || cfg.LegacyDeclaredExecutionMode != "" {
		t.Fatalf("step_config entry must exist and never carry the retired declared_execution_mode, got %+v", cfg)
	}
	if cfg.ExecutionTier != "high" {
		t.Fatalf("existing step_config fields must survive, got %+v", cfg)
	}
	if cfg.UseCodeExecutionMode == nil || !*cfg.UseCodeExecutionMode {
		t.Fatalf("scripted mode must sync use_code_execution_mode, got %+v", cfg)
	}

	entry := findChangelogEntry(t, changeStepTypeTestWorkspace, files, "change_step_type")
	if entry.BeforeRef == "" || entry.AfterRef == "" || entry.BeforeRef == entry.AfterRef {
		t.Fatalf("changelog refs must hash the real plan before and after, got before=%q after=%q", entry.BeforeRef, entry.AfterRef)
	}
	if len(entry.DeletedSteps) != 1 || len(entry.AddedSteps) != 1 {
		t.Fatalf("changelog entry must carry the full step JSON before and after for a revert, got deleted=%d added=%d", len(entry.DeletedSteps), len(entry.AddedSteps))
	}
	if !strings.Contains(string(entry.DeletedSteps[0]), `"agent"`) || !strings.Contains(string(entry.AddedSteps[0]), `"regular"`) {
		t.Fatalf("revert data must hold the old sequence and the new regular step, got deleted=%s added=%s", entry.DeletedSteps[0], entry.AddedSteps[0])
	}
	sawType := false
	for _, change := range entry.Changes {
		if change.Field == "type" && change.OldValue == string(StepTypeAgent) && change.NewValue == string(StepTypeRegular) {
			sawType = true
		}
	}
	if !sawType {
		t.Fatalf("changelog must record the type change, got %+v", entry.Changes)
	}
}

func TestChangeStepTypeConvertsScriptedToASequenceAndClearsTheMode(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "collect-price", Title: "Collect price", Description: "Collect prices."}, NextStepID: "judge"},
		testSequenceStep("judge", "Judge"),
	}}
	lock := true
	files, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "collect-price", AgentConfigs: &AgentConfigs{LockCode: &lock}}})
	files[normalizePathForWorkspaceAPI("learnings/collect-price/main.py", changeStepTypeTestWorkspace)] = "print('hi')"

	out, err := runChangeStepType(t, readFile, writeFile, "collect-price", "agent")
	if err != nil {
		t.Fatalf("change_step_type failed: %v", err)
	}
	if !strings.Contains(out, "main.py still exists") {
		t.Fatalf("result should warn about the now-orphaned script, got: %s", out)
	}

	updated, configs := readTestPlanAndConfigs(t, readFile)
	step, _, _ := findStepByID(updated.Steps, "collect-price")
	seq, ok := step.(*AgentPlanStep)
	if !ok {
		t.Fatalf("step type after conversion = %T, want *AgentPlanStep", step)
	}
	if len(seq.Items) != 1 || seq.Items[0].ID != normalizedRegularSequenceItemID || seq.NextStepID != "judge" {
		t.Fatalf("expected one execute-and-verify item and the chain kept, got %+v", seq)
	}
	cfg := MatchStepConfigByID("collect-price", configs)
	if cfg == nil || cfg.LockCode != nil {
		t.Fatalf("the code lock must be cleared on a sequence, got %+v", cfg)
	}
}

func TestChangeStepTypeClearsTheLegacyAgenticKeyOnARegularStep(t *testing.T) {
	// A regular step still carrying the retired declared_execution_mode=
	// "agentic" runs as a message_sequence through the transitional shim;
	// converting it to scripted means clearing that key (the plan type
	// already says scripted).
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "legacy"}},
	}}
	_, readFile, writeFile := changeStepTypeHarness(t, plan, []StepConfig{{ID: "legacy", AgentConfigs: &AgentConfigs{LegacyDeclaredExecutionMode: StepModeAgentic, LegacyDeclaredExecutionModeReason: "needs judgment"}}})

	if _, err := runChangeStepType(t, readFile, writeFile, "legacy", "scripted"); err != nil {
		t.Fatalf("change_step_type failed: %v", err)
	}
	updated, configs := readTestPlanAndConfigs(t, readFile)
	step, _, _ := findStepByID(updated.Steps, "legacy")
	if step.StepType() != StepTypeRegular {
		t.Fatalf("plan type must stay regular, got %s", step.StepType())
	}
	cfg := MatchStepConfigByID("legacy", configs)
	if cfg == nil || cfg.LegacyDeclaredExecutionMode != "" || cfg.LegacyDeclaredExecutionModeReason != "" {
		t.Fatalf("the retired key must be cleared, got %+v", cfg)
	}
	if !isScriptedStep(step, cfg) || cfg.UseCodeExecutionMode == nil || !*cfg.UseCodeExecutionMode {
		t.Fatalf("the step must now run scripted with code execution on, got %+v", cfg)
	}
}

func TestChangeStepTypeToScriptedIsANoOpOnAPlainRegularStep(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RegularPlanStep{Type: StepTypeRegular, CommonStepFields: CommonStepFields{ID: "already"}},
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, nil)
	before := len(files)
	out, err := runChangeStepType(t, readFile, writeFile, "already", "scripted")
	if err != nil {
		t.Fatalf("no-op conversion errored: %v", err)
	}
	if !strings.Contains(out, "already") || len(files) != before {
		t.Fatalf("a regular step is scripted by type; nothing to do, got %q with %d files", out, len(files))
	}
}

func TestChangeStepTypeIsANoOpWhenAlreadyTheTarget(t *testing.T) {
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		testSequenceStep("seq", "Seq"),
	}}
	files, readFile, writeFile := changeStepTypeHarness(t, plan, nil)
	before := len(files)

	out, err := runChangeStepType(t, readFile, writeFile, "seq", "agent")
	if err != nil {
		t.Fatalf("no-op conversion errored: %v", err)
	}
	if !strings.Contains(out, "already") || len(files) != before {
		t.Fatalf("a no-op must write nothing and say so, got %q with %d files", out, len(files))
	}
}

func TestChangeStepTypeRejectsOtherTypesAndUnknownSteps(t *testing.T) {
	// Pure mutation, no file round-trip: the point is the type check, not a
	// fully valid routing fixture.
	plan := &PlanningResponse{Steps: []PlanStepInterface{
		&RoutingPlanStep{Type: StepTypeRouting, CommonStepFields: CommonStepFields{ID: "route", Title: "Route"}},
	}}
	if _, err := changeStepTypeInPlan(plan, nil, "route", "scripted"); err == nil || !strings.Contains(err.Error(), "routing") {
		t.Fatalf("a routing step must be rejected by type, got err=%v", err)
	}
	if _, err := changeStepTypeInPlan(plan, nil, "missing", "scripted"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an unknown step must be reported, got err=%v", err)
	}
}

func TestChangeStepTypeConvertsAnOrphanStep(t *testing.T) {
	plan := &PlanningResponse{
		Steps:       []PlanStepInterface{testSequenceStep("main", "Main")},
		OrphanSteps: []PlanStepInterface{testSequenceStep("orphan", "Orphan")},
	}
	_, readFile, writeFile := changeStepTypeHarness(t, plan, nil)

	if _, err := runChangeStepType(t, readFile, writeFile, "orphan", "scripted"); err != nil {
		t.Fatalf("an orphan step must be convertible: %v", err)
	}
	updated, _ := readTestPlanAndConfigs(t, readFile)
	if step, _, _ := findStepByID(updated.OrphanSteps, "orphan"); step == nil || step.StepType() != StepTypeRegular {
		t.Fatalf("orphan step should now be regular, got %v", step)
	}
	if step, _, _ := findStepByID(updated.Steps, "main"); step == nil || step.StepType() != StepTypeAgent {
		t.Fatalf("the untouched step must keep its type, got %v", step)
	}
}

func TestChangeStepTypeIsAPlanModificationTool(t *testing.T) {
	if !IsPlanModificationTool("change_step_type") {
		t.Fatal("change_step_type must count as a plan mutation")
	}
}
