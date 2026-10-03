package security

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsurePlatformGitIgnoreCreatesKeepsAndDoesNotDuplicate(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".config")
	EnsurePlatformGitIgnore(config)
	EnsurePlatformGitIgnore(config)
	data, err := os.ReadFile(filepath.Join(config, "git", "ignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), ".sandbox-cache/") != 1 {
		t.Fatalf("want the line once, got %q", data)
	}

	// A person's own list is kept and gets the line once.
	own := filepath.Join(t.TempDir(), ".config")
	if err := os.MkdirAll(filepath.Join(own, "git"), 0o770); err != nil {
		t.Fatal(err)
	}
	ignoreFile := filepath.Join(own, "git", "ignore")
	if err := os.WriteFile(ignoreFile, []byte("*.log"), 0o660); err != nil {
		t.Fatal(err)
	}
	EnsurePlatformGitIgnore(own)
	EnsurePlatformGitIgnore(own)
	data, _ = os.ReadFile(ignoreFile)
	if !strings.HasPrefix(string(data), "*.log\n") || strings.Count(string(data), ".sandbox-cache/") != 1 {
		t.Fatalf("own list not preserved: %q", data)
	}
}

// git, run with the home environment a sandboxed command gets, must not list or add the platform's folder.
func TestSandboxHomeKeepsPlatformFolderOutOfGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	project := t.TempDir()
	home := filepath.Join(project, SandboxPersistentDirName, "home")
	env := withHome([]string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=1"}, home)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		cmd.Env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(project, "real.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(home, ".git-credentials")
	if err := os.WriteFile(secret, []byte("https://user:token@github.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := run("status", "--porcelain"); strings.Contains(status, SandboxPersistentDirName) || !strings.Contains(status, "real.txt") {
		t.Fatalf("status must show real.txt and hide the platform folder:\n%s", status)
	}
	run("add", "-A")
	if staged := run("diff", "--cached", "--name-only"); strings.Contains(staged, SandboxPersistentDirName) || !strings.Contains(staged, "real.txt") {
		t.Fatalf("git add -A staged the platform folder (credentials!) or missed real files:\n%s", staged)
	}
}
