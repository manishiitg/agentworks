package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPulseReviewReportsIncludeExistingCheckpointsOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	workspace := "Workflow/reports"
	run := filepath.Join(root, workspace, "runs", "pulse", "run-1")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "strategic-review.md"), []byte("# In-progress review"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(run, "strategic-review.md"), filepath.Join(run, "technical-review.md")); err != nil {
		t.Fatal(err)
	}
	reports, err := listPulseReviewReports(workspace, "")
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %#v, %v", reports, err)
	}
	if reports[0].Path != workspace+"/runs/pulse/run-1/strategic-review.md" || reports[0].UpdatedAt == "" {
		t.Fatalf("bad report: %#v", reports[0])
	}
	reports, err = listPulseReviewReports(workspace, "technical_review")
	if err != nil || len(reports) != 0 {
		t.Fatalf("unexpected report %#v %v", reports, err)
	}
	if _, err = listPulseReviewReports("../escape", ""); err == nil {
		t.Fatal("expected invalid workspace rejection")
	}
}
