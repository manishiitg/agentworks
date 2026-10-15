package browser

import "testing"

// A screenshot taken without a path inside a workflow step goes to the run's
// tool_output_folder, not the workflow root.
func TestDefaultScreenshotGoesToToolOutputFolder(t *testing.T) {
	plan := &browserArtifactPlan{Transfer: &ArtifactTransfer{}, RequestedPath: "screenshots/screenshot-1.png"}
	retargetDefaultScreenshot(plan, &FolderGuardConfig{Enabled: true, WritePaths: []string{"Workflow/demo/runs/r1", "Workflow/demo/tool_output_folder"}})
	if want := "Workflow/demo/tool_output_folder/screenshots/screenshot-1.png"; plan.RequestedPath != want || plan.Transfer.DestinationPath != want {
		t.Fatalf("got %q / %q, want %q", plan.RequestedPath, plan.Transfer.DestinationPath, want)
	}
	plain := &browserArtifactPlan{Transfer: &ArtifactTransfer{}, RequestedPath: "screenshots/screenshot-2.png"}
	retargetDefaultScreenshot(plain, &FolderGuardConfig{Enabled: true, WritePaths: []string{"Code/p"}})
	if plain.RequestedPath != "screenshots/screenshot-2.png" {
		t.Fatalf("a session without a tool_output_folder keeps its screenshots folder: %q", plain.RequestedPath)
	}
}
