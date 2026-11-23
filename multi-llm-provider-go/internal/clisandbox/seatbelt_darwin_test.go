//go:build darwin

package clisandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The real macOS sandbox, not just the profile text: the person's home stays
// open (their settings, logins, other projects), AgentWorks' workspace data is
// closed except the granted folders, a blocked path inside a granted folder is
// refused, and the CLI cannot start or script other apps.
func TestSeatbeltConfinesUnderTheRealSandbox(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root, err := os.MkdirTemp(home, ".agentworks-seatbelt-test-")
	if err != nil {
		t.Skipf("cannot create a folder under the home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	workspace := filepath.Join(root, "workspace-docs")
	granted := filepath.Join(workspace, "Workflow", "mine")
	otherWorkflow := filepath.Join(workspace, "Workflow", "other")
	personal := filepath.Join(root, "my-notes")
	blockedDir := filepath.Join(granted, "planning")
	blockedDB := filepath.Join(granted, "db.sqlite")
	for _, dir := range []string{granted, otherWorkflow, personal, blockedDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(granted, "ok.txt"), filepath.Join(otherWorkflow, "key.txt"), filepath.Join(personal, "note.txt"), filepath.Join(blockedDir, "plan.json"), blockedDB} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:              llmtypes.CLISecurityModeIsolated,
		Provider:          "codex-cli",
		Seatbelt:          true,
		PrivateHome:       filepath.Join(granted, ".sandbox", "cli-home"),
		ProtectedRoots:    []string{workspace},
		BlockedPaths:      []string{blockedDB},
		BlockedWritePaths: []string{blockedDir},
	}
	run := func(script string) error {
		args, _, err := LandlockArgs(policy, []string{"/bin/sh", "-c", script}, granted, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return exec.CommandContext(context.Background(), args[0], args[1:]...).Run()
	}
	for name, script := range map[string]string{
		"read the granted folder":      "cat " + filepath.Join(granted, "ok.txt"),
		"write the granted folder":     "echo y > " + filepath.Join(granted, "new.txt"),
		"read the person's own files":  "cat " + filepath.Join(personal, "note.txt"),
		"write the person's own files": "echo y > " + filepath.Join(personal, "new.txt"),
		"read a write-blocked path":    "cat " + filepath.Join(blockedDir, "plan.json"),
		"list the workflow folder":     "ls " + granted,
	} {
		if err := run(script); err != nil {
			t.Errorf("%s failed: %v", name, err)
		}
	}
	for name, script := range map[string]string{
		"read another workflow":        "cat " + filepath.Join(otherWorkflow, "key.txt"),
		"write another workflow":       "echo y > " + filepath.Join(otherWorkflow, "new.txt"),
		"write a blocked path":         "echo y > " + filepath.Join(blockedDir, "plan.json"),
		"read a read-blocked path":     "cat " + blockedDB,
		"start an app with open":       "/usr/bin/open -g -a TextEdit",
		"script an app with osascript": "/usr/bin/osascript -e 'return 1'",
	} {
		if err := run(script); err == nil {
			t.Errorf("could %s", name)
		}
	}
}

func TestSeatbeltArgsIsANoOpWithoutTheRequest(t *testing.T) {
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, PrivateHome: t.TempDir()}
	args, _, err := SeatbeltArgs(policy, []string{"claude"}, t.TempDir(), SeatbeltGrants{})
	if err != nil || strings.Join(args, " ") != "claude" {
		t.Fatalf("args = %v, err = %v", args, err)
	}
}

// Muse walks up from its working folder reading every folder on the way; a
// refused read there fails its whole MCP startup and the chat gets no bridge.
// Only Muse gets the listing grant, and nothing inside a closed folder opens.
func TestSeatbeltLetsMuseListFoldersAboveItsWorkingFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	root, err := os.MkdirTemp(home, ".agentworks-seatbelt-muse-test-")
	if err != nil {
		t.Skipf("cannot create a folder under the home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	state := filepath.Join(root, "state")
	granted := filepath.Join(state, "cli-runtimes", "v1", "mine")
	other := filepath.Join(state, "cli-runtimes", "v1", "other")
	for _, dir := range []string{granted, other} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(other, "key.txt")
	if err := os.WriteFile(secret, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runAs := func(provider, script string) error {
		policy := &llmtypes.CLISecurityPolicy{
			Mode:           llmtypes.CLISecurityModeIsolated,
			Provider:       provider,
			Seatbelt:       true,
			PrivateHome:    filepath.Join(granted, ".sandbox", "cli-home"),
			ProtectedRoots: []string{state},
		}
		args, _, err := LandlockArgs(policy, []string{"/bin/sh", "-c", script}, granted, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return exec.CommandContext(context.Background(), args[0], args[1:]...).Run()
	}
	for _, folder := range []string{state, filepath.Join(state, "cli-runtimes"), filepath.Join(state, "cli-runtimes", "v1")} {
		if err := runAs("muse-cli", "ls "+folder); err != nil {
			t.Errorf("muse cannot list %s: %v", folder, err)
		}
		if err := runAs("codex-cli", "ls "+folder); err == nil {
			t.Errorf("codex could list %s: only Muse gets the grant", folder)
		}
	}
	if err := runAs("muse-cli", "ls "+other); err == nil {
		t.Error("muse could list another runtime's contents")
	}
	if err := runAs("muse-cli", "cat "+secret); err == nil {
		t.Error("muse could read a file in another runtime")
	}
}
