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
	r, err := f.s.BeginGitTurn(t.Context(), "turn", []string{dir})
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
	r, err = f.s.FinishGitTurn(t.Context(), "turn", []string{dir})
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

	if _, err := f.s.BeginGitTurn(t.Context(), "clean", []string{dir}); err != nil {
		t.Fatal(err)
	}
	clean, err := f.s.FinishGitTurn(t.Context(), "clean", []string{dir})
	if err != nil || !f.s.Unchanged(t.Context(), clean) || len(clean.Review.Spans) != 0 {
		t.Fatalf("no-op turn: %v %+v", err, clean)
	}
}

func TestGitTurnRefusesConvertedBeforeBytesAndExternalCheckout(t *testing.T) {
	f := newCrew(t)
	f.repo("app", map[string]string{".gitattributes": "*.txt text\n", "a.txt": "original\n"})
	dir := filepath.Join(f.s.Root, "app")
	if _, err := f.s.BeginGitTurn(t.Context(), "converted", []string{dir}); err != nil {
		t.Fatal(err)
	}
	f.put("app/a.txt", "edited\n")
	r, err := f.s.FinishGitTurn(t.Context(), "converted", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "partial" || len(r.Skipped) != 1 || r.Skipped[0] != "app/a.txt" {
		t.Fatalf("invented raw before: %+v", r)
	}
	if _, err := f.s.BeginGitTurn(t.Context(), "external", []string{t.TempDir()}); err == nil {
		t.Fatal("external checkout accepted")
	}
}

func TestGitTurnEmptyRepositoryAndNewWorktree(t *testing.T) {
	f := newCrew(t)
	f.put("fresh/new.txt", "initial\n")
	f.git("fresh", "init", "--quiet")
	dir := filepath.Join(f.s.Root, "fresh")
	if _, err := f.s.BeginGitTurn(t.Context(), "first", []string{dir}); err != nil {
		t.Fatal(err)
	}
	f.git("fresh", "add", "-A")
	f.git("fresh", "commit", "--quiet", "-m", "first")
	f.put("fresh/new.txt", "edited\n")
	r, err := f.s.FinishGitTurn(t.Context(), "first", []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.s.CompareSaved(t.Context(), []string{r.ID}, "", "", func(string) bool { return true })
	if err != nil || len(c.Changes) != 1 || c.Changes[0].Status != "M" {
		t.Fatalf("unborn before bytes lost: %v %+v", err, c)
	}
	// A newly discovered checkout with a pre-existing commit is not a whole
	// project of added files. The test supplies that new checkout explicitly.
	f.git("fresh", "worktree", "add", "--quiet", "-b", "second", filepath.Join(f.s.Root, "second"))
	if _, err := f.s.BeginGitTurn(t.Context(), "new-worktree", []string{dir}); err != nil {
		t.Fatal(err)
	}
	r, err = f.s.FinishGitTurn(t.Context(), "new-worktree", []string{dir, filepath.Join(f.s.Root, "second")})
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
	if _, err := f.s.BeginGitTurn(t.Context(), "create", nil); err != nil {
		t.Fatal(err)
	}
	f.repo("new-project", map[string]string{"app.ts": "export const answer = 42\n"})
	r, err := f.s.FinishGitTurn(t.Context(), "create", []string{filepath.Join(f.s.Root, "new-project")})
	if err != nil {
		t.Fatal(err)
	}
	got := f.tree(r.After)
	if got["new-project/app.ts"] != "export const answer = 42\n" {
		t.Fatalf("initial commit missing: %+v", got)
	}
}
