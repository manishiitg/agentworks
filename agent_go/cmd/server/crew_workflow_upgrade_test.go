package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-396: one bad migration record must not apply the Crew stamp gate
// to an unmoved server's unrelated workflow or cut its scheduled work off.
func TestUnmovedWorkflowKeepsStartsWhenBindingMetadataIsUnreadable(t *testing.T) {
	root := serveContractGuardManifest(t, workflowContractSkillsVersion)
	ctx := context.Background()
	manifest, found, err := ReadWorkflowManifest(ctx, root)
	if err != nil || !found {
		t.Fatal(err)
	}
	if binding, err := findCrewExecutableByID(manifest.ID); err != nil || binding != nil {
		t.Fatalf("server without Crew data was treated as migrated: %v %v", binding, err)
	}
	directory, err := crewExecutablesPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "broken.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentWorkflowContractForManualRun(ctx, root); err != nil {
		t.Fatalf("unrelated broken binding stopped an unmoved workflow: %v", err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("unrelated broken binding stopped its webhook: %v", err)
	}
	// Force a directory-read error as well as a malformed JSON record. This
	// reaches the lookup error branch even when discovery skips bad records.
	if err := os.Rename(directory, directory+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(directory, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := directWebhookPreflight(manifest); err != nil {
		t.Fatalf("discovery error falsely marked an unmoved workflow as Crew-bound: %v", err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(directory+"-saved", directory); err != nil {
		t.Fatal(err)
	}
	draft := &productSurfaceDraft{}
	guard := workflowContractExecutionGuardRegistrar{definitionRegistrar: draft, workspacePath: root, scheduled: true}
	started := false
	if err := guard.RegisterCustomTool("run_full_workflow", "run", map[string]interface{}{}, func(context.Context, map[string]interface{}) (string, error) { started = true; return "started", nil }, "workflow"); err != nil {
		t.Fatal(err)
	}
	// Also exercise the schedule's exact-root lookup failure, rather than a
	// global discovery failure that could bypass the schedule branch entirely.
	file, err := crewExecutableFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := draft.tools["run_full_workflow"].exec(ctx, nil); err != nil || !started {
		t.Fatalf("binding lookup failure added a new schedule refusal: %v", err)
	}
}

// PLAT-746 review: plan edits are gone, so an older workflow that sits in a Crew must keep
// running on the contracts an unmoved one may, not wait for an update nobody can do.
// One too old to run is refused with the operator pointer, not "ask the chat".
func TestCrewBoundOlderWorkflowKeepsRunning(t *testing.T) {
	bound := func(t *testing.T, version string) (context.Context, string, *WorkflowManifest, crewExecutableBinding) {
		t.Helper()
		svc, files, ctx := newCrewCreationTestEnv(t)
		crew, err := svc.CreateCrewProject(ctx, CreateCrewRequest{UserID: "owner", WorkflowPath: "Workflow/build", Title: "Old workflow", Purpose: "Reports", IdempotencyKey: "old-" + version})
		if err != nil {
			t.Fatal(err)
		}
		manifest := NewWorkflowManifest("Old workflow")
		manifest.ID, manifest.Version, manifest.CodeLayoutVersion = "old-workflow", version, 1
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		const root = "Workflow/old-workflow"
		files.files[root+"/workflow.json"] = string(raw)
		binding := crewExecutableBinding{ID: manifest.ID, CrewID: crew.CrewID, CrewRoot: crew.WorkspacePath, Root: root, Kind: "workflow"}
		if err := registerCrewExecutable(binding); err != nil {
			t.Fatal(err)
		}
		read, found, err := ReadWorkflowManifest(ctx, root)
		if err != nil || !found {
			t.Fatalf("manifest of the bound workflow: %v %v", found, err)
		}
		return ctx, root, read, binding
	}
	t.Run("runs on the previous contract", func(t *testing.T) {
		ctx, root, manifest, binding := bound(t, workflowContractSkillsVersion)
		if err := requireCurrentWorkflowContractForManualRun(ctx, root, &binding); err != nil {
			t.Fatalf("a Crew workflow was refused for an update that cannot be done: %v", err)
		}
		if err := directWebhookPreflight(manifest); err != nil {
			t.Fatalf("its unattended start was refused: %v", err)
		}
	})
	t.Run("starts unattended on the contract before that", func(t *testing.T) {
		_, _, manifest, _ := bound(t, workflowContractStepDescriptionLayoutVersion)
		if err := directWebhookPreflight(manifest); err != nil {
			t.Fatalf("a Crew workflow got less than an unmoved one: %v", err)
		}
	})
	t.Run("two contracts behind is refused with the operator pointer", func(t *testing.T) {
		_, _, manifest, _ := bound(t, workflowContractManagedDBScriptsVersion)
		if err := directWebhookPreflight(manifest); !errors.Is(err, errWorkflowContractMigrationRequired) || !strings.Contains(err.Error(), "platform operator") {
			t.Fatalf("a workflow too old to run needs the operator pointer: %v", err)
		}
	})
}
