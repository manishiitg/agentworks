package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

func TestPlanMutationWriteAccessDoesNotUnblockGeneralFileWrites(t *testing.T) {
	const sessionID = "plan-mutation-write-access"
	const planPath = "Workflow/demo/planning/plan.json"
	workspacepkg.SetSessionFolderGuard(sessionID, []string{"Workflow/demo"}, []string{"Workflow/demo"})
	workspacepkg.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{"Workflow/demo/planning"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)

	client := workspacepkg.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	if err := client.ValidatePathWithContext(ctx, planPath, true); err == nil {
		t.Fatal("ordinary file write unexpectedly reached the guarded plan")
	}

	called := false
	writeFile := withPlanMutationWriteAccess("Workflow/demo", func(callCtx context.Context, path, _ string) error {
		called = true
		return client.ValidatePathWithContext(callCtx, path, true)
	})
	if err := writeFile(ctx, planPath, `{}`); err != nil {
		t.Fatalf("dedicated plan mutation write was blocked: %v", err)
	}
	if !called {
		t.Fatal("plan mutation write callback was not called")
	}

	if err := client.ValidatePathWithContext(ctx, planPath, true); err == nil || !strings.Contains(err.Error(), "blocked for writes") {
		t.Fatalf("plan capability leaked back into the caller context: %v", err)
	}
}

func TestPlanningFileMutationWriteAccessIsExact(t *testing.T) {
	const (
		sessionID     = "planning-file-mutation-write-access"
		workspacePath = "Workflow/demo"
		managedPath   = workspacePath + "/planning/changelog/changelog-test.json"
	)
	workspacepkg.SetSessionFolderGuard(sessionID, []string{workspacePath}, []string{workspacePath})
	workspacepkg.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{workspacePath + "/planning"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)

	client := workspacepkg.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	managedCtx := withPlanningFileMutationWriteAccess(ctx, workspacePath, "changelog/changelog-test.json")

	if err := client.ValidatePathWithContext(managedCtx, managedPath, true); err != nil {
		t.Fatalf("exact managed planning file should be writable: %v", err)
	}
	for _, blockedSibling := range []string{
		workspacePath + "/planning/plan.json",
		workspacePath + "/planning/changelog/other.json",
	} {
		if err := client.ValidatePathWithContext(managedCtx, blockedSibling, true); err == nil || !strings.Contains(err.Error(), "blocked for writes") {
			t.Fatalf("managed file capability unlocked sibling %s: %v", blockedSibling, err)
		}
	}
	if err := client.ValidatePathWithContext(ctx, managedPath, true); err == nil || !strings.Contains(err.Error(), "blocked for writes") {
		t.Fatalf("managed file capability leaked into caller context: %v", err)
	}
}

func TestWritePlanChangelogEntryUsesManagedFileAccess(t *testing.T) {
	const (
		sessionID     = "plan-changelog-managed-write-access"
		workspacePath = "Workflow/demo"
		filename      = "changelog-test.json"
		changelogPath = workspacePath + "/planning/changelog/" + filename
	)
	workspacepkg.SetSessionFolderGuard(sessionID, []string{workspacePath}, []string{workspacePath})
	workspacepkg.SetSessionFolderGuardBlockedWritePaths(sessionID, []string{workspacePath + "/planning"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)

	planChangelogSessionMutex.Lock()
	previousFile := planChangelogSessionFile
	previousStart := planChangelogSessionStart
	planChangelogSessionFile = filename
	planChangelogSessionStart = time.Now().UTC()
	planChangelogSessionMutex.Unlock()
	t.Cleanup(func() {
		planChangelogSessionMutex.Lock()
		planChangelogSessionFile = previousFile
		planChangelogSessionStart = previousStart
		planChangelogSessionMutex.Unlock()
	})

	client := workspacepkg.NewClient("http://unused")
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	ctx = context.WithValue(ctx, common.UserIDKey, "user-123")
	ctx = context.WithValue(ctx, common.UsernameKey, "erin")
	ctx = withPlanChangeOrigin(ctx, "workflow-builder")
	wrote := false
	var written string
	err := writePlanChangelogEntry(
		ctx,
		workspacePath,
		PlanChangelogEntry{Tool: "update_step_config", Reason: "test managed changelog write"},
		func(context.Context, string) (string, error) { return "", errors.New("not found") },
		func(writeCtx context.Context, path, content string) error {
			wrote = true
			written = content
			if path != changelogPath {
				t.Fatalf("unexpected changelog path: %s", path)
			}
			return client.ValidatePathWithContext(writeCtx, path, true)
		},
		loggerv2.NewNoop(),
	)
	if err != nil {
		t.Fatalf("typed changelog write was blocked: %v", err)
	}
	if !wrote {
		t.Fatal("typed changelog writer was not called")
	}
	var changelog PlanChangelog
	if err := json.Unmarshal([]byte(written), &changelog); err != nil {
		t.Fatalf("decode written changelog: %v", err)
	}
	if len(changelog.Entries) != 1 || changelog.Entries[0].ChangeID == "" {
		t.Fatalf("missing stable change identity: %+v", changelog.Entries)
	}
	if changelog.Entries[0].Origin.Type != "user_chat" || changelog.Entries[0].Origin.SessionID != sessionID {
		t.Fatalf("origin = %+v", changelog.Entries[0].Origin)
	}
	if changelog.Entries[0].Origin.UserID != "user-123" || changelog.Entries[0].Origin.Username != "erin" {
		t.Fatalf("user attribution = %+v", changelog.Entries[0].Origin)
	}
	if err := client.ValidatePathWithContext(ctx, changelogPath, true); err == nil || !strings.Contains(err.Error(), "blocked for writes") {
		t.Fatalf("changelog capability leaked into caller context: %v", err)
	}
}

func TestParsePlanDependencySurfaceReviewsRequiresEverySurface(t *testing.T) {
	complete := map[string]interface{}{}
	for _, surface := range requiredPlanDependencySurfaces {
		complete[surface] = map[string]interface{}{
			"disposition": "already_compatible",
			"evidence":    []interface{}{"checked the canonical consumer"},
		}
	}
	got, err := parsePlanDependencySurfaceReviews(complete)
	if err != nil || len(got) != len(requiredPlanDependencySurfaces) {
		t.Fatalf("complete surface review rejected: reviews=%+v err=%v", got, err)
	}
	delete(complete, "reporting")
	if _, err := parsePlanDependencySurfaceReviews(complete); err == nil || !strings.Contains(err.Error(), "reporting is required") {
		t.Fatalf("missing reporting surface was accepted: %v", err)
	}
}

func TestParsePlanDependencySurfaceReviewsRequiresIssueForUnresolvedSurface(t *testing.T) {
	complete := map[string]interface{}{}
	for _, surface := range requiredPlanDependencySurfaces {
		complete[surface] = map[string]interface{}{
			"disposition": "already_compatible",
			"evidence":    []interface{}{"checked the canonical consumer"},
		}
	}
	complete["reporting"] = map[string]interface{}{
		"disposition": "broken",
		"evidence":    []interface{}{"dashboard still reads the retired field"},
	}
	if _, err := parsePlanDependencySurfaceReviews(complete); err == nil || !strings.Contains(err.Error(), "durable Pulse issue") {
		t.Fatalf("broken surface without lifecycle issue was accepted: %v", err)
	}
	complete["reporting"].(map[string]interface{})["issue_ids"] = []interface{}{"PUL-1234ABCD"}
	got, err := parsePlanDependencySurfaceReviews(complete)
	if err != nil || len(got["reporting"].IssueIDs) != 1 {
		t.Fatalf("linked broken surface rejected: reviews=%+v err=%v", got, err)
	}
}

func TestManagedRetentionManifestWriteIsScoped(t *testing.T) {
	hcpo, dir := newPreValidationConcernTestOrchestrator(t)
	const sessionID = "retention-config-write"
	root := hcpo.GetWorkspacePath()
	workspacepkg.SetSessionFolderGuard(sessionID, []string{root}, []string{root + "/runs"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	manifest := `{"run_retention_count":10,"label":"keep"}`
	if err := hcpo.WriteWorkspaceFile(ctx, "workflow.json", manifest); err == nil {
		t.Fatal("raw manifest write must stay denied")
	}
	if err := hcpo.writeManagedWorkflowManifest(ctx, manifest); err != nil {
		t.Fatalf("managed retention write failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "workflow.json"))
	if err != nil || string(data) != manifest {
		t.Fatalf("manifest not persisted: %s %v", data, err)
	}
	for _, p := range []string{"workflow.json", "soul/soul.md", "../other/workflow.json"} {
		if err := hcpo.WriteWorkspaceFile(ctx, p, manifest); err == nil {
			t.Fatalf("managed capability leaked to raw %s", p)
		}
	}
}

// Live on social-media 2026-09-22 (PLAT-304 recurrence): the typed config
// tool's retention write succeeded through the managed path, but the follow-up
// persistWorkflowConfigToManifest rewrote workflow.json with a raw write, was
// denied in the background session, and failed the whole tool call.
func TestPersistWorkflowConfigToManifestUsesManagedWrite(t *testing.T) {
	hcpo, dir := newPreValidationConcernTestOrchestrator(t)
	const sessionID = "persist-config-write"
	root := hcpo.GetWorkspacePath()
	if err := os.WriteFile(filepath.Join(dir, "workflow.json"), []byte(`{"id":"wf","label":"keep","capabilities":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	workspacepkg.SetSessionFolderGuard(sessionID, []string{root}, []string{root + "/runs"})
	defer workspacepkg.ClearSessionShellConfig(sessionID)
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
	iwm := &InteractiveWorkshopManager{controller: hcpo}
	if err := iwm.persistWorkflowConfigToManifest(ctx, loggerv2.NewNoop()); err != nil {
		t.Fatalf("persisting workflow config in a guarded session failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "workflow.json"))
	if err != nil || !strings.Contains(string(data), `"label": "keep"`) || !strings.Contains(string(data), `"capabilities"`) {
		t.Fatalf("manifest not persisted with preserved fields: %s %v", data, err)
	}
	if err := hcpo.WriteWorkspaceFile(ctx, "workflow.json", "{}"); err == nil {
		t.Fatal("raw manifest write must stay denied")
	}
}
