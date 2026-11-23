//go:build linux

package clisandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The real launcher, not just the policy text: inside a granted workflow
// folder the blocked planning/ stays readable but not writable, the raw
// database can be neither read nor written, and the rest of the folder works.
// Set CODING_TEST_LANDLOCK_RUNNER to the host's launcher to run it.
func TestBlockedPathsUnderTheRealLauncher(t *testing.T) {
	runner := os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	if runner == "" {
		runner = os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") // the app's remote verify builds and names it
	}
	if runner == "" {
		t.Skip("CODING_TEST_LANDLOCK_RUNNER is not set")
	}
	// Outside /tmp: a confined command always writes its own private /tmp, so a
	// tree there would be writable whatever the policy says.
	base := os.Getenv("AGENTWORKS_LINUX_TEST_ROOT")
	if base == "" {
		t.Skip("AGENTWORKS_LINUX_TEST_ROOT (a writable folder outside /tmp) is not set")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "landlock-blocked-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	wf := filepath.Join(root, "Workflow", "w")
	for _, dir := range []string{"planning", "code", "db"} {
		if err := os.MkdirAll(filepath.Join(wf, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"planning/plan.json", "db/db.sqlite", "db/notes.md", "code/main.py"} {
		if err := os.WriteFile(filepath.Join(wf, f), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	workDir := filepath.Join(root, "run")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Blocked paths in the CLI's own folder (a server Crew's settings/, a managed
	// instruction file) are mounted over too; the folder is never split, so the
	// CLI can still create files in it.
	if err := os.MkdirAll(filepath.Join(workDir, "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "settings", "schedules.json"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "CLAUDE.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A blocked file that does not exist yet cannot be mounted over: its grant is
	// split instead, so it cannot be planted (and nothing new fits beside it).
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", LandlockRunner: runner,
		PrivateHome: filepath.Join(root, "home"), CredentialHome: root,
		WorkspaceWritePaths: []string{wf, other},
		BlockedPaths:        []string{filepath.Join(wf, "db", "db.sqlite"), filepath.Join(other, "db.sqlite-wal")},
		BlockedWritePaths:   []string{filepath.Join(wf, "planning"), filepath.Join(workDir, "settings"), filepath.Join(workDir, "settings", "webhooks.json"), filepath.Join(workDir, "CLAUDE.md")},
	}
	run := func(script string) error {
		args, cleanup, err := LandlockArgs(policy, []string{"/bin/sh", "-c", script}, workDir, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		return exec.CommandContext(context.Background(), args[0], args[1:]...).Run()
	}
	allowed := map[string]string{
		"edit code":          "echo y >> " + filepath.Join(wf, "code", "main.py"),
		"new file in code":   "echo y > " + filepath.Join(wf, "code", "new.py"),
		"edit db notes":      "echo y >> " + filepath.Join(wf, "db", "notes.md"),
		"read planning":      "cat " + filepath.Join(wf, "planning", "plan.json"),
		"write the work dir": "echo y > " + filepath.Join(workDir, "ok.txt"),
		"read settings":      "cat " + filepath.Join(workDir, "settings", "schedules.json"),
		// PLAT-746-924: the existing blocked paths are mounted over, so the folder holding them keeps working.
		"new file in the workflow root": "echo y > " + filepath.Join(wf, "new.txt"),
	}
	for name, script := range allowed {
		if err := run(script); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	refused := map[string]string{
		"write planning":         "echo y >> " + filepath.Join(wf, "planning", "plan.json"),
		"new file in planning":   "echo y > " + filepath.Join(wf, "planning", "x.json"),
		"read the database":      "cat " + filepath.Join(wf, "db", "db.sqlite"),
		"write the database":     "echo y >> " + filepath.Join(wf, "db", "db.sqlite"),
		"write settings":         "echo y >> " + filepath.Join(workDir, "settings", "schedules.json"),
		"new file in settings":   "echo y > " + filepath.Join(workDir, "settings", "webhooks.json"),
		"write the instructions": "echo y >> " + filepath.Join(workDir, "CLAUDE.md"),
		"create a wal file":      "echo y > " + filepath.Join(other, "db.sqlite-wal"),
	}
	for name, script := range refused {
		if err := run(script); err == nil {
			t.Errorf("%s was allowed", name)
		}
	}
}
