package crewsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// PLAT-856-387: the handoff preserves history and staged/unstaged project work
// without copying Git login/configuration files or overwriting another branch.
func TestGitHandoffPreservesHistoryAndStagedWork(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	run := func(root string, args ...string) string {
		t.Helper()
		out, err := gitCommand(t.Context(), root, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(root, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	run(local, "init", "-b", "main")
	write(local, "note.txt", "committed")
	run(local, "add", "note.txt")
	run(local, "-c", "user.name=Example", "-c", "user.email=owner@example.com", "commit", "-m", "initial")
	run(local, "config", "remote.origin.url", "https://example.com/project.git")
	write(local, "note.txt", "staged")
	run(local, "add", "note.txt")
	write(local, "note.txt", "unstaged")
	left := crewroot.Roots{Files: local, Resources: local}
	right := crewroot.Roots{Files: remote, Resources: remote}
	first, err := Capture(t.Context(), left, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.Git, err = CaptureGit(t.Context(), local, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = Apply(t.Context(), right, first); err != nil {
		t.Fatal(err)
	}
	if run(remote, "rev-parse", "HEAD") != first.Git.Head || run(remote, "show", ":note.txt") != "staged" {
		t.Fatal("lost history or staging")
	}
	if out, _ := gitCommand(t.Context(), remote, "config", "--get", "remote.origin.url").Output(); len(out) > 0 {
		t.Fatal("copied local Git configuration")
	}
	write(remote, "server.txt", "server work")
	run(remote, "add", "server.txt")
	back, err := Capture(t.Context(), right, first.Files)
	if err != nil {
		t.Fatal(err)
	}
	back.Git, err = CaptureGit(t.Context(), remote, first.Git)
	if err != nil {
		t.Fatal(err)
	}
	// Unstaging while away is an external edit too, even though the patch is empty.
	run(local, "reset", "--mixed", "HEAD")
	if err = Apply(t.Context(), left, back); err == nil {
		t.Fatal("overwrote independently cleared staging")
	}
	restore := gitCommand(t.Context(), local, "apply", "--cached", "--binary", "-")
	restore.Stdin = strings.NewReader(string(first.Git.IndexPatch))
	if err := restore.Run(); err != nil {
		t.Fatal(err)
	}
	if err = Apply(t.Context(), left, back); err != nil {
		t.Fatal(err)
	}
	if run(local, "show", ":server.txt") != "server work" || run(local, "config", "--get", "remote.origin.url") != "https://example.com/project.git" {
		t.Fatal("return handoff changed Git state")
	}
	content, _ := os.ReadFile(filepath.Join(local, "note.txt"))
	if string(content) != "unstaged" {
		t.Fatal("lost unstaged work")
	}
}
