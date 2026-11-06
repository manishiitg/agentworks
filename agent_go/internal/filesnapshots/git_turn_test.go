package filesnapshots

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
