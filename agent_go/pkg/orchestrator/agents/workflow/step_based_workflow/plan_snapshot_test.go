package step_based_workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func snapshotTestPlan(t *testing.T, id, description string) string {
	t.Helper()
	data, err := json.Marshal(map[string]interface{}{"steps": []interface{}{map[string]interface{}{
		"id": id, "title": id, "type": "agent", "description": description,
		"items": []interface{}{map[string]interface{}{"id": "work", "type": "user_message", "message": "Do and verify work."}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCurrentPlanReadsCannotReplaceExecutionSnapshots(t *testing.T) {
	const planPath = "Workflow/instagram/planning/plan.json"
	files := map[string]string{planPath: snapshotTestPlan(t, "old-step", "old description")}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, files)}
	first, err := controller.ReadCurrentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	firstCtx := withExecutionPlan(t.Context(), first)
	files[planPath] = snapshotTestPlan(t, "new-step", "new description")
	second, err := controller.ReadCurrentPlan(firstCtx)
	if err != nil {
		t.Fatal(err)
	}
	secondCtx := withExecutionPlan(t.Context(), second)
	if first == second {
		t.Fatal("distinct reads reused a plan object")
	}
	for _, check := range []struct {
		ctx context.Context
		id  string
	}{{firstCtx, "old-step"}, {secondCtx, "new-step"}, {context.WithoutCancel(firstCtx), "old-step"}} {
		if got := executionPlanFromContext(check.ctx).Steps[0].GetID(); got != check.id {
			t.Fatalf("execution snapshot = %s, want %s", got, check.id)
		}
		if got := controller.getArtifactFolderNamesForStep(check.ctx, 1); len(got) != 1 || got[0] != check.id {
			t.Fatalf("artifact lookup used a different execution: %v", got)
		}
	}
	if executionPlanFromContext(t.Context()) != nil {
		t.Fatal("execution snapshot leaked into parent context")
	}
	files[planPath] = "invalid"
	if _, err := controller.ReadCurrentPlan(firstCtx); err == nil {
		t.Fatal("invalid current plan fell back to execution snapshot")
	}
}

func TestStepConfigLookupsReadFreshFromCurrentPlan(t *testing.T) {
	const planPath = "Workflow/instagram/planning/plan.json"
	files := map[string]string{planPath: snapshotTestPlan(t, "first", "original")}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, files)}
	for _, id := range []string{"first", "added"} {
		files[planPath] = snapshotTestPlan(t, id, "latest")
		got, err := resolveWorkshopStepConfigTarget(t.Context(), controller, id)
		if err != nil || got != id {
			t.Fatalf("current target = %s: %v", got, err)
		}
	}
	if _, err := resolveWorkshopStepConfigTarget(t.Context(), controller, "first"); err == nil {
		t.Fatal("deleted step still resolved")
	}
}

func TestRegisteredStepPromptsUseRunRevisionNotCurrentPlan(t *testing.T) {
	oldPlan := snapshotTestPlan(t, "removed-step", "Historical contract")
	value, err := canonicalJSONDocument(oldPlan)
	if err != nil {
		t.Fatal(err)
	}
	revisionFiles := map[string]interface{}{"planning/plan.json": value}
	id, _, err := planRevisionForFiles(revisionFiles)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := json.Marshal(executablePlanRevision{RevisionID: id, Files: revisionFiles})
	if err != nil {
		t.Fatal(err)
	}
	const metadataPath = "Workflow/instagram/runs/iteration-2/default/run_metadata.json"
	files := map[string]string{
		"Workflow/instagram/planning/plan.json": "invalid current plan must not affect historical lookup",
		metadataPath:                            `{"plan_revision":"` + id + `"}`,
		"Workflow/instagram/planning/revisions/" + id + ".json":                                                                string(revision),
		"Workflow/instagram/runs/iteration-2/default/logs/removed-step/execution/execution-attempt-1-iteration-0-prompts.json": `{"system_prompt":"Historical system prompt","user_message":"Historical message"}`,
	}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, files), selectedRunFolder: "iteration-2/default"}
	agent := newWorkshopDefinitionDraft()
	RegisterWorkshopChatTools(agent, &WorkshopChatSession{controller: controller, StepRegistry: NewWorkshopStepRegistry(), config: &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()}}, workshopToolTestLogger{})
	tool := agent.tools["get_step_prompts"]
	result, err := tool.Execute(t.Context(), map[string]interface{}{"step_id": "1"})
	if err != nil || !strings.Contains(result, "Historical system prompt") {
		t.Fatalf("historical prompt = %q, %v", result, err)
	}
	// Legacy runs can still retrieve artifacts by stable ID, never by today's positions.
	delete(files, metadataPath)
	result, err = tool.Execute(t.Context(), map[string]interface{}{"step_id": "removed-step"})
	if err != nil || !strings.Contains(result, "Historical message") {
		t.Fatalf("legacy exact ID = %q, %v", result, err)
	}
	if _, err := tool.Execute(t.Context(), map[string]interface{}{"step_id": "1"}); err == nil {
		t.Fatal("historical position silently used another plan")
	}
	// Refuse a revision whose contents no longer match its content-addressed ID.
	files[metadataPath] = `{"plan_revision":"` + id + `"}`
	files["Workflow/instagram/planning/revisions/"+id+".json"] = `{"revision_id":"` + id + `","files":{}}`
	if _, err := controller.readRunPlanSnapshot(t.Context(), "iteration-2/default"); err == nil {
		t.Fatal("accepted mismatched revision contents")
	}
}

func TestRunRevisionUsesExecutionSnapshotAfterCurrentPlanChanges(t *testing.T) {
	const planPath = "Workflow/instagram/planning/plan.json"
	files := map[string]string{planPath: snapshotTestPlan(t, "running-step", "Executing contract")}
	controller := &StepBasedWorkflowOrchestrator{BaseOrchestrator: newFakeWorkspaceAPIWithContent(t, files)}
	plan, err := controller.ReadCurrentPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := withExecutionPlan(t.Context(), plan)
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	value, err := canonicalJSONDocument(string(encoded))
	if err != nil {
		t.Fatal(err)
	}
	revisionFiles := make(map[string]interface{})
	for _, path := range executablePlanRevisionFiles {
		revisionFiles[path] = nil
	}
	revisionFiles["planning/plan.json"] = value
	id, _, err := planRevisionForFiles(revisionFiles)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := json.Marshal(executablePlanRevision{RevisionID: id, Files: revisionFiles})
	if err != nil {
		t.Fatal(err)
	}
	files["Workflow/instagram/planning/revisions/"+id+".json"] = string(revision)
	files[planPath] = snapshotTestPlan(t, "next-step", "New contract for a later dispatch")
	got, err := controller.ensureExecutablePlanRevision(ctx)
	if err != nil || got != id {
		t.Fatalf("run revision = %s, %v; want loaded snapshot %s", got, err, id)
	}
}
