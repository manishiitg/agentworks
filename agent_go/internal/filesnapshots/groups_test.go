package filesnapshots

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A checkout the reply cloned lists one group with its branch and only the
// files edited in it, not hundreds of adds; non-Git files stay separate.
func TestNewCheckoutCollapsesToEditedFilesAndKeepsPlainFilesApart(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "code", "app")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "--quiet", "-b", "feature-x")
	var changes []Change
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("f%02d.txt", i)
		if err := os.WriteFile(filepath.Join(repo, name), []byte("one\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		changes = append(changes, Change{Path: "code/app/" + name, Status: "A"})
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "init")
	run("remote", "add", "origin", "https://user:secret@github.com/acme/app.git")
	os.WriteFile(filepath.Join(repo, "f03.txt"), []byte("one\ntwo\n"), 0o600)
	os.WriteFile(filepath.Join(repo, "new.txt"), []byte("a\nb\nc\n"), 0o600)
	changes = append(changes, Change{Path: "notes.md", Status: "M"})

	groups := GroupChanges(context.Background(), root, changes, func(string) bool { return true })
	if len(groups) != 2 || groups[0].Kind != "repo" || groups[1].Kind != "crew_files" {
		t.Fatalf("groups = %+v", groups)
	}
	g := groups[0]
	if g.Root != "code/app" || g.Branch != "feature-x" || !g.NewCheckout || g.GitHubRepo != "acme/app" {
		t.Fatalf("repo group = %+v", g)
	}
	if len(g.Files) != 2 || g.Files[0].Path != "code/app/f03.txt" || g.Files[0].Status != "M" || *g.Files[0].Added != 1 ||
		g.Files[1].Path != "code/app/new.txt" || g.Files[1].Status != "A" || *g.Files[1].Added != 3 {
		t.Fatalf("edited files = %+v", g.Files)
	}
	if len(groups[1].Files) != 1 || groups[1].Files[0].Path != "notes.md" {
		t.Fatalf("crew files = %+v", groups[1])
	}
}

func TestGitHubRepoDropsCredentialsAndOtherHosts(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/app.git":             "acme/app",
		"https://tok@github.com/acme/app":         "acme/app",
		"ssh://git@github.com/acme/app.git":       "acme/app",
		"https://gitlab.example.com/acme/app.git": "",
	} {
		if got := GitHubRepo(remote); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", remote, got, want)
		}
	}
}
