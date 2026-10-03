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
	// A project's own home (a Crew, a workflow, a terminal that is not a slot): the service creates it and writes the XDG ignore file there.
	env := SlotHomeEnv([]string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=1"}, project, []string{project}, "")
	if homeEnvValue(env, "HOME") != home {
		t.Fatalf("home = %q, want %q", homeEnvValue(env, "HOME"), home)
	}
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

// A slot's own home is owner-only, so the service cannot write the ignore list there: it goes in the project's .sandbox-cache and git is pointed at it.
func TestSlotHomeEnvPointsGitAtTheProjectsIgnoreList(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, SandboxPersistentDirName, "cache"), 0o770); err != nil {
		t.Fatal(err)
	}
	slotHome := filepath.Join(t.TempDir(), "slot01") // not under the project: like /srv/<app>/slots/home/<slot>
	if err := os.MkdirAll(slotHome, 0o700); err != nil {
		t.Fatal(err)
	}
	base := []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_NOSYSTEM=1"}
	env := SlotHomeEnv(base, project, []string{project}, slotHome)
	if homeEnvValue(env, "GIT_CONFIG_COUNT") != "1" || homeEnvValue(env, "GIT_CONFIG_KEY_0") != "core.excludesFile" {
		t.Fatalf("git not pointed at the list: %v", env)
	}
	list := homeEnvValue(env, "GIT_CONFIG_VALUE_0")
	if data, err := os.ReadFile(list); err != nil || !strings.Contains(string(data), ".sandbox-cache/") {
		t.Fatalf("ignore list missing: %v %q", err, list)
	}
	if _, err := os.Stat(filepath.Join(slotHome, ".config", "git", "ignore")); err == nil {
		t.Fatal("must not write into the slot's own home")
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = project
		cmd.Env = append(append([]string{}, env...), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
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
	if err := os.WriteFile(filepath.Join(project, SandboxPersistentDirName, "cache", "junk"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if status := run("status", "--porcelain"); strings.Contains(status, SandboxPersistentDirName) || !strings.Contains(status, "real.txt") {
		t.Fatalf("slot-run git shows the platform folder:\n%s", status)
	}

	// An existing GIT_CONFIG_COUNT is extended, not replaced.
	extended := SlotHomeEnv(append(append([]string{}, base...), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=user.name", "GIT_CONFIG_VALUE_0=x"), project, []string{project}, slotHome)
	if homeEnvValue(extended, "GIT_CONFIG_COUNT") != "2" || homeEnvValue(extended, "GIT_CONFIG_KEY_0") != "user.name" || homeEnvValue(extended, "GIT_CONFIG_KEY_1") != "core.excludesFile" {
		t.Fatalf("existing git config entries lost: %v", extended)
	}

	// No .sandbox-cache in the project: nothing is created, env unchanged.
	bare := t.TempDir()
	plain := SlotHomeEnv(base, bare, []string{bare}, slotHome)
	if homeEnvValue(plain, "GIT_CONFIG_COUNT") != "" {
		t.Fatalf("env changed without a project .sandbox-cache: %v", plain)
	}
	if _, err := os.Stat(filepath.Join(bare, SandboxPersistentDirName)); err == nil {
		t.Fatal(".sandbox-cache must not be created")
	}
}
