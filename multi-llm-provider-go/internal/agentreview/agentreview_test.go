package agentreview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeTB captures whether RequireReviewed gated (called Fatalf) without failing
// the real test. Embeds testing.TB so it satisfies the interface (including the
// unexported method); only the methods RequireReviewed uses are overridden.
type fakeTB struct {
	testing.TB
	fatal bool
}

func (f *fakeTB) Helper()                 {}
func (f *fakeTB) Logf(string, ...any)     {}
func (f *fakeTB) Fatalf(string, ...any)   { f.fatal = true }
func (f *fakeTB) Errorf(string, ...any)   {}
func (f *fakeTB) Fatal(...any)            { f.fatal = true }

func approve(t *testing.T, dir, test, fingerprint string) {
	t.Helper()
	path := filepath.Join(dir, test+".json")
	var rec Record
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	rec.Review.Verdict = "good"
	rec.Review.ReviewedFingerprint = fingerprint
	rec.Review.Reviewer = "test"
	out, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestRequireReviewedGate proves the agentic gate: unreviewed output fails,
// an agent's sign-off on the current fingerprint passes, and a behavior change
// (new fingerprint) invalidates the sign-off and fails again — forcing re-review.
func TestRequireReviewedGate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MLP_AGENT_REVIEW_DIR", dir)
	t.Setenv("MLP_AGENT_REVIEW_CAPTURE", "") // gating mode, not capture

	// 1. First capture, no review yet -> gate FAILS.
	rec := Write(t, "demo", "summary", map[string]any{"out": "x"}, map[string]any{"shape": "A"})
	f1 := &fakeTB{}
	RequireReviewed(f1, rec)
	if !f1.fatal {
		t.Fatal("expected gate to FAIL on unreviewed output")
	}

	// 2. Agent approves the current fingerprint; re-capture preserves the review -> gate PASSES.
	approve(t, dir, "demo", rec.Fingerprint)
	rec2 := Write(t, "demo", "summary", map[string]any{"out": "x"}, map[string]any{"shape": "A"})
	if rec2.Review.Verdict != "good" {
		t.Fatalf("re-capture did not preserve the review: %+v", rec2.Review)
	}
	f2 := &fakeTB{}
	RequireReviewed(f2, rec2)
	if f2.fatal {
		t.Fatal("expected gate to PASS after approval of the current fingerprint")
	}

	// 3. Behavior changes (new shape -> new fingerprint); the stale review no
	//    longer matches -> gate FAILS, forcing a fresh agent review.
	rec3 := Write(t, "demo", "summary", map[string]any{"out": "y"}, map[string]any{"shape": "B"})
	if rec3.Fingerprint == rec.Fingerprint {
		t.Fatal("fingerprint should change when the shape changes")
	}
	f3 := &fakeTB{}
	RequireReviewed(f3, rec3)
	if !f3.fatal {
		t.Fatal("expected gate to FAIL after a behavior change invalidated the stale review")
	}
}
