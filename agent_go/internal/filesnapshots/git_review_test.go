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
	for _, group := range GroupChanges(ctx, f.s.Root, c.Changes, c.NewRepos, all) {
		AddChatGitState(ctx, f.s.Root, &group, r.BeforeHeads[group.Root], all)
		if group.Root == "services/api" && group.Branch == "main" && (len(group.Uncommitted) != 0 || len(group.Commits) != 0 || group.CommitsKnown) {
			t.Fatalf("historical branch inherited current Git state: %+v", group)
		}
		if group.Root == "services/api" && group.Branch == "incoming" && len(group.Uncommitted) == 0 {
			t.Fatal("current branch lost its real Git state")
		}
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

// PR459: an initial dirty file restored before committing another file could
// be silently put back into the saved after-tree while crossing a checkout.
func TestGitReviewRestoredDirtyFileIsIncompleteAcrossSwitch(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	f.repo("app", map[string]string{"dirty.txt": "original\n", "work.txt": "original\n"})
	f.git("app", "branch", "other")
	f.put("app/dirty.txt", "already dirty\n")
	if _, err := f.s.Begin(ctx, "restore-and-switch"); err != nil {
		t.Fatal(err)
	}
	f.put("app/dirty.txt", "original\n")
	f.put("app/work.txt", "real work\n")
	f.git("app", "commit", "-qm", "work", "--", "work.txt")
	f.git("app", "checkout", "-q", "other")
	r, err := f.s.Finish(ctx, "restore-and-switch")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.Compare(ctx, r.ID, "", func(string) bool { return true })
	if err != nil || !strings.Contains(c.Note, "Incomplete") || len(c.Changes) != 1 || c.Changes[0].Path != "app/work.txt" {
		t.Fatalf("restoration was silently treated as complete: %v %+v", err, c)
	}
	denied, err := f.s.Compare(ctx, r.ID, "", func(string) bool { return false })
	if err != nil || denied.Note != "" || len(denied.Changes) != 0 {
		t.Fatalf("incomplete metadata bypassed current grants: %v %+v", err, denied)
	}
}

// PR460: returning a later branch's file to the initial branch's HEAD is a
// proved commit relative to that later branch, not an unstaged carried file.
func TestGitReviewKeepsCommittedWorkOnLaterSegments(t *testing.T) {
	for _, switchAway := range []bool{false, true} {
		t.Run(fmt.Sprint(switchAway), func(t *testing.T) {
			f := newCrew(t)
			ctx := t.Context()
			f.repo("app", map[string]string{"f.txt": "main original\n"})
			f.git("app", "checkout", "-qb", "other")
			f.put("app/f.txt", "other original\n")
			f.git("app", "commit", "-qam", "other base")
			f.git("app", "checkout", "-q", "main")
			f.put("app/f.txt", "already dirty\n")
			if _, err := f.s.Begin(ctx, "later-segment"); err != nil {
				t.Fatal(err)
			}
			f.put("app/f.txt", "main original\n")
			f.git("app", "checkout", "-q", "other")
			f.put("app/f.txt", "main original\n")
			f.git("app", "commit", "-qam", "proved work")
			if switchAway {
				f.git("app", "checkout", "-q", "main")
			}
			r, err := f.s.Finish(ctx, "later-segment")
			if err != nil {
				t.Fatal(err)
			}
			c, err := f.s.Compare(ctx, r.ID, "app/f.txt", func(string) bool { return true })
			if err != nil || len(c.Changes) != 1 || c.Changes[0].Checkout.Branch != "other" || !strings.Contains(c.Diff, "-other original") || !strings.Contains(c.Diff, "+main original") || strings.Contains(c.Diff, "already dirty") || c.Note != "" {
				t.Fatalf("known later commit was suppressed or rewritten: %v %+v", err, c)
			}
		})
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

// PLAT-746-286: identical filenames in a repo, its linked worktree and a
// nested repo must never share diffs or revert each other's saved edits.
func TestCheckoutScopedHistoryAndRevert(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	all := func(string) bool { return true }
	f.repo("app", map[string]string{"same.txt": "base\n"})
	f.git("app", "worktree", "add", "-q", "-b", "feature", "../feature")
	f.repo("app/nested", map[string]string{"same.txt": "nested base\n"})
	f.put("loose.txt", "loose base\n")
	if _, err := f.s.Begin(ctx, "shared-turn"); err != nil {
		t.Fatal(err)
	}
	f.put("app/same.txt", "main edit\n")
	f.put("feature/same.txt", "feature edit\n")
	f.put("app/nested/same.txt", "nested edit\n")
	f.put("loose.txt", "loose edit\n")
	r, err := f.s.Finish(ctx, "shared-turn")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(ctx, []string{r.ID}, "", "", all)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	var foreign Change
	for _, change := range c.Changes {
		if change.Checkout != nil {
			ids[change.Checkout.Root] = change.Checkout.ID
			if change.Checkout.Root == "feature" {
				foreign = change
			}
		}
	}
	if len(ids) != 3 || ids["app"] == ids["feature"] {
		t.Fatalf("distinct checkout identities missing: %v", ids)
	}
	scoped, err := f.s.CompareSaved(ctx, []string{r.ID}, "", "", all, ids["app"])
	if err != nil || len(scoped.Changes) != 1 || scoped.Changes[0].Path != "app/same.txt" {
		t.Fatalf("mixed checkout comparison: %v %+v", err, scoped)
	}
	if _, err := f.s.CompareSaved(ctx, []string{r.ID}, foreign.Path, foreign.Key, all, ids["app"]); err == nil {
		t.Fatal("foreign checkout key bypassed scope")
	}
	plan, err := f.s.ApplyUndo(ctx, r.ID, all, all, ids["app"])
	if err != nil || len(plan.Restore) != 1 {
		t.Fatalf("scoped revert: %v %+v", err, plan)
	}
	for file, want := range map[string]string{"app/same.txt": "base\n", "feature/same.txt": "feature edit\n", "app/nested/same.txt": "nested edit\n", "loose.txt": "loose edit\n"} {
		b, err := os.ReadFile(filepath.Join(f.s.Root, file))
		if err != nil || string(b) != want {
			t.Fatalf("revert crossed checkout at %s: %q %v", file, b, err)
		}
	}
	f.put("feature/same.txt", "later editor\n")
	plan, err = f.s.ApplyUndo(ctx, r.ID, all, all, ids["feature"])
	if err != nil || len(plan.Restore) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "changed_later" {
		t.Fatalf("later edit not preserved: %v %+v", err, plan)
	}
	f.git("app/nested", "checkout", "-qb", "other")
	plan, err = f.s.ApplyUndo(ctx, r.ID, all, all, ids["app/nested"])
	if err != nil || len(plan.Restore) != 0 || len(plan.Skipped) != 1 || plan.Skipped[0].Reason != "git_context_changed" {
		t.Fatalf("branch boundary not preserved: %v %+v", err, plan)
	}
}

// PLAT-746-286 / PR527: listing history must reuse saved metadata rather than
// replay every Git diff, without reusing a previous caller's file grants.
func TestSavedSummaryCachesHistoryWithoutBypassingCurrentGrants(t *testing.T) {
	f := newCrew(t)
	ctx := t.Context()
	f.put("visible.txt", "before\n")
	f.put("restricted.txt", "before\n")
	if _, err := f.s.Begin(ctx, "summary"); err != nil {
		t.Fatal(err)
	}
	f.put("visible.txt", "after\n")
	f.put("restricted.txt", "after\n")
	r, err := f.s.Finish(ctx, "summary")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := f.s.SavedSummary(ctx, r.ID, func(string) bool { return true })
	if err != nil || len(summary.Changes) != 2 {
		t.Fatalf("warm summary: %v %+v", err, summary)
	}
	// With Git unavailable, a repeated menu read must still use the completed
	// immutable summary and recheck the now narrower grant.
	t.Setenv("PATH", t.TempDir())
	summary, err = f.s.SavedSummary(ctx, r.ID, func(p string) bool { return p == "visible.txt" })
	if err != nil || len(summary.Changes) != 1 || summary.Changes[0].Path != "visible.txt" {
		t.Fatalf("cache hit lost grant or replayed Git: %v %+v", err, summary)
	}
	r.Status = "unavailable"
	if err := f.s.save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SavedSummary(ctx, r.ID, func(string) bool { return true }); err == nil {
		t.Fatal("stale cache hid an unavailable saved comparison")
	}
}
