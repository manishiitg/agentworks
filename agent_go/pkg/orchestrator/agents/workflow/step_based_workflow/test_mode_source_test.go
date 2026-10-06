package step_based_workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A test run reads upstream outputs from a real run. The workshop's own folder
// (iteration-0) is a scratch run, so the default must be the most complete
// recent real run, as happened with bid-record on Upwork (PLAT-562).
func TestResolveTestSourceRunPrefersTheMostCompleteRealRun(t *testing.T) {
	wf := t.TempDir()
	step := func(run, name string) {
		dir := filepath.Join(wf, "runs", run, "daily-bid", "execution", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	step("iteration-0", "check-cdp") // scratch run: guard only
	for _, name := range []string{"check-cdp", "bid-pick-job", "bid-submit"} {
		step("iteration-26-sched", name)
	}
	step("test-123", "bid-submit") // an earlier test run is never a source
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(wf, "runs", "iteration-26-sched", "daily-bid", "execution"), old, old)

	got, err := resolveTestSourceRun(wf, "iteration-0/daily-bid", "")
	if err != nil || got != "iteration-26-sched/daily-bid" {
		t.Fatalf("default source = %q, %v; want the most complete real run", got, err)
	}
	if got, err := resolveTestSourceRun(wf, "iteration-0/daily-bid", "iteration-0/daily-bid"); err != nil || got != "iteration-0/daily-bid" {
		t.Fatalf("explicit source = %q, %v", got, err)
	}
	for _, bad := range []string{"../x/daily-bid", "test-123/daily-bid", "/etc", "iteration-9/daily-bid"} {
		if _, err := resolveTestSourceRun(wf, "iteration-0/daily-bid", bad); err == nil {
			t.Fatalf("source_run %q must be rejected", bad)
		}
	}
}
