package server

import (
	"path/filepath"
	"testing"
)

// #3220: a reused status must go stale for every change that alters it (or the Changes view shows old files), and
// only for those (or every worktree commit re-reads the whole folder).
func TestWorkspaceGitWatchStaleOnlyForAffectingChanges(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work")
	main := filepath.Join(root, "app")
	common := filepath.Join(main, ".git")
	tree := filepath.Join(root, "app-fix")
	treeGit := filepath.Join(common, "worktrees", "app-fix")
	other := filepath.Join(root, "notes")

	fresh := func() *workspaceGitWatch {
		w := &workspaceGitWatch{root: root, repos: map[string]*workspaceGitCached{}}
		for dir, gitDir := range map[string]string{main: common, tree: treeGit, other: filepath.Join(other, ".git")} {
			c := common
			if dir == other {
				c = gitDir
			}
			branch := "main"
			if dir == tree {
				branch = "fix"
			}
			repo := workspaceGitRepo{Branch: branch, Upstream: "origin/" + branch, Files: []workspaceGitFile{{Path: "a.txt"}}}
			w.store(dir, w.begin(dir), repo, gitDir, c)
		}
		return w
	}
	cases := []struct {
		name  string
		path  string
		stale []string
	}{
		{"edit in the main checkout", filepath.Join(main, "src", "a.go"), []string{main}},
		{"edit in a worktree", filepath.Join(tree, "a.go"), []string{tree}},
		{"worktree commit writes its own index", filepath.Join(treeGit, "index"), []string{tree}},
		{"commit writes objects", filepath.Join(common, "objects", "ab", "cdef"), nil},
		{"main checkout index", filepath.Join(common, "index"), []string{main}},
		// Live: commits and fetches by other agents' worktrees kept every checkout of the repository stale.
		{"a commit on the worktree's branch", filepath.Join(common, "refs", "heads", "fix.lock"), []string{tree}},
		{"a fetch moved the main checkout's upstream", filepath.Join(common, "refs", "remotes", "origin", "main"), []string{main}},
		{"another agent's branch moved", filepath.Join(common, "refs", "heads", "other"), nil},
		{"a fetch", filepath.Join(common, "FETCH_HEAD"), nil},
		{"refs repacked", filepath.Join(common, "packed-refs"), []string{main, tree}},
		{"outside every checkout", filepath.Join(root, "README.md"), nil},
	}
	for _, tc := range cases {
		w := fresh()
		w.note(tc.path, false)
		want := map[string]bool{}
		for _, dir := range tc.stale {
			want[dir] = true
		}
		for _, dir := range []string{main, tree, other} {
			if _, ok := w.cached(dir); ok == want[dir] {
				t.Errorf("%s: %s reused=%t, want stale=%t", tc.name, dir, ok, want[dir])
			}
		}
	}

	// A change during a read keeps the result stale; callers filter the returned files in place.
	w := fresh()
	token := w.begin(main)
	w.note(filepath.Join(main, "b.go"), false)
	w.store(main, token, workspaceGitRepo{}, common, common)
	if _, ok := w.cached(main); ok {
		t.Fatal("a status read across a change was reused")
	}
	repo, _ := w.cached(tree)
	repo.Files[0].Path = "changed"
	if again, _ := w.cached(tree); again.Files[0].Path != "a.txt" {
		t.Fatal("filtering a returned status changed the kept one")
	}
}
