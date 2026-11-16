package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #2922: a chat gets one branch folder per repository, a fork gets its own copy,
// and removing a chat never deletes work: a folder with uncommitted edits or
// unpushed commits stays.
func TestChatBranchReuseAndRemovalKeepsUnsavedWork(t *testing.T) {
	base := t.TempDir()
	roots := crewroot.Roots{Files: filepath.Join(base, "files"), Private: filepath.Join(base, "private")}
	repo := filepath.Join(roots.Files, "code", "app")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "a.txt")
	git(repo, "commit", "--quiet", "-m", "init")

	start := func(chat, purpose string) chatBranch {
		t.Helper()
		b, _, err := startChatBranch(t.Context(), roots, chat, "code/app", purpose)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	clean, edited, committed := start("crew:chat:a", "Fix login"), start("crew:chat:b", "Fix login"), start("crew:chat:c", "docs")
	if clean.Branch != "chat/fix-login" || edited.Branch != "chat/fix-login-2" || clean.Path != "code/app/.worktrees/fix-login" {
		t.Fatalf("unexpected branches: %+v %+v", clean, edited)
	}
	if again, reused, err := startChatBranch(t.Context(), roots, "crew:chat:a", filepath.Join(repo), "other"); err != nil || !reused || again.Path != clean.Path {
		t.Fatalf("the chat's existing branch was not returned: %v %t %+v", err, reused, again)
	}
	if status := git(repo, "status", "--porcelain"); status != "" {
		t.Fatalf("branch folders show in the repository's own status: %q", status)
	}
	if got := chatBranchCheckouts(roots, "crew:chat:a"); len(got) != 1 || got[0] != filepath.Join(roots.Files, "code", "app", ".worktrees", "fix-login") {
		t.Fatalf("file card checkouts: %v", got)
	}
	folder := func(b chatBranch) string { return filepath.Join(roots.Files, filepath.FromSlash(b.Path)) }
	if err := os.WriteFile(filepath.Join(folder(edited), "a.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder(committed), "b.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(folder(committed), "add", "b.txt")
	git(folder(committed), "commit", "--quiet", "-m", "unpushed")

	// A fork gets its own branch from the parent's commit with the parent's
	// uncommitted edits copied in; its first turn is told, once.
	if err := os.WriteFile(filepath.Join(folder(edited), "notes.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	forks := forkChatBranches(t.Context(), roots, "crew:chat:b", "crew:chat:fork")
	if len(forks) != 1 || forks[0].Branch != "chat/fix-login-2-fork" {
		t.Fatalf("fork branches: %+v", forks)
	}
	for name, want := range map[string]string{"a.txt": "edited\n", "notes.txt": "untracked\n"} {
		if got, err := os.ReadFile(filepath.Join(folder(forks[0]), name)); err != nil || string(got) != want {
			t.Fatalf("fork copy of %s: %q %v", name, got, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(folder(edited), "a.txt")); string(got) != "edited\n" {
		t.Fatalf("parent folder changed by the fork: %q", got)
	}
	if line := takeChatBranchAnnouncement(roots, "crew:chat:fork"); !strings.Contains(line, "chat/fix-login-2-fork") || !strings.Contains(line, folder(forks[0])) {
		t.Fatalf("fork announcement: %q", line)
	}
	if line := takeChatBranchAnnouncement(roots, "crew:chat:fork"); line != "" {
		t.Fatalf("fork announced twice: %q", line)
	}

	for _, chat := range []string{"crew:chat:a", "crew:chat:b", "crew:chat:c", "crew:chat:fork"} {
		removeChatBranches(t.Context(), roots, chat)
	}
	if _, err := os.Stat(folder(clean)); !os.IsNotExist(err) {
		t.Fatalf("clean branch folder was kept: %v", err)
	}
	for _, kept := range []chatBranch{edited, committed} {
		if _, err := os.Stat(folder(kept)); err != nil {
			t.Fatalf("branch folder with unsaved work was removed: %s %v", kept.Branch, err)
		}
	}
	branches := map[string]bool{}
	for _, name := range strings.Fields(git(repo, "branch", "--list", "--format=%(refname:short)", "chat/*")) {
		branches[name] = true
	}
	if branches["chat/fix-login"] || !branches["chat/fix-login-2"] || !branches["chat/docs"] || !branches["chat/fix-login-2-fork"] {
		t.Fatalf("wrong branches after removal: %v", branches)
	}
}
