package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserStagingDirectoriesAreDeploymentNamespaced(t *testing.T) {
	t.Setenv(browserStagingNamespaceEnv, "customer Production")
	t.Cleanup(func() {
		_ = os.RemoveAll(browserArtifactStagingDir())
		_ = os.RemoveAll(browserUploadStagingDir())
	})
	if got := browserArtifactStagingDir(); got != "/tmp/agentworks-browser-artifacts-customer-production" {
		t.Fatalf("artifact staging directory = %q", got)
	}
	if got := browserUploadStagingDir(); got != "/tmp/agentworks-browser-uploads-customer-production" {
		t.Fatalf("upload staging directory = %q", got)
	}
}

func TestPrepareBrowserArtifactRewritesNamedScreenshot(t *testing.T) {
	plan, err := prepareBrowserArtifact("screenshot", []string{"@e1", "Workflow/demo/evidence/login.png", "--full-page"}, "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Transfer == nil || !plan.FinalizeOnCall {
		t.Fatalf("expected immediate screenshot transfer plan, got %#v", plan)
	}
	if plan.Transfer.DestinationPath != "Workflow/demo/evidence/login.png" {
		t.Fatalf("destination = %q", plan.Transfer.DestinationPath)
	}
	if plan.RewrittenArgs[1] == plan.Transfer.DestinationPath {
		t.Fatal("screenshot destination was not rewritten to staging")
	}
	if !strings.Contains(plan.RewrittenArgs[1], browserArtifactStagingDir()) {
		t.Fatalf("staged path = %q", plan.RewrittenArgs[1])
	}
}

func TestPrepareBrowserArtifactLeavesNoTargetScreenshotAlone(t *testing.T) {
	plan, err := prepareBrowserArtifact("screenshot", []string{"--full-page"}, "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if plan != nil {
		t.Fatalf("no-target screenshot unexpectedly produced plan: %#v", plan)
	}
}

func TestPrepareBrowserArtifactRewritesDownloadDestination(t *testing.T) {
	plan, err := prepareBrowserArtifact("download", []string{"#export", "Workflow/demo/Downloads/report.csv"}, "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil || plan.Transfer == nil || plan.Transfer.Kind != "download" || !plan.Transfer.Finalize {
		t.Fatalf("download plan = %#v", plan)
	}
	if plan.RewrittenArgs[1] == plan.Transfer.DestinationPath || !strings.Contains(plan.RewrittenArgs[1], browserArtifactStagingDir()) {
		t.Fatalf("download path was not rewritten to staging: %#v", plan.RewrittenArgs)
	}
}

func TestPrepareBrowserUploadsPreservesNamesAndRewritesEveryInput(t *testing.T) {
	plan, err := prepareBrowserUploads("upload", []string{"#files", "Workflow/demo/input/report.pdf", "Downloads/photo.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupBrowserUploadPlan(plan)
	if plan == nil || len(plan.Transfers) != 2 || len(plan.RewrittenArgs) != 3 {
		t.Fatalf("upload plan = %#v", plan)
	}
	for i, transfer := range plan.Transfers {
		if transfer.SourcePath == transfer.StagedPath || !strings.Contains(transfer.StagedPath, browserUploadStagingDir()) {
			t.Fatalf("upload transfer %d = %#v", i, transfer)
		}
		if filepath.Base(transfer.SourcePath) != filepath.Base(transfer.StagedPath) {
			t.Fatalf("upload filename changed: %#v", transfer)
		}
		if plan.RewrittenArgs[i+1] != transfer.StagedPath {
			t.Fatalf("rewritten upload args = %#v", plan.RewrittenArgs)
		}
	}
}

func TestPrepareBrowserArtifactCarriesRecordingLeaseToStop(t *testing.T) {
	owner, session := "record-owner", "record-session"
	key := browserArtifactLeaseKey(owner, session, "record")
	deleteBrowserArtifactLease(key)
	t.Cleanup(func() { deleteBrowserArtifactLease(key) })

	start, err := prepareBrowserArtifact("record", []string{"start", "Workflow/demo/evidence/run.webm"}, owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if start == nil || !start.StoreLeaseOnSuccess || start.FinalizeOnCall {
		t.Fatalf("unexpected start plan: %#v", start)
	}
	setBrowserArtifactLease(key, browserArtifactLease{Transfer: start.Transfer, RequestedPath: start.RequestedPath})

	stop, err := prepareBrowserArtifact("record", []string{"stop"}, owner, session)
	if err != nil {
		t.Fatal(err)
	}
	if stop == nil || !stop.FinalizeOnCall || !stop.DeleteLeaseOnSuccess {
		t.Fatalf("unexpected stop plan: %#v", stop)
	}
	if !stop.Transfer.Finalize || start.Transfer.Finalize {
		t.Fatalf("record transfer finalization flags: start=%v stop=%v", start.Transfer.Finalize, stop.Transfer.Finalize)
	}
	if stop.Transfer.SourcePath != start.Transfer.SourcePath || stop.Transfer.DestinationPath != start.Transfer.DestinationPath {
		t.Fatalf("stop transfer %#v does not match start %#v", stop.Transfer, start.Transfer)
	}
}
