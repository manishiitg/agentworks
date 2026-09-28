package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func newCrewBuilderToolsTestEnv(t *testing.T) (map[string]recordedTool, *mockWorkspaceAPI, string, *ProductScheduleService) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true}]}`)

	svc, files := newInternalTriggerTestCrew(t)
	productJSON := `{"schema_version":1,"product":"crewx","id":"rts","title":"RTS","session_id":"sess-1","triggers":[]}`
	files["_users/owner/Chats/Work/projects/rts/product.json"] = productJSON

	manifest := NewWorkflowManifest("Builder test")
	manifest.CreatedBy = "owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"owner"}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockWorkspaceAPI{files: map[string]string{
		manifestPath("Workflow/test"):                       string(raw),
		"Workflow/test/planning/plan.json":                  `{"steps":[{"type":"regular","id":"work","title":"Work","description":"Work"}]}`,
		"Workflow/test/variables/variables.json":            `{"variables":[],"groups":[{"name":"default"}]}`,
		"_users/owner/Chats/Work/projects/rts/product.json": productJSON,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)

	api := &StreamingAPI{productSchedules: svc}
	reg := &recordingRegistrar{}
	if err := api.registerCrewBuilderTools(reg, "owner", "Workflow/test"); err != nil {
		t.Fatal(err)
	}
	return reg.tools, mock, manifest.ID, svc
}

func TestManageCrewTriggerInternalDefaultsCaller(t *testing.T) {
	tools, _, workflowID, _ := newCrewBuilderToolsTestEnv(t)
	tool := tools["manage_crew_trigger"]
	ctx := context.Background()

	createdRaw, err := tool.exec(ctx, map[string]interface{}{
		"action": "create", "crew_project_id": "rts", "crew_profile_id": "crewx",
		"name": "Release reviewer", "message": "Review the delivery",
		"kind": "internal", "enabled": true,
	})
	if err != nil {
		t.Fatalf("internal create failed: %v", err)
	}
	var created productWebhookResponse
	if err := json.Unmarshal([]byte(createdRaw), &created); err != nil {
		t.Fatal(err)
	}
	if created.Kind != triggerKindInternal || created.Caller == nil || created.Caller.ID != workflowID {
		t.Fatalf("caller did not default to bound workflow: %+v", created)
	}
	if created.Secret != "" || created.Path != "" || created.AuthMode != "" {
		t.Fatalf("internal trigger exposed endpoint material: %+v", created)
	}

	updatedRaw, err := tool.exec(ctx, map[string]interface{}{
		"action": "update", "crew_project_id": "rts", "crew_profile_id": "crewx",
		"id": created.ID, "enabled": false,
	})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	var updated productWebhookResponse
	if err := json.Unmarshal([]byte(updatedRaw), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatalf("update did not disable trigger: %+v", updated)
	}
	if updated.Kind != triggerKindInternal || updated.Caller == nil || updated.Caller.ID != workflowID {
		t.Fatalf("update lost internal binding: %+v", updated)
	}

	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "delete", "crew_project_id": "rts", "crew_profile_id": "crewx", "id": created.ID,
	}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	listedRaw, err := tool.exec(ctx, map[string]interface{}{
		"action": "list", "crew_project_id": "rts", "crew_profile_id": "crewx",
	})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	var listed struct {
		Triggers []productWebhookResponse `json:"triggers"`
	}
	if err := json.Unmarshal([]byte(listedRaw), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Triggers) != 0 {
		t.Fatalf("expected no triggers after delete, got %+v", listed.Triggers)
	}
}

func TestManageCrewTriggerRejectsUnknownCallerWorkflow(t *testing.T) {
	tools, _, _, _ := newCrewBuilderToolsTestEnv(t)
	tool := tools["manage_crew_trigger"]
	if _, err := tool.exec(context.Background(), map[string]interface{}{
		"action": "create", "crew_project_id": "rts", "crew_profile_id": "crewx",
		"name": "Ghost", "message": "Review", "kind": "internal", "enabled": true,
		"caller": map[string]interface{}{"type": "workflow", "id": "ghost-pipeline"},
	}); err == nil {
		t.Fatal("binding to an unknown caller workflow must fail")
	}
}

func TestCrewBuilderMutationsRecheckWriteAccess(t *testing.T) {
	tools, mock, _, _ := newCrewBuilderToolsTestEnv(t)
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.CreatedBy = "different-owner"
	manifest.Access = &WorkflowAccess{Owners: []string{"different-owner"}, Readers: []string{"owner"}}
	raw, _ := json.Marshal(manifest)
	mock.files[manifestPath("Workflow/test")] = string(raw)
	before := len(mock.files)
	for _, name := range []string{"create_crew", "manage_crew_attachment", "manage_crew_trigger"} {
		if _, err := tools[name].exec(context.Background(), map[string]interface{}{"action": "create"}); err == nil || !strings.Contains(err.Error(), "write access") {
			t.Errorf("%s failed to recheck write access: %v", name, err)
		}
	}
	if len(mock.files) != before {
		t.Fatal("revoked writer changed workspace files")
	}
	if _, err := tools["manage_crew_attachment"].exec(context.Background(), map[string]interface{}{"action": "list"}); err != nil {
		t.Fatalf("read-only discovery should remain available: %v", err)
	}
}

func TestDetachCrewAttachmentRevokesLiveSessionGrant(t *testing.T) {
	tools, mock, _, _ := newCrewBuilderToolsTestEnv(t)
	tool := tools["manage_crew_attachment"]
	ctx := context.Background()
	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "rts", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 1 {
		t.Fatalf("attachments = %+v", manifest.CrewAttachments)
	}
	root := strings.TrimSpace(manifest.CrewAttachments[0].CrewWorkspacePath)

	// Simulate a run-start session: workflow-owned, holding the crew
	// grant plus an unrelated folder grant that must survive.
	sessionID := "crew-detach-revokes-live-grant"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	common.ApplySessionWorkflowFolderAccess(sessionID, "Workflow/test",
		[]string{"Workflow/test/runs"}, nil, nil,
		map[string]string{"WORKFLOW_CREW_RTS": root})
	common.GrantSessionCrewAttachmentReads(sessionID, []string{root})

	if _, err := tool.exec(ctx, map[string]interface{}{"action": "detach", "alias": "rts"}); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil {
		t.Fatal("session config missing after detach")
	}
	for _, paths := range [][]string{cfg.ReadPaths, cfg.BlockedWritePaths} {
		for _, path := range paths {
			if path == root {
				t.Fatalf("detached crew root still granted: %+v", cfg)
			}
		}
	}
	for key := range cfg.Env {
		if strings.HasPrefix(key, "WORKFLOW_CREW_") {
			t.Fatalf("detached crew env key survived: %v", cfg.Env)
		}
	}
	found := false
	for _, path := range cfg.ReadPaths {
		if path == "Workflow/test/runs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("unrelated grant lost on detach: %+v", cfg.ReadPaths)
	}
}

func TestCrewAttachmentReadRootsSkipInvalidBindings(t *testing.T) {
	tools, mock, _, svc := newCrewBuilderToolsTestEnv(t)
	ctx := context.Background()
	// Attach through the tool so the stored root equals the resolved binding.
	if _, err := tools["manage_crew_attachment"].exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "rts", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 1 {
		t.Fatalf("attachments = %+v", manifest.CrewAttachments)
	}
	root := manifest.CrewAttachments[0].CrewWorkspacePath
	if roots := crewAttachmentReadRoots(ctx, svc, "owner", "Workflow/test"); len(roots) != 1 || roots[0] != root {
		t.Fatalf("roots = %v, want the authorized crew root %q", roots, root)
	}
	rewrite := func(path string) {
		t.Helper()
		manifest.CrewAttachments[0].CrewWorkspacePath = path
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		mock.files[manifestPath("Workflow/test")] = string(raw)
	}
	// Same project-name suffix under another owner: shape-valid, but it is
	// not the freshly authorized binding and must never be granted.
	other := strings.Replace(root, "_users/owner/", "_users/other/", 1)
	if other == root {
		t.Fatalf("fixture root %q has no owner segment to swap", root)
	}
	rewrite(other)
	if roots := crewAttachmentReadRoots(ctx, svc, "owner", "Workflow/test"); len(roots) != 0 {
		t.Fatalf("wrong-owner roots = %v, want none", roots)
	}
	// Non-project paths still fail the shape check first.
	rewrite("_users/owner/secrets")
	if roots := crewAttachmentReadRoots(ctx, svc, "owner", "Workflow/test"); len(roots) != 0 {
		t.Fatalf("retargeted roots = %v, want none", roots)
	}
	if roots := crewAttachmentReadRoots(ctx, nil, "owner", "Workflow/test"); len(roots) != 0 {
		t.Fatalf("nil-service roots = %v, want none", roots)
	}
	if roots := crewAttachmentReadRoots(ctx, svc, "owner", "Workflow/missing"); len(roots) != 0 {
		t.Fatalf("missing manifest roots = %v, want none", roots)
	}
}

func TestCreateCrewToolEndToEnd(t *testing.T) {
	tools, mock, _, svc := newCrewBuilderToolsTestEnv(t)
	registerWorkCrewProfile(t, svc.registry)
	// Creation reads and writes through the workspace API like production;
	// the files-map stub other builder tests use would split the stores.
	svc.readFile = readFileFromWorkspace
	svc.writeFile = writeFileToWorkspace
	tool, ok := tools["create_crew"]
	if !ok {
		t.Fatal("create_crew tool is not registered")
	}
	ctx := context.Background()
	result, err := tool.exec(ctx, map[string]interface{}{
		"title": "Release Reviewer", "purpose": "Own release quality", "role": "Reviewer",
		"step_instruction": "Review the release.", "idempotency_key": "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Created crew", "release-reviewer", "add_step(type=crew)", "crew-release-reviewer"} {
		if !strings.Contains(result, want) {
			t.Fatalf("result missing %q:\n%s", want, result)
		}
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 1 || manifest.CrewAttachments[0].Alias != "release-reviewer" {
		t.Fatalf("attachments = %+v", manifest.CrewAttachments)
	}
	crewPath := manifest.CrewAttachments[0].CrewWorkspacePath
	if _, ok := mock.files[crewPath+"/product.json"]; !ok {
		t.Fatal("crew product.json missing")
	}
	if _, ok := mock.files[crewPath+"/MEMORY.md"]; !ok {
		t.Fatal("crew MEMORY.md missing")
	}
	triggers, err := svc.projectWebhookConfigs(ctx, "owner", "work", manifest.CrewAttachments[0].CrewProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(triggers) != 1 || !triggers[0].IsInternal() {
		t.Fatalf("triggers = %+v, want one internal binding", triggers)
	}
	again, err := tool.exec(ctx, map[string]interface{}{
		"title": "Release Reviewer", "purpose": "Own release quality", "role": "Reviewer",
		"step_instruction": "Review the release.", "idempotency_key": "proposal-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again, "Adopted existing") {
		t.Fatalf("retry = %q, want adoption", again)
	}
}

func TestCreateCrewToolRejectsBadInput(t *testing.T) {
	tools, _, _, svc := newCrewBuilderToolsTestEnv(t)
	registerWorkCrewProfile(t, svc.registry)
	tool := tools["create_crew"]
	ctx := context.Background()
	if _, err := tool.exec(ctx, map[string]interface{}{
		"step_instruction": "Review.", "idempotency_key": "k",
	}); err == nil {
		t.Fatal("missing title: expected rejection")
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"title": "T", "step_instruction": "Review.", "idempotency_key": "k",
		"skills": "not-an-array",
	}); err == nil || !strings.Contains(err.Error(), "skills") {
		t.Fatalf("non-array skills err = %v, want rejection", err)
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"title": "T", "purpose": "P", "role": "R", "idempotency_key": "k",
	}); err == nil {
		t.Fatal("missing step instruction: expected rejection")
	}
}

func TestManageCrewTriggerRejectsInaccessibleCallerWorkflow(t *testing.T) {
	tools, mock, _, _ := newCrewBuilderToolsTestEnv(t)
	foreign := NewWorkflowManifest("Foreign pipeline")
	foreign.ID = "wf-foreign"
	foreign.CreatedBy = "stranger"
	foreign.Access = &WorkflowAccess{Owners: []string{"stranger"}}
	foreignRaw, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	mock.files[manifestPath("Workflow/foreign")] = string(foreignRaw)
	tool := tools["manage_crew_trigger"]
	if _, err := tool.exec(context.Background(), map[string]interface{}{
		"action": "create", "crew_project_id": "rts", "crew_profile_id": "crewx",
		"name": "Squat", "message": "Review", "kind": "internal", "enabled": true,
		"caller": map[string]interface{}{"type": "workflow", "id": "wf-foreign"},
	}); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("foreign caller err = %v, want access denial", err)
	}
}

func TestManageCrewAttachmentRoundTrip(t *testing.T) {
	tools, mock, _, _ := newCrewBuilderToolsTestEnv(t)
	tool := tools["manage_crew_attachment"]
	ctx := context.Background()

	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "rts", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err != nil {
		t.Fatalf("attach failed: %v", err)
	}
	var manifest WorkflowManifest
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 1 || manifest.CrewAttachments[0].Alias != "rts" {
		t.Fatalf("attachment not persisted: %+v", manifest.CrewAttachments)
	}
	if !strings.HasPrefix(manifest.CrewAttachments[0].CrewWorkspacePath, "_users/owner/") {
		t.Fatalf("attachment workspace not resolved: %+v", manifest.CrewAttachments[0])
	}

	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "rts", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err == nil {
		t.Fatal("duplicate alias must fail")
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "rts-again", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err == nil {
		t.Fatal("duplicate crew project must fail")
	}
	if _, err := tool.exec(ctx, map[string]interface{}{
		"action": "attach", "alias": "Bad Alias!", "crew_project_id": "rts", "crew_profile_id": "crewx",
	}); err == nil {
		t.Fatal("invalid alias must fail")
	}

	if _, err := tool.exec(ctx, map[string]interface{}{"action": "detach", "alias": "rts"}); err != nil {
		t.Fatalf("detach failed: %v", err)
	}
	manifest = WorkflowManifest{}
	if err := json.Unmarshal([]byte(mock.files[manifestPath("Workflow/test")]), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.CrewAttachments) != 0 {
		t.Fatalf("detach did not persist: %+v", manifest.CrewAttachments)
	}
	if _, err := tool.exec(ctx, map[string]interface{}{"action": "detach", "alias": "rts"}); err == nil {
		t.Fatal("detaching a missing alias must fail")
	}
}
