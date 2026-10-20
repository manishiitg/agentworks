package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestValidateManifestFolderAccess(t *testing.T) {
	manifest := NewWorkflowManifest("Attached folders")
	manifest.FolderAccess = []workflowtypes.WorkflowFolderGrant{{
		ID: "grant-1", Alias: "docs-source", Path: t.TempDir(), Access: workflowtypes.FolderAccessReadWrite,
	}}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid folder grant rejected: %v", err)
	}

	manifest.FolderAccess = append(manifest.FolderAccess, workflowtypes.WorkflowFolderGrant{
		ID: "grant-2", Alias: "docs_source", Path: t.TempDir(), Access: workflowtypes.FolderAccessReadOnly,
	})
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "environment key") {
		t.Fatalf("environment-key alias collision should be rejected, got %v", err)
	}

	manifest.FolderAccess = []workflowtypes.WorkflowFolderGrant{{
		ID: "grant-root", Alias: "root", Path: string(filepath.Separator), Access: workflowtypes.FolderAccessReadOnly,
	}}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "filesystem root") {
		t.Fatalf("filesystem root should be rejected, got %v", err)
	}
}

func TestValidateManifestWorkflowIcon(t *testing.T) {
	manifest := NewWorkflowManifest("Daily review")
	manifest.Icon = "📊"
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid workflow icon rejected: %v", err)
	}

	manifest.Icon = strings.Repeat("x", 9)
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "icon must be 8 characters or fewer") {
		t.Fatalf("ValidateManifest() error = %v, want icon length error", err)
	}

	manifest.Icon = "data:image/png;base64," + strings.Repeat("A", 1024)
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("uploaded image icon rejected: %v", err)
	}
}

func TestValidateManifestFolderAccessRequest(t *testing.T) {
	manifest := NewWorkflowManifest("Pending folder request")
	manifest.FolderAccessRequests = []workflowtypes.WorkflowFolderAccessRequest{{
		ID: "folder-request-1", Alias: "public-website", Access: workflowtypes.FolderAccessReadWrite,
		RequestedPath: t.TempDir(), Reason: "Publish the website", RequestedAt: "2026-08-29T16:45:00Z",
	}}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid pending folder request rejected: %v", err)
	}
	manifest.FolderAccessRequests[0].Reason = ""
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("pending folder request without a reason should be rejected")
	}
	manifest.FolderAccessRequests[0].Reason = "Publish the website"
	manifest.FolderAccessRequests[0].RequestedPath = "relative/path"
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("relative requested path should be rejected, got %v", err)
	}
}

func TestValidateManifestWorkflowContextPaths(t *testing.T) {
	manifest := NewWorkflowManifest("Linked workflows")
	manifest.WorkflowContextPaths = []string{"Workflow/reference"}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid workflow reference rejected: %v", err)
	}
	manifest.WorkflowContextPaths = []string{"Workflow/reference", "Workflow/reference"}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate workflow reference should be rejected, got %v", err)
	}
	manifest.WorkflowContextPaths = []string{"Chats/not-a-workflow"}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "Workflow/<folder>") {
		t.Fatalf("non-workflow reference should be rejected, got %v", err)
	}
}

func TestNormalizeWorkflowFolderGrantsCanonicalizesAndPreservesCreation(t *testing.T) {
	realRoot := t.TempDir()
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "selected")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	requested := []workflowtypes.WorkflowFolderGrant{{ID: "grant-1", Alias: "source", Path: link, Access: workflowtypes.FolderAccessReadOnly}}
	previous := []workflowtypes.WorkflowFolderGrant{{ID: "grant-1", CreatedAt: "2026-08-01T00:00:00Z"}}
	normalized, err := normalizeWorkflowFolderGrants(requested, previous)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(realRoot)
	if normalized[0].Path != canonical || normalized[0].CreatedAt != previous[0].CreatedAt || normalized[0].UpdatedAt == "" {
		t.Fatalf("unexpected normalized grant: %#v", normalized[0])
	}
}

func TestWorkflowManifestChangelogChangesIsStableAndValueFree(t *testing.T) {
	previous := `{
  "label": "Old label",
  "capabilities": {
    "selected_skills": ["browser"],
    "slack_webhook_secret_name": "old-secret-value"
  },
  "unchanged": true
}`
	current := `{
  "label": "New label",
  "capabilities": {
    "selected_skills": ["browser", "ffmpeg"],
    "slack_webhook_secret_name": "new-secret-value"
  },
  "new_setting": "enabled",
  "unchanged": true
}`

	got := workflowManifestChangelogChanges(previous, current)
	want := []step_based_workflow.PlanFieldChange{
		{Field: "workflow.json.capabilities.selected_skills", OldValue: "present", NewValue: "changed"},
		{Field: "workflow.json.capabilities.slack_webhook_secret_name", OldValue: "present", NewValue: "changed"},
		{Field: "workflow.json.label", OldValue: "present", NewValue: "changed"},
		{Field: "workflow.json.new_setting", OldValue: "absent", NewValue: "added"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changes = %#v, want %#v", got, want)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal changes: %v", err)
	}
	for _, value := range []string{"old-secret-value", "new-secret-value", "New label"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("changelog evidence leaked manifest value %q: %s", value, encoded)
		}
	}
}

func TestWorkflowManifestChangelogChangesCreationAndCorruptPriorState(t *testing.T) {
	created := workflowManifestChangelogChanges("", `{"label":"New workflow","capabilities":{"enabled":true}}`)
	wantCreated := []step_based_workflow.PlanFieldChange{
		{Field: "workflow.json", OldValue: "absent", NewValue: "added"},
	}
	if !reflect.DeepEqual(created, wantCreated) {
		t.Fatalf("creation changes = %#v, want %#v", created, wantCreated)
	}

	corrupt := workflowManifestChangelogChanges("not-json", `{"label":"New workflow"}`)
	wantCorrupt := []step_based_workflow.PlanFieldChange{
		{Field: "workflow.json", OldValue: "present", NewValue: "changed"},
	}
	if !reflect.DeepEqual(corrupt, wantCorrupt) {
		t.Fatalf("corrupt prior changes = %#v, want %#v", corrupt, wantCorrupt)
	}
}

func TestValidateManifestCDPPorts(t *testing.T) {
	manifest := NewWorkflowManifest("Multi-profile browser")
	manifest.Capabilities.BrowserMode = "cdp"
	manifest.Capabilities.CDPPorts = []int{9222, 9333}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid multi-profile CDP ports rejected: %v", err)
	}

	manifest.Capabilities.CDPPorts = []int{9222, 9222}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("duplicate CDP ports should be rejected")
	}

	manifest.Capabilities.CDPPorts = []int{9222, 9333, 9444, 9555, 9666}
	if err := ValidateManifest(manifest); err == nil {
		t.Fatal("more than four CDP ports should be rejected")
	}
}

func TestValidateManifestRejectsEquivalentMCPServerAliases(t *testing.T) {
	manifest := NewWorkflowManifest("Duplicate MCP aliases")
	manifest.Capabilities.SelectedServers = []string{"google_sheets", "google-sheets"}

	err := ValidateManifest(manifest)
	if err == nil {
		t.Fatal("equivalent MCP server aliases should be rejected")
	}
	if !strings.Contains(err.Error(), "resolve to the same MCP server") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateManifestRejectsScheduleDependencyCycle(t *testing.T) {
	manifest := NewWorkflowManifest("Dependency cycle")
	manifest.Schedules = []WorkflowSchedule{
		{ID: "collect", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "0 16 * * 1-5", AfterScheduleIDs: []string{"publish"}},
		{ID: "review", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "5 16 * * 1-5", AfterScheduleIDs: []string{"collect"}},
		{ID: "publish", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "10 16 * * 1-5", AfterScheduleIDs: []string{"review"}},
	}
	err := ValidateManifest(manifest)
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("expected dependency cycle to be rejected, got %v", err)
	}
}

func TestValidateManifestAcceptsTypedDependencyAndCollisionPolicies(t *testing.T) {
	manifest := NewWorkflowManifest("Dependent schedules")
	manifest.Schedules = []WorkflowSchedule{
		{ID: "close", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "55 15 * * 1-5", CollisionPolicy: "queue_latest"},
		{ID: "snapshot", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "0 16 * * 1-5", CollisionPolicy: "queue_latest"},
		{
			ID: "pulse", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "10 16 * * 1-5",
			AfterScheduleIDs: []string{"close", "snapshot"}, AfterTerminalStatus: "completed", AfterDelayMinutes: 10,
			DependencyDeadline: "17:30", CollisionPolicy: "coalesce", MaxStartDelayMinutes: 80,
		},
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("valid dependency policy rejected: %v", err)
	}

	manifest.Schedules[2].DependencyDeadline = "tomorrow"
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "HH:MM") {
		t.Fatalf("invalid dependency deadline should be rejected, got %v", err)
	}
}

func TestValidateManifestRejectsInvalidDependencyLists(t *testing.T) {
	manifest := NewWorkflowManifest("Invalid dependency policy")
	manifest.Schedules = []WorkflowSchedule{
		{ID: "collect", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "0 9 * * *"},
		{ID: "publish", Mode: "multi-agent", ScheduleType: "cron", CronExpression: "30 9 * * *", AfterScheduleIDs: []string{""}},
	}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("empty dependency ID should be rejected, got %v", err)
	}

	manifest.Schedules[1].AfterScheduleIDs = []string{"missing"}
	if err := ValidateManifest(manifest); err == nil || !strings.Contains(err.Error(), "unknown dependency") {
		t.Fatalf("unknown dependency ID should be rejected, got %v", err)
	}
}

func TestReplaceScheduleDependencyIDsClearsHiddenLegacyDependency(t *testing.T) {
	schedule := WorkflowSchedule{
		AfterScheduleID:  "legacy-parent",
		AfterScheduleIDs: []string{"old-list-parent"},
	}
	replaceScheduleDependencyIDs(&schedule, []string{"new-parent"}, false)
	if schedule.AfterScheduleID != "" {
		t.Fatalf("legacy dependency survived list replacement: %q", schedule.AfterScheduleID)
	}
	if got := scheduleDependencyIDs(schedule); !reflect.DeepEqual(got, []string{"new-parent"}) {
		t.Fatalf("dependencies = %v, want only new-parent", got)
	}

	schedule.AfterScheduleID = "explicit-legacy-parent"
	replaceScheduleDependencyIDs(&schedule, []string{"new-parent"}, true)
	if got := scheduleDependencyIDs(schedule); !reflect.DeepEqual(got, []string{"explicit-legacy-parent", "new-parent"}) {
		t.Fatalf("explicit singular+list update lost a dependency: %v", got)
	}
}

func TestNewWorkflowManifestDefaultsGlobalSecretsToNone(t *testing.T) {
	manifest := NewWorkflowManifest("Test workflow")
	if manifest.Version != WorkflowContractCurrentVersion {
		t.Fatalf("Version = %q, want %q", manifest.Version, WorkflowContractCurrentVersion)
	}
	if manifest.Capabilities.SelectedGlobalSecretNames == nil {
		t.Fatal("SelectedGlobalSecretNames = nil, want empty selection")
	}
	if got := len(*manifest.Capabilities.SelectedGlobalSecretNames); got != 0 {
		t.Fatalf("SelectedGlobalSecretNames length = %d, want 0", got)
	}
}

func TestWorkflowCreatorDefaultsGlobalSecretsToNone(t *testing.T) {
	cases := []struct {
		name        string
		workflowMap map[string]interface{}
	}{
		{
			name:        "missing capabilities",
			workflowMap: map[string]interface{}{},
		},
		{
			name: "null global secrets",
			workflowMap: map[string]interface{}{
				"capabilities": map[string]interface{}{
					"selected_global_secret_names": nil,
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defaultWorkflowCreatorGlobalSecretsToNone(tc.workflowMap)

			caps := tc.workflowMap["capabilities"].(map[string]interface{})
			names, ok := caps["selected_global_secret_names"].([]interface{})
			if !ok {
				t.Fatalf("selected_global_secret_names = %T, want []interface{}", caps["selected_global_secret_names"])
			}
			if len(names) != 0 {
				t.Fatalf("selected_global_secret_names length = %d, want 0", len(names))
			}
		})
	}
}

func TestReadWorkflowManifestMigratesMissingLabelFromWorkspacePath(t *testing.T) {
	const workspacePath = "Workflow/instagram"
	manifestJSON, err := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_instagram",
		"version":        "1.0.9",
		"capabilities":   map[string]interface{}{},
		"schedules":      []interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	manifest, found, err := ReadWorkflowManifest(context.Background(), workspacePath)
	if err != nil || !found {
		t.Fatalf("ReadWorkflowManifest() found=%v err=%v", found, err)
	}
	if manifest.Label != "instagram" {
		t.Fatalf("Label = %q, want instagram", manifest.Label)
	}

	workspace.mu.Lock()
	persistedJSON := workspace.files[workspacePath+"/workflow.json"]
	workspace.mu.Unlock()
	var persisted WorkflowManifest
	if err := json.Unmarshal([]byte(persistedJSON), &persisted); err != nil {
		t.Fatalf("unmarshal persisted manifest: %v", err)
	}
	if persisted.Label != "instagram" {
		t.Fatalf("persisted Label = %q, want instagram", persisted.Label)
	}
}

func TestReadWorkflowManifestPrunesRetiredExecutionDefaultsFields(t *testing.T) {
	const workspacePath = "Workflow/linkedin"
	manifestJSON, err := json.Marshal(map[string]interface{}{
		"schema_version": 1,
		"id":             "wf_linkedin",
		"version":        "1.0.20",
		"label":          "linkedin",
		"capabilities":   map[string]interface{}{},
		"execution_defaults": map[string]interface{}{
			"always_use_same_run":    true,
			"global_skill_objective": "retired field text",
			"workshop_mode":          "optimizer",
		},
		"schedules": []interface{}{},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	workspace := &mockWorkspaceAPI{files: map[string]string{
		workspacePath + "/workflow.json": string(manifestJSON),
	}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)

	manifest, found, err := ReadWorkflowManifest(context.Background(), workspacePath)
	if err != nil || !found {
		t.Fatalf("ReadWorkflowManifest() found=%v err=%v", found, err)
	}
	if manifest.ExecutionDefs.WorkshopMode != "optimizer" {
		t.Fatalf("known execution_defaults fields not preserved: %+v", manifest.ExecutionDefs)
	}

	workspace.mu.Lock()
	persistedJSON := workspace.files[workspacePath+"/workflow.json"]
	workspace.mu.Unlock()
	if strings.Contains(persistedJSON, "global_skill_objective") || strings.Contains(persistedJSON, "always_use_same_run") {
		t.Fatalf("persisted manifest still contains retired execution defaults: %s", persistedJSON)
	}
	if !strings.Contains(persistedJSON, "\"workshop_mode\": \"optimizer\"") {
		t.Fatalf("persisted manifest lost known field workshop_mode: %s", persistedJSON)
	}
}

func TestReadWorkflowManifestDoesNotRewriteCrewRuntimeManifest(t *testing.T) {
	// Code projects too (PLAT-701): their product schedules have no group_names.
	for _, workspacePath := range []string{"_users/user-1/Chats/Work/projects/prodissue-362d6e05", "Chats/Work/projects/prodissue-362d6e05", "Crew/prodissue-362d6e05", "_users/user-1/Chats/Code/projects/orbit2-0-53daa320", "Chats/Code/projects/orbit2-0-53daa320"} {
		t.Run(workspacePath, func(t *testing.T) {
			manifestJSON := `{
  "schema_version": 1,
  "id": "362d6e05-94ea-5d89-9799-bd957367fe75",
  "label": "prodissue",
  "identity": {"name": "Production issues", "icon": "🛠️"},
  "capabilities": {},
  "schedules": [{"id": "daily", "name": "Daily", "messages": ["check"], "isolated": true}],
  "triggers": [{"id": "3477f709-12a7-4d6e-aa30-0dec685de412", "name": "Local test", "enabled": true, "message": "test", "run_destination": "crew_chat", "webhook": {"auth_mode": "bearer", "encrypted_secret": "ciphertext"}}],
  "crew_extension": {"future": true}
}`

			if strings.Contains(workspacePath, "/Code/") {
				// Without schedules the rewrite would validate and drop the
				// product fields, so this pins the skip, not the validator.
				manifestJSON = strings.Replace(manifestJSON, `[{"id": "daily", "name": "Daily", "messages": ["check"], "isolated": true}]`, `[]`, 1)
			}
			workspace := &mockWorkspaceAPI{files: map[string]string{
				workspacePath + "/workflow.json": manifestJSON,
			}}
			server := httptest.NewServer(workspace)
			defer server.Close()
			t.Setenv("WORKSPACE_API_URL", server.URL)

			_, found, err := ReadWorkflowManifest(context.Background(), workspacePath)
			if err != nil || !found {
				t.Fatalf("ReadWorkflowManifest() found=%v err=%v", found, err)
			}

			workspace.mu.Lock()
			persistedJSON := workspace.files[workspacePath+"/workflow.json"]
			workspace.mu.Unlock()
			if persistedJSON != manifestJSON {
				t.Fatalf("Crew runtime manifest was rewritten:\n%s", persistedJSON)
			}
		})
	}
}

// 2026-10-15: a workflow whose workflow.json still carried the retired top-level objective and success_criteria was rewritten on
// EVERY read (about 270 times a minute): the writer kept every key the schema has no field for, so the prune never removed them.
func TestReadWorkflowManifestPrunesRetiredTopLevelFieldsOnce(t *testing.T) {
	const workspacePath = "Workflow/x-post-drafts"
	manifestJSON, err := json.Marshal(map[string]interface{}{
		"schema_version": 1, "id": "wf_x", "version": "1.0.20", "label": "x-post-drafts",
		"objective": "retired objective text", "success_criteria": []string{"retired"},
		"future_field": map[string]string{"kept": "by the writer"},
		"capabilities": map[string]interface{}{}, "schedules": []interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace := &mockWorkspaceAPI{files: map[string]string{workspacePath + "/workflow.json": string(manifestJSON)}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	staleManifestPruneMu.Lock()
	delete(staleManifestPruneAt, workspacePath)
	staleManifestPruneMu.Unlock()

	if _, found, err := ReadWorkflowManifest(context.Background(), workspacePath); err != nil || !found {
		t.Fatalf("ReadWorkflowManifest() found=%v err=%v", found, err)
	}
	workspace.mu.Lock()
	first := workspace.files[workspacePath+"/workflow.json"]
	workspace.mu.Unlock()
	if strings.Contains(first, "objective") || strings.Contains(first, "success_criteria") {
		t.Fatalf("the prune did not remove the retired top-level fields: %s", first)
	}
	// Only the RETIRED keys go: a key the schema merely does not model (another subsystem, another app version) stays.
	if !strings.Contains(first, "future_field") {
		t.Fatalf("the prune deleted a top-level key that is not on the retired list: %s", first)
	}
	// A second read finds nothing stale and writes nothing: the file is byte-for-byte what the first read left.
	if _, _, err := ReadWorkflowManifest(context.Background(), workspacePath); err != nil {
		t.Fatal(err)
	}
	workspace.mu.Lock()
	second := workspace.files[workspacePath+"/workflow.json"]
	workspace.mu.Unlock()
	if second != first {
		t.Fatalf("a read of an already-clean manifest rewrote it:\n%s\n--- vs ---\n%s", first, second)
	}
}
