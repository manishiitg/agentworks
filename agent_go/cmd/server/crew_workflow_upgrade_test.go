package server

import (
	"context"
	"os"
	"path/filepath"
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
