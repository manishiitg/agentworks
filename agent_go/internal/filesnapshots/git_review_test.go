package filesnapshots

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-746-264: checkout copies and incoming Git moves must not become edits;
// a commit made before switching away must still have its exact saved diff.
func TestGitReviewKeepsBranchWorkAcrossSwitchesAndMultipleRepositories(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	all := func(string) bool { return true }
	f.repo("services/api", map[string]string{"same.txt": "base\n", "carried.txt": "base\n"})
	f.repo("tools/api", map[string]string{"same.txt": "other base\n"})
	f.git("services/api", "checkout", "-qb", "incoming")
	for i := 0; i < 30; i++ {
		f.put(fmt.Sprintf("services/api/copied-%02d.txt", i), "incoming\n")
	}
	f.put("services/api/same.txt", "incoming base\n")
	f.git("services/api", "add", "-A")
	f.git("services/api", "commit", "-qm", "incoming")
	f.git("services/api", "checkout", "-q", "main")
	f.put("services/api/carried.txt", "already dirty\n")
	if _, err := f.s.Begin(ctx, "switch"); err != nil {
		t.Fatal(err)
	}
	f.put("services/api/same.txt", "my main edit\n")
	f.git("services/api", "commit", "-qm", "work", "--", "same.txt")
	f.git("services/api", "checkout", "-q", "incoming")
	f.put("services/api/same.txt", "my incoming edit\n")
	f.put("tools/api/same.txt", "my other repo edit\n")
	r, err := f.s.Finish(ctx, "switch")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(ctx, []string{r.ID}, "", "", all)
	if err != nil || len(c.Changes) != 3 || c.Note != "" {
		t.Fatalf("switch comparison: %v %+v", err, c)
	}
	seen := map[string]bool{}
	for _, change := range c.Changes {
		if change.Checkout == nil || change.Key == "" {
			t.Fatalf("missing captured context: %+v", change)
		}
		label := change.Checkout
		seen[label.Root+":"+label.Branch] = true
		diff, err := f.s.CompareSaved(ctx, []string{r.ID}, change.Path, change.Key, all)
		if err != nil || !strings.Contains(diff.Diff, "+my ") || strings.Contains(diff.Diff, "copied-") {
			t.Fatalf("saved diff: %v %+v", err, diff)
		}
		if label.Root == "services/api" && label.Branch == "incoming" && !strings.Contains(diff.Diff, "-incoming base") {
			t.Fatalf("incoming destination was not the baseline: %s", diff.Diff)
		}
		// A version key is not authority to read its path.
		if _, err := f.s.CompareSaved(ctx, []string{r.ID}, change.Path, change.Key, func(string) bool { return false }); err == nil {
			t.Fatal("saved key bypassed the current file grant")
		}
		bytes, err := f.s.ReadComparedFile(ctx, diff, change.Path, "after")
		if err != nil || !strings.Contains(string(bytes), "my ") {
			t.Fatalf("saved file bytes: %v %q", err, bytes)
		}
	}
	if len(seen) != 3 || !seen["services/api:main"] || !seen["services/api:incoming"] || !seen["tools/api:main"] {
		t.Fatalf("checkout contexts: %v", seen)
	}
	// Creating a new branch carries the existing dirty files without edits.
	if _, err := f.s.Begin(ctx, "branch-only"); err != nil {
		t.Fatal(err)
	}
	f.git("services/api", "checkout", "-qb", "renamed-context")
	next, err := f.s.Finish(ctx, "branch-only")
	if err != nil {
		t.Fatal(err)
	}
	if c, err := f.s.Compare(ctx, next.ID, "", all); err != nil || len(c.Changes) != 0 || c.Note != "" {
		t.Fatalf("branch creation counted carried dirty files: %v %+v", err, c)
	}
	// A new chat reply on the same path has a distinct, selectable saved diff.
	if _, err := f.s.Begin(ctx, "later"); err != nil {
		t.Fatal(err)
	}
	f.put("services/api/same.txt", "later branch edit\n")
	later, err := f.s.Finish(ctx, "later")
	if err != nil {
		t.Fatal(err)
	}
	// Historical diffs and grouping survive current checkout removal and GC.
	if err := os.RemoveAll(filepath.Join(f.s.Root, "services")); err != nil {
		t.Fatal(err)
	}
	first, _ := f.s.read(r.ID)
	first.StartedAt = time.Now().Add(-16 * 24 * time.Hour)
	if err := f.s.save(first); err != nil {
		t.Fatal(err)
	}
	next, _ = f.s.read(next.ID)
	next.StartedAt = time.Now().Add(-15 * 24 * time.Hour)
	if err := f.s.save(next); err != nil {
		t.Fatal(err)
	}
	f.s.dropViews(ctx)
	f.s.prune(ctx)
	if _, err := f.s.read(next.ID); err != nil {
		t.Fatal("retention removed the zero-diff branch-switch baseline", err)
	}
	if err := f.s.command(ctx, "gc", "--prune=now", "--quiet").Run(); err != nil {
		t.Fatal(err)
	}
	history, err := f.s.CompareSaved(ctx, []string{r.ID, next.ID, later.ID}, "", "", all)
	if err != nil || len(history.Changes) != 4 {
		t.Fatalf("historical checkout work lost: %v %+v", err, history)
	}
	for _, change := range history.Changes {
		if _, err := f.s.CompareSaved(ctx, []string{r.ID, next.ID, later.ID}, change.Path, change.Key, all); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGitReviewIncomingResetAndMissingReflogDoNotInventChanges(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	all := func(string) bool { return true }
	f.repo("app", map[string]string{"a.txt": "original\n"})
	f.git("app", "checkout", "-qb", "upstream")
	f.put("app/a.txt", "incoming\n")
	f.put("app/incoming.txt", "incoming\n")
	f.git("app", "add", "-A")
	f.git("app", "commit", "-qm", "incoming")
	f.git("app", "checkout", "-q", "main")
	for _, operation := range []string{"merge", "reset", "clean-reset", "soft-reset", "missing-log"} {
		if operation == "clean-reset" {
			f.put("app/a.txt", "already dirty\n")
		}
		if _, err := f.s.Begin(ctx, operation); err != nil {
			t.Fatal(err)
		}
		switch operation {
		case "merge":
			f.git("app", "merge", "--ff-only", "upstream")
		case "reset":
			f.git("app", "reset", "--hard", "HEAD~1")
		case "clean-reset":
			f.git("app", "reset", "--hard", "HEAD")
		case "soft-reset":
			f.git("app", "reset", "--soft", "upstream")
		case "missing-log":
			f.git("app", "reflog", "expire", "--expire=now", "--all")
			f.git("app", "checkout", "-q", "upstream")
		}
		r, err := f.s.Finish(ctx, operation)
		if err != nil {
			t.Fatal(err)
		}
		c, err := f.s.Compare(ctx, r.ID, "", all)
		if err != nil || len(c.Changes) != 0 || (operation == "missing-log" || operation == "soft-reset") != strings.Contains(c.Note, "Incomplete") {
			t.Fatalf("%s invented work: %v %+v", operation, err, c)
		}
	}
}
