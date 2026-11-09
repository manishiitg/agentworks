package filesnapshots

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// PLAT-746-319: the old reply capture enumerated 104k files even on no-op
// turns. Capture changed paths, including edits committed before completion,
// without writing the user's index or copying clean/ignored project contents.
func TestGitTurnCapturesOnlyChangedPathsAndCommittedEdits(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{"clean.txt": "original\n", "dirty.txt": "committed\n", "untouched.txt": strings.Repeat("large clean file\n", 10000), ".gitignore": "node_modules/\n"})
	f.put("app/dirty.txt", "before reply\n")
	f.put("app/node_modules/generated.js", "ignored\n")
	dir := filepath.Join(f.s.Root, "app")
	index := filepath.Join(dir, ".git", "index")
	beforeIndex, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.s.BeginGitTurn(t.Context(), "turn")
	if err != nil {
		t.Fatal(err)
	}
	if r.BeforeCapture.Captured != 1 || r.BeforeCapture.WalkedFiles != 0 {
		t.Fatalf("whole workspace captured: %+v", r.BeforeCapture)
	}
	if got := f.tree(r.Before); len(got) != 1 || got["app/dirty.txt"] != "before reply\n" {
		t.Fatalf("before: %+v", got)
	}
	afterIndex, _ := os.ReadFile(index)
	if string(beforeIndex) != string(afterIndex) {
		t.Fatal("capture modified project index")
	}
	f.put("app/dirty.txt", "after reply\n")
	f.put("app/clean.txt", "changed then committed\n")
	f.put("app/new.txt", "new and committed\n")
	f.git("app", "add", "-A")
	f.git("app", "commit", "--quiet", "-m", "during reply")
	r, err = f.s.FinishGitTurn(t.Context(), "turn")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Changes) != 3 {
		t.Fatalf("want three real changes, got %+v", c.Changes)
	}
	for _, change := range c.Changes {
		diff, e := f.s.CompareSaved(t.Context(), []string{r.ID}, change.Path, change.Key, func(string) bool { return true })
		if e != nil {
			t.Fatal(e)
		}
		if change.Path == "app/dirty.txt" && (!strings.Contains(diff.Diff, "-before reply") || !strings.Contains(diff.Diff, "+after reply")) {
			t.Fatalf("dirty baseline lost: %s", diff.Diff)
		}
	}
	if _, err := f.s.CompareSaved(t.Context(), []string{r.ID}, c.Changes[0].Path, c.Changes[0].Key, func(string) bool { return false }); err == nil {
		t.Fatal("revoked file exposed")
	}
	if got := f.tree(r.After); len(got) != 3 {
		t.Fatalf("clean/ignored files imported: %+v", got)
	}
	// PLAT-746-319 review: new captures must match the live checkout identity used by Undo.
	plan, err := f.s.PlanUndo(t.Context(), r.ID, func(string) bool { return true }, func(string) bool { return true })
	if err != nil || len(plan.Restore) != 3 || len(plan.Skipped) != 0 {
		t.Fatalf("Undo rejected unchanged checkout: %v %+v", err, plan)
	}

	if _, err := f.s.BeginGitTurn(t.Context(), "clean"); err != nil {
		t.Fatal(err)
	}
	clean, err := f.s.FinishGitTurn(t.Context(), "clean")
	if err != nil || !f.s.Unchanged(t.Context(), clean) || len(clean.Review.Spans) != 0 {
		t.Fatalf("no-op turn: %v %+v", err, clean)
	}
}

// PLAT-746-319: a file left uncommitted across replies must reuse its private
// blob; only a subsequent content edit imports bytes, including same-size edits.
func TestDirtyTurnReusesPrivateIndexAcrossReplies(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprint(linked), func(t *testing.T) {
			f := newCrew(t)
			f.repo("app", map[string]string{"a.txt": "base-a\n", "b.txt": "base-b\n"})
			dir := "app"
			if linked {
				dir = "feature"
				f.git("app", "worktree", "add", "--quiet", "-b", "feature", filepath.Join(f.s.Root, dir))
			}
			f.put(dir+"/a.txt", "dirty-a\n")
			f.put(dir+"/b.txt", "dirty-b\n")
			f.backdateAll()
			begin := func(id string) Record {
				t.Helper()
				r, err := f.s.BeginGitTurn(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			finish := func(id string) Record {
				t.Helper()
				r, err := f.s.FinishGitTurn(t.Context(), id)
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
			assertReused := func(stats CaptureStats) {
				t.Helper()
				if stats.ImportedBytes != 0 || stats.Changed != 0 || stats.Reused < 2 {
					t.Fatalf("stable dirty files imported again: %+v", stats)
				}
			}
			begin("seed")
			assertReused(finish("seed").AfterCapture)
			assertReused(begin("noop").BeforeCapture)
			noop := finish("noop")
			assertReused(noop.AfterCapture)
			if !f.s.Unchanged(t.Context(), noop) {
				t.Fatal("unchanged dirty files became reply edits")
			}

			assertReused(begin("same-size").BeforeCapture)
			f.put(dir+"/a.txt", "dirty-z\n")
			edited := finish("same-size")
			if edited.AfterCapture.ImportedBytes != int64(len("dirty-z\n")) || edited.AfterCapture.Changed != 1 || edited.AfterCapture.Reused < 1 {
				t.Fatalf("did not import only the edited dirty file: %+v", edited.AfterCapture)
			}
			all := func(string) bool { return true }
			c, err := f.s.CompareSaved(t.Context(), []string{edited.ID}, "", "", all)
			if err != nil || len(c.Changes) != 1 || c.Changes[0].Path != dir+"/a.txt" {
				t.Fatalf("wrong reply changes: %v %+v", err, c.Changes)
			}
			diff, err := f.s.CompareSaved(t.Context(), []string{edited.ID}, c.Changes[0].Path, c.Changes[0].Key, all)
			if err != nil || !strings.Contains(diff.Diff, "-dirty-a") || !strings.Contains(diff.Diff, "+dirty-z") {
				t.Fatalf("lost private before bytes: %v %s", err, diff.Diff)
			}
			if plan, err := f.s.ApplyUndo(t.Context(), edited.ID, all, all); err != nil || len(plan.Restore) != 1 {
				t.Fatalf("Undo lost cached version: %v %+v", err, plan)
			}
			begin("after-undo")
			assertReused(finish("after-undo").AfterCapture)

			assertReused(begin("commit-only").BeforeCapture)
			f.git(dir, "add", "a.txt", "b.txt")
			f.git(dir, "commit", "--quiet", "-m", "commit existing dirty files")
			index, err := gitIn(t.Context(), f.s.Root, filepath.Join(f.s.Root, dir), "rev-parse", "--git-path", "index").Output()
			if err != nil {
				t.Fatal(err)
			}
			indexPath := strings.TrimSpace(string(index))
			if !filepath.IsAbs(indexPath) {
				indexPath = filepath.Join(f.s.Root, dir, indexPath)
			}
			beforeIndex, err := os.ReadFile(indexPath)
			if err != nil {
				t.Fatal(err)
			}
			committed := finish("commit-only")
			assertReused(committed.AfterCapture)
			if !f.s.Unchanged(t.Context(), committed) {
				t.Fatal("commit without file edits created changes")
			}
			afterIndex, err := os.ReadFile(indexPath)
			if err != nil || string(beforeIndex) != string(afterIndex) {
				t.Fatal("capture wrote the user's index")
			}

			f.put(dir+"/a.txt", "left-dirty-a\n")
			f.put(dir+"/b.txt", "left-dirty-b\n")
			begin("seed-restore")
			finish("seed-restore")
			assertReused(begin("restore-head").BeforeCapture)
			f.git(dir, "restore", "--", "a.txt")
			restored := finish("restore-head")
			if restored.AfterCapture.ImportedBytes != int64(len("dirty-a\n")) || restored.AfterCapture.Changed != 1 {
				t.Fatalf("restore-to-HEAD lost or reread other dirty files: %+v", restored.AfterCapture)
			}
			c, err = f.s.CompareSaved(t.Context(), []string{restored.ID}, "", "", all)
			if err != nil || len(c.Changes) != 1 || c.Changes[0].Path != dir+"/a.txt" {
				t.Fatalf("restore-to-HEAD comparison: %v %+v", err, c.Changes)
			}
		})
	}
}

// PLAT-746-319: raw candidate discovery must retain staged renames/deletions
// and untracked literal paths while leaving ignored output out of the cache.
func TestTurnCandidatePathsKeepStagedDeletedAndUntrackedFiles(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{"main.txt": "before\n", "old.txt": "renamed\n", "removed.txt": "deleted\n"})
	f.git("app", "mv", "old.txt", "renamed\tfile.txt")
	f.put("app/main.txt", "staged\n")
	f.git("app", "add", "main.txt")
	f.put("app/main.txt", "edited\n")
	if err := os.Remove(filepath.Join(f.s.Root, "app", "removed.txt")); err != nil {
		t.Fatal(err)
	}
	f.put("app/new\nfile.txt", "new\n")
	f.put("app/.gitignore", "*.log\n")
	f.put("app/ignored.log", "ignored\n")
	f.backdateAll()
	r, err := f.s.BeginGitTurn(t.Context(), "paths")
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, p := range r.GitTurn["app"].Dirty {
		paths[p] = true
	}
	for _, p := range []string{"main.txt", "old.txt", "renamed\tfile.txt", "removed.txt", "new\nfile.txt", ".gitignore"} {
		if !paths[p] {
			t.Fatalf("lost literal candidate %q: %+v", p, paths)
		}
	}
	if paths["ignored.log"] {
		t.Fatal("ignored output became a candidate")
	}
	if got := f.tree(r.Before); got["app/main.txt"] != "edited\n" || got["app/renamed\tfile.txt"] != "renamed\n" {
		t.Fatalf("not the raw working-tree baseline: %+v", got)
	}
	r, err = f.s.FinishGitTurn(t.Context(), "paths")
	if err != nil || r.AfterCapture.ImportedBytes != 0 || !f.s.Unchanged(t.Context(), r) {
		t.Fatalf("unchanged staged work reread or became edits: %v %+v", err, r.AfterCapture)
	}
}

func TestGitTurnRefusesConvertedBeforeBytesAndExternalGitDir(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{".gitattributes": "*.txt text\n", "a.txt": "original\n"})
	if _, err := f.s.BeginGitTurn(t.Context(), "converted"); err != nil {
		t.Fatal(err)
	}
	f.put("app/a.txt", "edited\n")
	r, err := f.s.FinishGitTurn(t.Context(), "converted")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "partial" || len(r.Skipped) != 1 || r.Skipped[0] != "app/a.txt" {
		t.Fatalf("invented raw before: %+v", r)
	}
	external := newCrew(t)
	external.repo("outside", map[string]string{"private.txt": "private contents"})
	f.put("external/.git", "gitdir: "+filepath.Join(external.s.Root, "outside", ".git")+"\n")
	if r, err := f.s.BeginGitTurn(t.Context(), "external"); err != nil {
		t.Fatal(err)
	} else if _, found := r.GitTurn["external"]; found {
		t.Fatal("checkout metadata outside the project was accepted")
	}
}

func TestGitTurnEmptyRepositoryAndNewWorktree(t *testing.T) {
	f := newCrew(t)
	f.put("fresh/new.txt", "initial\n")
	f.git("fresh", "init", "--quiet")
	// A checkout Git cannot read (broken .git folder) is skipped, not a failed reply.
	broken := filepath.Join(f.s.Root, "broken")
	if err := os.MkdirAll(filepath.Join(broken, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.BeginGitTurn(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	f.git("fresh", "add", "-A")
	f.git("fresh", "commit", "--quiet", "-m", "first")
	f.put("fresh/new.txt", "edited\n")
	r, err := f.s.FinishGitTurn(t.Context(), "first")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
	if err != nil || len(c.Changes) != 1 || c.Changes[0].Status != "M" {
		t.Fatalf("unborn before bytes lost: %v %+v", err, c)
	}
	// A newly discovered checkout with a pre-existing commit is not a whole
	// project of added files. It really appears after the before-capture.
	if _, err := f.s.BeginGitTurn(t.Context(), "new-worktree"); err != nil {
		t.Fatal(err)
	}
	f.git("fresh", "worktree", "add", "--quiet", "-b", "second", filepath.Join(f.s.Root, "second"))
	r, err = f.s.FinishGitTurn(t.Context(), "new-worktree")
	if err != nil {
		t.Fatal(err)
	}
	c, err = f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
	if err != nil || len(c.Changes) != 0 {
		t.Fatalf("checkout contents counted as edits: %v %+v", err, c)
	}
}

// PLAT-746-319 review: a repository created and committed within one reply has no starting HEAD.
func TestGitTurnCapturesFirstCommitCreatedDuringReply(t *testing.T) {
	f := newCrew(t)
	if err := os.MkdirAll(f.s.Root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.BeginGitTurn(t.Context(), "create"); err != nil {
		t.Fatal(err)
	}
	f.repo("new-project", map[string]string{"app.ts": "export const answer = 42\n"})
	r, err := f.s.FinishGitTurn(t.Context(), "create")
	if err != nil {
		t.Fatal(err)
	}
	got := f.tree(r.After)
	if got["new-project/app.ts"] != "export const answer = 42\n" {
		t.Fatalf("initial commit missing: %+v", got)
	}
}

// PLAT-746-319: capture discovery skipped linked worktrees, and ordinary files
// had no path at all. Both must produce private diffs and scoped Undo.
func TestTurnDiscoversLinkedWorktreeAndOrdinaryFiles(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "ordinary"
		if linked {
			name = "linked-worktree"
		}
		t.Run(name, func(t *testing.T) {
			f := newCrew(t)
			dir := "notes"
			if linked {
				f.repo("app", map[string]string{"a.txt": "before\n", "gone.txt": "removed\n"})
				dir = "feature"
				f.git("app", "worktree", "add", "--quiet", "-b", "feature", filepath.Join(f.s.Root, dir))
			} else {
				f.put(dir+"/a.txt", "before\n")
				f.put(dir+"/gone.txt", "removed\n")
			}
			f.put(dir+"/.env.local", "excluded credential")
			f.put("node_modules/cache.txt", "excluded dependency")
			f.put("unchanged.txt", strings.Repeat("unchanged file\n", 4096))
			f.backdateAll()
			if _, err := f.s.BeginGitTurn(t.Context(), "reply"); err != nil {
				t.Fatal(err)
			}
			f.put(dir+"/a.txt", "after\n")
			f.put(dir+"/new.txt", "added\n")
			if err := os.Remove(filepath.Join(f.s.Root, dir, "gone.txt")); err != nil {
				t.Fatal(err)
			}
			if linked {
				f.git(dir, "add", "a.txt", "new.txt", "gone.txt")
				f.git(dir, "commit", "--quiet", "-m", "edit during reply")
			}
			r, err := f.s.FinishGitTurn(t.Context(), "reply")
			if err != nil {
				t.Fatal(err)
			}
			all := func(string) bool { return true }
			c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", all)
			if err != nil || len(c.Changes) != 3 {
				t.Fatalf("missing file changes: %v %+v", err, c)
			}
			for _, change := range c.Changes {
				if linked && (change.Checkout == nil || change.Checkout.Root != dir || change.Checkout.Branch != "feature") {
					t.Fatalf("lost worktree context: %+v", change)
				}
				if !linked && change.Checkout != nil {
					t.Fatalf("ordinary file needs no Git: %+v", change)
				}
			}
			for _, tree := range []string{r.Before, r.After} {
				for p := range f.tree(tree) {
					if strings.Contains(p, ".env") || strings.Contains(p, "node_modules") {
						t.Fatalf("excluded file captured: %s", p)
					}
				}
			}
			if plan, err := f.s.ApplyUndo(t.Context(), r.ID, all, all); err != nil || len(plan.Restore) != 3 || len(plan.Skipped) != 0 {
				t.Fatalf("Undo lost saved files: %v %+v", err, plan)
			}
			if b, err := os.ReadFile(filepath.Join(f.s.Root, dir, "a.txt")); err != nil || string(b) != "before\n" {
				t.Fatalf("wrong restored bytes: %v %q", err, b)
			}
			if _, err := os.Stat(filepath.Join(f.s.Root, dir, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("added file not removed: %v", err)
			}
			if b, err := os.ReadFile(filepath.Join(f.s.Root, dir, "gone.txt")); err != nil || string(b) != "removed\n" {
				t.Fatalf("deleted file not restored: %v %q", err, b)
			}
			if !linked {
				// Undo changed metadata; settle that once, then an unchanged turn
				// must reuse private blobs instead of rereading the loose files.
				f.backdateAll()
				if _, err := f.s.BeginGitTurn(t.Context(), "settle"); err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.FinishGitTurn(t.Context(), "settle"); err != nil {
					t.Fatal(err)
				}
				warm, err := f.s.BeginGitTurn(t.Context(), "unchanged")
				if err != nil || warm.BeforeCapture.ImportedBytes != 0 || warm.BeforeCapture.Reused < 3 {
					t.Fatalf("unchanged ordinary files reread: %v %+v", err, warm.BeforeCapture)
				}
				warm, err = f.s.FinishGitTurn(t.Context(), "unchanged")
				if err != nil || warm.AfterCapture.ImportedBytes != 0 || !f.s.Unchanged(t.Context(), warm) {
					t.Fatalf("unchanged finish reread or invented edits: %v %+v", err, warm.AfterCapture)
				}
			}
		})
	}
}

// PLAT-746-319 review: ignored output must neither enter reply history nor
// appear as a deletion when a new ignore rule takes effect during the turn.
func TestTurnRespectsOrdinaryIgnoreRules(t *testing.T) {
	for _, newlyIgnored := range []bool{false, true} {
		t.Run(fmt.Sprint(newlyIgnored), func(t *testing.T) {
			f := newCrew(t)
			rules := "generated/\n"
			if newlyIgnored {
				rules = ""
			}
			f.put(".gitignore", rules)
			f.put("generated/output.json", "before\n")
			f.backdateAll()
			before, err := f.s.BeginGitTurn(t.Context(), "ignored")
			if err != nil {
				t.Fatal(err)
			}
			if !newlyIgnored && f.tree(before.Before)["generated/output.json"] != "" {
				t.Fatal("ignored output was imported")
			}
			f.put("generated/output.json", "after\n")
			if newlyIgnored {
				f.put(".gitignore", "generated/\n")
			}
			r, err := f.s.FinishGitTurn(t.Context(), "ignored")
			if err != nil {
				t.Fatal(err)
			}
			all := func(string) bool { return true }
			c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", all)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if newlyIgnored {
				want = 1 // Only .gitignore changed; output did not become a deletion.
			}
			if len(c.Changes) != want || (want == 1 && c.Changes[0].Path != ".gitignore") {
				t.Fatalf("ignored output leaked into changes: %+v", c.Changes)
			}
			plan, err := f.s.PlanUndo(t.Context(), r.ID, all, all)
			if err != nil || len(plan.Restore) != want {
				t.Fatalf("Undo includes ignored output: %v %+v", err, plan)
			}
		})
	}
}

// PLAT-746-319: a stalled checkout used to spend the entire after deadline,
// losing ordinary-file and healthy checkout changes from the reply bar too.
func TestTurnSlowCheckoutKeepsHealthyReplyChanges(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX Git process boundary")
	}
	for _, slowBefore := range []bool{true, false} {
		t.Run(fmt.Sprintf("before=%t", slowBefore), func(t *testing.T) {
			f := newCrew(t)
			f.repo("00-slow", map[string]string{"a.txt": "original\n"})
			for i := 0; i < 6; i++ {
				f.repo(fmt.Sprintf("app%d", i), map[string]string{"a.txt": "original\n"})
			}
			f.put("report.md", "original\n")
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			blocked := filepath.Join(bin, "blocked")
			// Only the selected checkout stalls. Private Git and all healthy
			// checkouts still execute their real commands through safegit.
			wrapper := fmt.Sprintf("#!/bin/sh\nif [ \"$PWD\" = %q ] && [ -f %q ]; then exec sleep 60; fi\nexec %q \"$@\"\n", filepath.Join(f.s.Root, "00-slow"), blocked, realGit)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(wrapper), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			block := func(on bool) {
				t.Helper()
				if on {
					if err := os.WriteFile(blocked, nil, 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					_ = os.Remove(blocked)
				}
			}
			boundary := func(begin bool) Record {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
				defer cancel()
				var r Record
				var err error
				if begin {
					r, err = f.s.BeginGitTurn(ctx, "slow")
				} else {
					r, err = f.s.FinishGitTurn(ctx, "slow")
				}
				if err != nil {
					t.Fatalf("healthy reply capture lost: %v", err)
				}
				return r
			}
			block(slowBefore)
			before := boundary(true)
			if slowBefore && len(before.GitTurn) != 6 {
				t.Fatalf("healthy checkouts starved: %d", len(before.GitTurn))
			}
			block(!slowBefore)
			f.put("00-slow/a.txt", "changed but unknown\n")
			for i := 0; i < 6; i++ {
				f.put(fmt.Sprintf("app%d/a.txt", i), "changed\n")
			}
			f.put("report.md", "changed\n")
			r := boundary(false)
			block(false)
			if r.Status != "partial" || len(r.Review.IncompleteRoots) != 1 || r.Review.IncompleteRoots[0] != "00-slow" {
				t.Fatalf("missing checkout not disclosed: %+v", r.Review)
			}
			c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
			if err != nil || len(c.Changes) != 7 {
				t.Fatalf("healthy/ordinary reply files lost: %v %+v", err, c.Changes)
			}
			for _, change := range c.Changes {
				if strings.HasPrefix(change.Path, "00-slow/") {
					t.Fatal("unreadable baseline invented a change")
				}
			}
		})
	}
}

// PLAT-746-319: a U+202F filename produced a Go \u escape that Git rejects,
// failing every reply capture for the newly imported project.
func TestGitTurnKeepsUnicodeFilename(t *testing.T) {
	f := newCrew(t)
	name := "report 2026.md"
	repo := "repo 2026"
	f.repo(repo, map[string]string{name: "committed\n"})
	paths := []string{name, repo + "/" + name}
	for _, p := range paths {
		f.put(p, "before\n")
	}
	if _, err := f.s.BeginGitTurn(t.Context(), "unicode"); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		f.put(p, "after\n")
	}
	r, err := f.s.FinishGitTurn(t.Context(), "unicode")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
	if err != nil || len(c.Changes) != 2 {
		t.Fatalf("Unicode changes lost: %v %+v", err, c.Changes)
	}
	for _, p := range paths {
		diff, err := f.s.CompareSaved(t.Context(), []string{r.ID}, p, "", func(string) bool { return true })
		if err != nil || !strings.Contains(diff.Diff, "+after") || !strings.Contains(diff.Diff, "-before") {
			t.Fatalf("saved Unicode diff: %v %+v", err, diff)
		}
	}
	plan, err := f.s.PlanUndo(t.Context(), r.ID, func(string) bool { return true }, func(string) bool { return true })
	if err != nil || len(plan.Restore) != 2 || len(plan.Skipped) != 0 {
		t.Fatalf("Unicode Undo paths lost: %v %+v", err, plan)
	}
}

// PLAT-746-766: cache trees used to exhaust a shared 10k allowance and
// prevent later repositories from being captured, regardless of byte size.
func TestTurnDiscoveryDoesNotLimitFileCount(t *testing.T) {
	f := newCrew(t)
	f.repo("z-project", map[string]string{"edit.txt": "original"})
	const count = 10001
	for i := 0; i < count; i++ {
		f.put(fmt.Sprintf("cache/file-%05d", i), "")
		f.put(fmt.Sprintf("z-project/new-%05d", i), "")
	}
	var stats CaptureStats
	dirs, loose, err := f.s.turnPaths(t.Context(), &stats)
	if err != nil || len(loose) != count || len(dirs) != 1 || stats.Deferred != "" {
		t.Fatalf("ordinary file count hid a repository: %v loose=%d dirs=%v stats=%+v", err, len(loose), dirs, stats)
	}
	repos, skipped, err := f.s.turnRepos(t.Context(), dirs, &stats)
	if err != nil || len(skipped) != 0 || len(repos["z-project"].Dirty) != count {
		t.Fatalf("repository paths were capped: %v paths=%d skipped=%v", err, len(repos["z-project"].Dirty), skipped)
	}
}

// PLAT-746: every reply in a large project warned "exceeded the capture size
// limit" because files changed by others before the reply did not fit the
// before capture, even when the reply never touched them. Warn only when a
// file left out of the capture changed during the reply.
func TestTurnCaptureLimitWarnsOnlyForFilesTheReplyChanged(t *testing.T) {
	for _, touched := range []bool{false, true} {
		t.Run(fmt.Sprint(touched), func(t *testing.T) {
			f := newCrew(t)
			for i := 0; i < 11; i++ {
				f.put(fmt.Sprintf("assets/%02d.bin", i), "")
				if err := os.Truncate(filepath.Join(f.s.Root, "assets", fmt.Sprintf("%02d.bin", i)), MaxFileBytes); err != nil {
					t.Fatal(err)
				}
			}
			f.put("notes.txt", "before\n")
			f.backdateAll()
			begun, err := f.s.BeginGitTurn(t.Context(), "turn")
			if err != nil {
				t.Fatal(err)
			}
			if begun.BeforeCapture.BudgetSkipped == 0 || len(begun.Skipped) == 0 {
				t.Fatalf("fixture did not exceed the capture limit: %+v", begun.BeforeCapture)
			}
			f.put("notes.txt", "after\n")
			if touched {
				asset, err := os.OpenFile(filepath.Join(f.s.Root, filepath.FromSlash(begun.Skipped[0])), os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = asset.WriteAt([]byte("x"), 0)
				asset.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err := f.s.FinishGitTurn(t.Context(), "turn")
			if err != nil {
				t.Fatal(err)
			}
			if r.CaptureLimitLost() != touched {
				t.Fatalf("capture limit warning = %t, want %t: skipped=%v", r.CaptureLimitLost(), touched, r.Skipped)
			}
			c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
			if err != nil || len(c.Changes) != 1 || c.Changes[0].Path != "notes.txt" {
				t.Fatalf("want only the edited note, got %v %+v", err, c.Changes)
			}
		})
	}
}
