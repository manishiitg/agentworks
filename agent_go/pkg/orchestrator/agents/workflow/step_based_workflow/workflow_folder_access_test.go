package step_based_workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

func TestWorkflowFolderAccessBuilderPromptAdvertisesApprovalFlowWithoutGrants(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspacePath := "Workflow/no-attached-folders"
	if err := os.MkdirAll(filepath.Join(docsRoot, filepath.FromSlash(workspacePath)), 0o755); err != nil {
		t.Fatal(err)
	}

	prompt := workflowFolderAccessBuilderPrompt(workspacePath)
	for _, required := range []string{
		"No external folders are currently attached",
		"request_workflow_folder_access",
		"create a pending request",
		"Workflow toolbar → Attached folders",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("Builder prompt omitted %q:\n%s", required, prompt)
		}
	}
}

func TestUpsertWorkflowFolderAccessRequestPersistsAndDeduplicates(t *testing.T) {
	raw := []byte(`{"schema_version":1,"id":"wf_test","label":"test","folder_access":[]}`)
	request := workflowtypes.WorkflowFolderAccessRequest{
		ID: "folder-request-1", Alias: "public-website", Access: workflowtypes.FolderAccessReadWrite,
		Reason: "Publish the site", RequestedAt: "2026-08-29T16:45:00Z",
	}
	updated, existing, err := upsertWorkflowFolderAccessRequest(raw, request)
	if err != nil || existing {
		t.Fatalf("first upsert: existing=%v err=%v", existing, err)
	}
	var manifest workflowFolderAccessManifest
	if err := json.Unmarshal(updated, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.FolderAccessRequests) != 1 || manifest.FolderAccessRequests[0].Alias != "public-website" {
		t.Fatalf("pending request not persisted: %#v", manifest.FolderAccessRequests)
	}
	_, existing, err = upsertWorkflowFolderAccessRequest(updated, request)
	if err != nil || !existing {
		t.Fatalf("duplicate upsert: existing=%v err=%v", existing, err)
	}

	request.RequestedPath = "/tmp/public-website"
	updated, existing, err = upsertWorkflowFolderAccessRequest(updated, request)
	if err != nil || existing {
		t.Fatalf("path enrichment: existing=%v err=%v", existing, err)
	}
	if err := json.Unmarshal(updated, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.FolderAccessRequests) != 1 || manifest.FolderAccessRequests[0].RequestedPath != request.RequestedPath {
		t.Fatalf("pending request path was not enriched in place: %#v", manifest.FolderAccessRequests)
	}
}

func TestAppendWorkflowFolderAccessPreservesModesAndAliases(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspacePath := "Workflow/attached-folders"
	workflowDir := filepath.Join(docsRoot, filepath.FromSlash(workspacePath))
	readOnly := t.TempDir()
	readWrite := t.TempDir()
	readOnly, _ = filepath.EvalSymlinks(readOnly)
	readWrite, _ = filepath.EvalSymlinks(readWrite)
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := workflowFolderAccessManifest{FolderAccess: []workflowtypes.WorkflowFolderGrant{
		{ID: "read", Alias: "reference-data", Path: readOnly, Access: workflowtypes.FolderAccessReadOnly},
		{ID: "write", Alias: "docs-source", Path: readWrite, Access: workflowtypes.FolderAccessReadWrite},
		{ID: "missing", Alias: "missing", Path: filepath.Join(t.TempDir(), "gone"), Access: workflowtypes.FolderAccessReadWrite},
	}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "workflow.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	reads, writes, readOnlyPaths, env := appendWorkflowFolderAccess(workspacePath, []string{"Workflow/attached-folders"}, nil)
	if !containsString(reads, readOnly) || !containsString(reads, readWrite) {
		t.Fatalf("read grants missing: %v", reads)
	}
	if containsString(writes, readOnly) || !containsString(writes, readWrite) {
		t.Fatalf("write modes not preserved: %v", writes)
	}
	if !containsString(readOnlyPaths, readOnly) || containsString(readOnlyPaths, readWrite) {
		t.Fatalf("read-only write-deny roots not preserved: %v", readOnlyPaths)
	}
	if env["WORKFLOW_FOLDER_REFERENCE_DATA"] != readOnly || env["WORKFLOW_FOLDER_project_source"] != readWrite {
		t.Fatalf("alias environment incorrect: %#v", env)
	}
	if _, exists := env["WORKFLOW_FOLDER_MISSING"]; exists {
		t.Fatal("missing host folder should not become a runtime capability")
	}

	sessionID := "attached-folder-read-only-test"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	common.SetSessionFolderGuard(sessionID, reads, writes)
	common.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/attached-folders/planning"})
	configureWorkflowFolderAccessSession(sessionID, workspacePath, readOnlyPaths, env)
	session := common.GetSessionShellConfig(sessionID)
	if session == nil || !containsString(session.BlockedWritePaths, readOnly) || !containsString(session.BlockedWritePaths, "Workflow/attached-folders/planning") {
		t.Fatalf("read-only attachment did not merge into session write denies: %#v", session)
	}
}

func TestRefreshWorkflowFolderAccessSessionRestoresGrantAfterBaseGuardReset(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspacePath := "Workflow/restored-builder"
	workflowDir := filepath.Join(docsRoot, filepath.FromSlash(workspacePath))
	attached := t.TempDir()
	attached, _ = filepath.EvalSymlinks(attached)
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := workflowFolderAccessManifest{FolderAccess: []workflowtypes.WorkflowFolderGrant{{
		ID: "source", Alias: "public-website", Path: attached, Access: workflowtypes.FolderAccessReadWrite,
	}}}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workflowDir, "workflow.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	sessionID := "restored-builder-folder-access"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	common.SetSessionWorkingDir(sessionID, workspacePath)
	common.SetSessionFolderGuard(sessionID, []string{workspacePath, "Downloads"}, []string{workspacePath, "Downloads"})

	RefreshWorkflowFolderAccessSession(sessionID, workspacePath)
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || !containsString(cfg.ReadPaths, attached) || !containsString(cfg.WritePaths, attached) {
		t.Fatalf("restored session did not receive attached folder: %#v", cfg)
	}
	if cfg.Env["WORKFLOW_FOLDER_PUBLIC_WEBSITE"] != attached {
		t.Fatalf("restored session did not receive folder alias env: %#v", cfg.Env)
	}
}

func TestRefreshWorkflowFolderAccessSessionReconcilesCrewGrants(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspacePath := "Workflow/crew-refresh"
	workflowDir := filepath.Join(docsRoot, filepath.FromSlash(workspacePath))
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	crewRoot := "_users/owner/Chats/Work/projects/project-a"
	if err := os.MkdirAll(filepath.Join(docsRoot, filepath.FromSlash(crewRoot)), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(workflowDir, "workflow.json")
	attached := `{"id":"wf-1","crew_attachments":[{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"` + crewRoot + `"}]}`
	if err := os.WriteFile(manifestPath, []byte(attached), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "crew-refresh-reconciles"
	t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
	common.SetSessionWorkingDir(sessionID, workspacePath)

	RefreshWorkflowFolderAccessSession(sessionID, workspacePath)
	cfg := common.GetSessionShellConfig(sessionID)
	if cfg == nil || !containsString(cfg.ReadPaths, crewRoot) || !containsString(cfg.BlockedWritePaths, crewRoot) {
		t.Fatalf("live crew root not granted readable: %#v", cfg)
	}
	if cfg.Env["WORKFLOW_CREW_project-a"] != crewRoot {
		t.Fatalf("live crew alias env missing: %#v", cfg.Env)
	}

	// Detach in the manifest: the next refresh must revoke the grant.
	if err := os.WriteFile(manifestPath, []byte(`{"id":"wf-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	RefreshWorkflowFolderAccessSession(sessionID, workspacePath)
	cfg = common.GetSessionShellConfig(sessionID)
	if cfg == nil {
		t.Fatal("session config missing after refresh")
	}
	for _, paths := range [][]string{cfg.ReadPaths, cfg.BlockedWritePaths} {
		if containsString(paths, crewRoot) {
			t.Fatalf("detached crew root still granted: %#v", cfg)
		}
	}
	for key := range cfg.Env {
		if strings.HasPrefix(key, "WORKFLOW_CREW_") {
			t.Fatalf("detached crew env key survived: %#v", cfg.Env)
		}
	}
}

func TestLiveCrewAttachmentGrantsSkipInvalidAttachments(t *testing.T) {
	docsRoot := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docsRoot)
	workspacePath := "Workflow/crew-grants"
	workflowDir := filepath.Join(docsRoot, filepath.FromSlash(workspacePath))
	if err := os.MkdirAll(workflowDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Only the server A crew workspace exists: the gone crew is deleted and
	// the evil alias is retargeted at an arbitrary path.
	if err := os.MkdirAll(filepath.Join(docsRoot, "_users", "owner", "Chats", "Work", "projects", "project-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"wf-1","crew_attachments":[` +
		`{"id":"a1","alias":"project-a","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"_users/owner/Chats/Work/projects/project-a"},` +
		`{"id":"a2","alias":"gone","crew_profile_id":"work","crew_project_id":"gone","crew_workspace_path":"_users/owner/Chats/Work/projects/gone"},` +
		`{"id":"a3","alias":"evil","crew_profile_id":"work","crew_project_id":"project-a","crew_workspace_path":"_users/owner/secrets"}` +
		`]}`
	if err := os.WriteFile(filepath.Join(workflowDir, "workflow.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	live := liveCrewAttachmentGrants(workspacePath)
	if len(live) != 1 || live[0].Alias != "project-a" {
		t.Fatalf("live grants = %+v, want only the project-a attachment", live)
	}
	read, _, readOnly, env := appendWorkflowFolderAccess(workspacePath, nil, nil)
	crewRoot := "_users/owner/Chats/Work/projects/project-a"
	if !containsString(read, crewRoot) || !containsString(readOnly, crewRoot) {
		t.Fatalf("crew root missing from guard paths: read=%v readOnly=%v", read, readOnly)
	}
	if env["WORKFLOW_CREW_project-a"] != crewRoot {
		t.Fatalf("crew alias env = %v", env)
	}
	for _, paths := range [][]string{read, readOnly} {
		for _, path := range paths {
			if strings.Contains(path, "gone") || strings.Contains(path, "secrets") {
				t.Fatalf("invalid attachment granted: %v", paths)
			}
		}
	}
}
