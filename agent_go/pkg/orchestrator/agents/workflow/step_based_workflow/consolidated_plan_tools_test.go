package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func consolidatedPlanDraft(t *testing.T, files *externalPlanTestFiles) *workshopDefinitionDraft {
	t.Helper()
	d := newWorkshopDefinitionDraft()
	if err := RegisterPlanModificationTools(d, externalPlanTestWorkspace, loggerv2.NewNoop(), files.read, files.write, nil, "adapter test"); err != nil {
		t.Fatal(err)
	}
	return d
}
func TestConsolidatedPlanRegistryHidesNativeAliases(t *testing.T) {
	d := consolidatedPlanDraft(t, newExternalPlanTestFiles(t, regularStep("fetch")))
	for _, name := range []string{
		"add_step", "update_step", "manage_step_route", "change_step_type", "maintain_plan",
		"migrate_agent_code_items", "migrate_agent_steps",
		"migrate_declared_execution_mode", "strip_declared_execution_mode",
		"create_plan", "delete_plan_steps", "validate_plan_change", "update_validation_schema", "record_plan_drift_review",
	} {
		if _, ok := d.tools[name]; !ok {
			t.Errorf("missing %s", name)
		}
	}
	for name := range d.tools {
		if capturedPlanName(name) && name != "change_step_type" {
			t.Errorf("legacy tool still exposed: %s", name)
		}
	}
}
func TestConsolidatedUpdateUsesCurrentPlanAndNativeChangelog(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	d := consolidatedPlanDraft(t, f)
	// Change persisted state after tool construction, so the adapter cannot use
	// a registration-time type or plan snapshot.
	plan := PlanningResponse{Steps: []PlanStepInterface{testSequenceStep("fetch", "Fetch")}}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	f.files[externalPlanTestWorkspace+"/planning/plan.json"] = string(raw)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, "adapter-owner")
	out, err := d.tools["update_step"].Execute(ctx, map[string]interface{}{"step_id": "fetch", "changes": map[string]interface{}{"title": "Review invoices", "reason": "Clarify invoice review scope"}})
	if err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	var after PlanningResponse
	if err = json.Unmarshal([]byte(f.files[externalPlanTestWorkspace+"/planning/plan.json"]), &after); err != nil {
		t.Fatal(err)
	}
	if after.Steps[0].StepType() != StepTypeAgent || after.Steps[0].GetTitle() != "Review invoices" {
		t.Fatalf("wrong mutation: %+v", after.Steps)
	}
	found := false
	for path, data := range f.files {
		if strings.Contains(path, "/planning/changelog/") && strings.Contains(data, "update_agent_step") && strings.Contains(data, "adapter-owner") {
			found = true
		}
	}
	if !found {
		t.Fatal("native changelog and caller attribution were lost")
	}
}
func TestConsolidatedUpdateRejectsWrongTypeFieldsBeforeWrites(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	d := consolidatedPlanDraft(t, f)
	cases := []map[string]interface{}{
		{"step_id": "fetch", "changes": map[string]interface{}{"reason": "Add invalid routing fields", "routes": []interface{}{}}},
		{"step_id": "fetch", "changes": map[string]interface{}{"reason": "Reject invented field", "invented": true}},
		{"step_id": "fetch", "changes": map[string]interface{}{"reason": "Cannot redirect target", "existing_step_id": "other", "title": "Wrong"}},
		{"step_id": "missing", "changes": map[string]interface{}{"reason": "Reject missing step", "title": "Missing"}},
	}
	for _, args := range cases {
		if _, err := d.tools["update_step"].Execute(context.Background(), args); err == nil {
			t.Fatalf("accepted invalid input: %+v", args)
		}
		if f.writes != 0 {
			t.Fatalf("wrote on invalid input: %+v", args)
		}
	}
}
func TestConsolidatedAddUsesNativeConfigAndRejectsTypeMismatch(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	d := consolidatedPlanDraft(t, f)
	step := map[string]interface{}{"id": "validate", "title": "Validate invoices", "description": "Validate totals and write validated.json.", "context_dependencies": []string{}, "context_output": "validated.json", "insert_after_step_id": "fetch", "validation_schema": map[string]interface{}{"files": []interface{}{map[string]interface{}{"file_name": "validated.json", "must_exist": true}}}, "reason": "Validate invoices before processing"}
	_, err := d.tools["add_step"].Execute(context.Background(), map[string]interface{}{"type": "scripted", "step": step})
	if err != nil {
		t.Fatal(err)
	}
	configs, err := ParseStepConfigContent(f.files[externalPlanTestWorkspace+"/planning/step_config.json"])
	if err != nil {
		t.Fatal(err)
	}
	config := MatchStepConfigByID("validate", configs)
	if config == nil || config.UseCodeExecutionMode == nil || !*config.UseCodeExecutionMode {
		t.Fatal("native scripted configuration lost")
	}
	before := f.writes
	if _, err = d.tools["add_step"].Execute(context.Background(), map[string]interface{}{"type": "human_input", "step": step}); err == nil {
		t.Fatal("accepted scripted payload as human input")
	}
	if f.writes != before {
		t.Fatal("invalid typed payload wrote files")
	}
}
func TestConsolidatedUpdateFindsOrphanSteps(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	var plan PlanningResponse
	_ = json.Unmarshal([]byte(f.files[externalPlanTestWorkspace+"/planning/plan.json"]), &plan)
	plan.OrphanSteps = []PlanStepInterface{regularStep("utility")}
	raw, _ := json.Marshal(plan)
	f.files[externalPlanTestWorkspace+"/planning/plan.json"] = string(raw)
	d := consolidatedPlanDraft(t, f)
	if _, err := d.tools["update_step"].Execute(context.Background(), map[string]interface{}{"step_id": "utility", "changes": map[string]interface{}{"title": "Utility renamed", "reason": "Clarify utility purpose"}}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(f.files[externalPlanTestWorkspace+"/planning/plan.json"]), &plan)
	if plan.OrphanSteps[0].GetTitle() != "Utility renamed" {
		t.Fatal("orphan update missed target")
	}
}
func TestConsolidatedToolsPreserveScheduleCollisionGuard(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	d := newWorkshopDefinitionDraft()
	blocked := errors.New("scheduled execution is active")
	guard := GuardScheduleTools(d, func(context.Context, string, map[string]interface{}) error { return blocked })
	if err := RegisterPlanModificationTools(guard, externalPlanTestWorkspace, loggerv2.NewNoop(), f.read, f.write, nil, "test"); err != nil {
		t.Fatal(err)
	}
	_, err := d.tools["update_step"].Execute(context.Background(), map[string]interface{}{"step_id": "fetch", "changes": map[string]interface{}{"title": "New", "reason": "Change title"}})
	if !errors.Is(err, blocked) || f.writes != 0 {
		t.Fatalf("collision protection lost: %v writes=%d", err, f.writes)
	}
}

// The approved schedule override (`force`) must pass the guard AND the real update_step schema: update_step
// validates strictly and used to reject `force` as an unknown property, so the override never worked
// (salesoutreach and jobsearch chats, 2026-10-05).
func TestScheduleOverrideReachesTheRealUpdateStep(t *testing.T) {
	f := newExternalPlanTestFiles(t, regularStep("fetch"))
	d := newWorkshopDefinitionDraft()
	guard := GuardScheduleTools(d, func(_ context.Context, _ string, args map[string]interface{}) error {
		if args["force"] == true {
			return nil
		}
		return errors.New("schedule_running")
	})
	if err := RegisterPlanModificationTools(guard, externalPlanTestWorkspace, loggerv2.NewNoop(), f.read, f.write, nil, "test"); err != nil {
		t.Fatal(err)
	}
	args := map[string]interface{}{"step_id": "fetch", "changes": map[string]interface{}{"title": "New", "reason": "Change title"}}
	if _, err := d.tools["update_step"].Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "schedule_running") {
		t.Fatalf("without force the guard must block: %v", err)
	}
	args["force"] = true
	if _, err := d.tools["update_step"].Execute(context.Background(), args); err != nil || f.writes == 0 {
		t.Fatalf("approved override did not apply: err=%v writes=%d", err, f.writes)
	}
}
func TestWorkshopConsolidatesGroupsAndRemovesReviewLauncher(t *testing.T) {
	d := newWorkshopDefinitionDraft()
	base := &orchestrator.BaseOrchestrator{}
	base.SetWorkspacePath(t.TempDir())
	session := &WorkshopChatSession{controller: &StepBasedWorkflowOrchestrator{BaseOrchestrator: base}, StepRegistry: NewWorkshopStepRegistry(), config: &WorkshopConfig{WorkspacePath: base.GetWorkspacePath()}}
	RegisterWorkshopChatTools(d, session, workshopToolTestLogger{})
	if _, ok := d.tools["manage_group"]; !ok {
		t.Fatal("group adapter missing")
	}
	for _, n := range []string{"review_plan", "add_group", "update_group", "delete_group"} {
		if _, ok := d.tools[n]; ok {
			t.Errorf("removed tool %s still present", n)
		}
	}
	if _, err := d.tools["manage_group"].Execute(context.Background(), map[string]interface{}{"action": "delete", "parameters": map[string]interface{}{"name": "x", "values": map[string]interface{}{"bad": "value"}}}); err == nil {
		t.Fatal("delete accepted update-only fields")
	}
	for _, n := range []string{"add_step", "update_step", "manage_step_route", "manage_group", "maintain_plan", "change_step_type"} {
		if !ScheduleGuardedTool(n) {
			t.Errorf("unguarded canonical tool %s", n)
		}
	}
}
func TestBackgroundReadOnlyAccessRejectsEscalationAndWriterTools(t *testing.T) {
	for _, args := range []map[string]interface{}{{"access_mode": "unknown"}, {"access_mode": true}} {
		if _, err := parseBackgroundReadOnlyAccess(args, "executor"); err == nil {
			t.Fatal("invalid access mode accepted")
		}
	}
	if _, err := parseBackgroundReadOnlyAccess(map[string]interface{}{"access_mode": "read_only"}, "orchestrator"); err == nil {
		t.Fatal("read-only delegation accepted mutating orchestrator")
	}
	if ok, err := parseBackgroundReadOnlyAccess(map[string]interface{}{"access_mode": "read_only"}, "executor"); err != nil || !ok {
		t.Fatal("read-only executor unavailable")
	}
	for _, n := range []string{"add_step", "update_step", "execute_step", "run_full_workflow", "run_in_background", "write_workspace_file", "record_pulse_finding", "record_plan_drift_review", "slack", "notify_user", "set_workflow_secret"} {
		if readOnlyBackgroundToolAllowed(n) {
			t.Errorf("writer tool allowed: %s", n)
		}
	}
	for _, n := range []string{"get_workflow_config", "query_workflow_db", "execute_shell_command", "read_skill"} {
		if !readOnlyBackgroundToolAllowed(n) {
			t.Errorf("inspection tool missing: %s", n)
		}
	}
}

func TestConsolidatedUpdateFindsNestedSteps(t *testing.T) {
	parent := &AgentPlanStep{CommonStepFields: CommonStepFields{ID: "parent", Title: "Delegate", Description: "Delegate analysis"}, NextStepID: "end", PredefinedRoutes: []PlanOrchestrationRoute{{RouteID: "nested", RouteName: "Analysis", SubAgentStep: testSequenceStep("nested", "Analyze")}}}
	f := newExternalPlanTestFiles(t, parent)
	d := consolidatedPlanDraft(t, f)
	if _, err := d.tools["update_step"].Execute(context.Background(), map[string]interface{}{"step_id": "nested", "changes": map[string]interface{}{"title": "Analyze invoices", "reason": "Clarify delegated analysis"}}); err != nil {
		t.Fatal(err)
	}
	var after PlanningResponse
	_ = json.Unmarshal([]byte(f.files[externalPlanTestWorkspace+"/planning/plan.json"]), &after)
	step, _, _ := findStepByID(after.Steps, "nested")
	if step == nil || step.GetTitle() != "Analyze invoices" {
		t.Fatal("nested update missed target")
	}
}
func TestConsolidatedConversionRetainsNativeRestrictions(t *testing.T) {
	f := newExternalPlanTestFiles(t, testSequenceStep("fetch", "Fetch"))
	d := consolidatedPlanDraft(t, f)
	if _, err := d.tools["change_step_type"].Execute(context.Background(), map[string]interface{}{"step_id": "fetch", "target_type": "branch", "reason": "Reject unrelated conversion"}); err == nil {
		t.Fatal("unrelated conversion accepted")
	}
	if f.writes != 0 {
		t.Fatal("unrelated conversion wrote files")
	}
	if _, err := d.tools["change_step_type"].Execute(context.Background(), map[string]interface{}{"step_id": "fetch", "target_type": "scripted", "reason": "Make retrieval deterministic"}); err != nil {
		t.Fatal(err)
	}
	var after PlanningResponse
	_ = json.Unmarshal([]byte(f.files[externalPlanTestWorkspace+"/planning/plan.json"]), &after)
	if after.Steps[0].GetID() != "fetch" || after.Steps[0].StepType() != StepTypeRegular {
		t.Fatal("conversion lost identity or type")
	}
}

// server A: a crew add_step missing id was rejected with every oneOf branch's
// complaints (including other types' validation_schema rules), which led the
// agent to add fields crew steps do not take. The error now names the chosen
// type's own problems only.
func TestConsolidatedAddReportsTheChosenTypesSchemaError(t *testing.T) {
	d := consolidatedPlanDraft(t, newExternalPlanTestFiles(t, regularStep("fetch")))
	step := map[string]interface{}{
		"title": "Crew flow test", "crew_profile_id": "work", "crew_project_id": "p1", "trigger_id": "t1",
		"instruction": "Exercise the flow.", "context_dependencies": []string{}, "insert_after_step_id": "fetch",
		"reason": "Delegate flow testing",
	}
	typ := "crew"
	if _, ok := d.tools["add_step"]; !ok {
		t.Fatal("add_step missing")
	}
	_, err := d.tools["add_step"].Execute(context.Background(), map[string]interface{}{"type": typ, "step": step})
	if err == nil {
		t.Fatal("crew step without id was accepted")
	}
	msg := err.Error()
	if !strings.Contains(msg, `type "crew"`) || !strings.Contains(msg, "'id'") {
		t.Fatalf("error does not name the crew schema's missing id: %s", msg)
	}
	if strings.Contains(msg, "validation_schema") || strings.Contains(msg, "oneOf") {
		t.Fatalf("error still mixes other step types' rules: %s", msg)
	}
}
