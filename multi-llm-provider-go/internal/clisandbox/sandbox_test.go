package clisandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestFingerprintIsStableAndIncludesMode(t *testing.T) {
	first := &llmtypes.CLISecurityPolicy{
		Mode:               llmtypes.CLISecurityModeVerified,
		Provider:           "codex-cli",
		WorkspaceReadPaths: []string{"/b", "/a"},
	}
	second := first.Clone()
	second.WorkspaceReadPaths = []string{"/a", "/b"}
	if Fingerprint(first) != Fingerprint(&second) {
		t.Fatal("equivalent policies produced different fingerprints")
	}
	second.Mode = llmtypes.CLISecurityModeCompatibility
	if Fingerprint(first) == Fingerprint(&second) {
		t.Fatal("mode change did not change fingerprint")
	}
}

func TestCodexProfileAllowsWorkspaceAndDeniesUnrelatedHome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(home, ".agentworks-sandbox-allowed-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	allowed := filepath.Join(workspace, "allowed.txt")
	if err := os.WriteFile(allowed, []byte("allowed"), 0o600); err != nil {
		t.Fatal(err)
	}
	deniedDir, err := os.MkdirTemp(home, ".agentworks-sandbox-denied-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(deniedDir) })
	denied := filepath.Join(deniedDir, "denied.txt")
	if err := os.WriteFile(denied, []byte("denied"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeVerified,
		Provider:            "codex-cli",
		WorkspaceReadPaths:  []string{workspace},
		WorkspaceWritePaths: []string{workspace},
	}
	profile, err := codexProfile(policy, "/usr/bin/true", workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	profileFile := filepath.Join(t.TempDir(), "profile.sb")
	if err := os.WriteFile(profileFile, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(
		t.Context(),
		"/usr/bin/sandbox-exec", "-f", profileFile,
		"/bin/sh", "-c", `cat "$1" >/dev/null && ! cat "$2" >/dev/null 2>&1`,
		"sandbox-test", allowed, denied,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox contract failed: %v\n%s\nprofile:\n%s", err, out, profile)
	}
}

func TestPrepareCodexCommandRejectsWrongProvider(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	_, _, err := PrepareCodexCommand(&llmtypes.CLISecurityPolicy{
		Mode:     llmtypes.CLISecurityModeVerified,
		Provider: "claude-code",
	}, []string{"true"}, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareCodexCommandRunsInstalledCodexInsideSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("Codex CLI is not installed")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(home, ".agentworks-codex-smoke-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeVerified,
		Provider:            "codex-cli",
		ProfileVersion:      "1",
		WorkspaceReadPaths:  []string{workspace},
		WorkspaceWritePaths: []string{workspace},
		HostReadPaths:       []string{filepath.Join(home, ".codex")},
		HostWritePaths:      []string{filepath.Join(home, ".codex")},
		ApprovedCapabilities: []string{
			"provider_identity",
		},
	}
	command, cleanup, err := PrepareCodexCommand(policy, []string{"codex", "--version"}, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", command).CombinedOutput()
	if err != nil {
		t.Fatalf("sandboxed Codex failed: %v\n%s", err, out)
	}
	if !strings.Contains(strings.ToLower(string(out)), "codex") {
		t.Fatalf("unexpected Codex version output: %s", out)
	}
}

func TestPreparedSandboxExecutesInsideTmuxPane(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(home, ".agentworks-tmux-sandbox-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	deniedFile, err := os.CreateTemp(home, ".agentworks-tmux-denied-*")
	if err != nil {
		t.Fatal(err)
	}
	deniedPath := deniedFile.Name()
	if _, err := deniedFile.WriteString("secret"); err != nil {
		t.Fatal(err)
	}
	_ = deniedFile.Close()
	t.Cleanup(func() { _ = os.Remove(deniedPath) })

	resultPath := filepath.Join(workspace, "result.txt")
	fakeCodex := filepath.Join(workspace, "fake-codex")
	script := "#!/bin/sh\nif cat \"$1\" >/dev/null 2>&1; then echo exposed; else echo denied; fi > \"$2\"\n"
	if err := os.WriteFile(fakeCodex, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeVerified,
		Provider:            "codex-cli",
		WorkspaceReadPaths:  []string{workspace},
		WorkspaceWritePaths: []string{workspace},
	}
	command, cleanup, err := PrepareCodexCommand(policy, []string{fakeCodex, deniedPath, resultPath}, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	session := fmt.Sprintf("agentworks-sandbox-test-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "tmux", "kill-session", "-t", session).Run()
	})
	if out, err := exec.CommandContext(t.Context(), "tmux", "new-session", "-d", "-s", session, command).CombinedOutput(); err != nil {
		t.Fatalf("start tmux pane: %v\n%s", err, out)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(resultPath)
		if readErr == nil {
			result := strings.TrimSpace(string(data))
			if result == "" {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			if result != "denied" {
				t.Fatalf("tmux pane exposed denied home file: %q", data)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("tmux sandbox test did not produce a result")
}

func TestPrepareCodexCommandUsesPrivateHomeInIsolatedMode(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	workspace := t.TempDir()
	privateHome := t.TempDir()
	resultPath := filepath.Join(workspace, "environment.txt")
	fakeCodex := filepath.Join(workspace, "fake-codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nprintf '%s\\n%s\\n' \"$HOME\" \"$CODEX_HOME\" > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeIsolated,
		Provider:            "codex-cli",
		PrivateHome:         privateHome,
		WorkspaceReadPaths:  []string{workspace},
		WorkspaceWritePaths: []string{workspace},
	}
	command, cleanup, err := PrepareCodexCommand(policy, []string{fakeCodex, resultPath}, workspace, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("isolated command failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	resolvedPrivateHome, err := filepath.EvalSymlinks(privateHome)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvedPrivateHome + "\n" + filepath.Join(resolvedPrivateHome, ".codex") + "\n"
	if string(data) != want {
		t.Fatalf("isolated environment = %q, want %q", data, want)
	}
}

func TestPreparedSandboxRunsExplicitBridgeOutsideWorkspace(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec is macOS-only")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(home, ".agentworks-bridge-workspace-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	bridgeDir, err := os.MkdirTemp(home, ".agentworks-trusted-bridge-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(bridgeDir) })
	bridge := filepath.Join(bridgeDir, "mcpbridge")
	if err := os.WriteFile(bridge, []byte("#!/bin/sh\nprintf bridge-ok > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(workspace, "bridge-result.txt")
	fakeCodex := filepath.Join(workspace, "fake-codex")
	if err := os.WriteFile(fakeCodex, []byte("#!/bin/sh\nexec \"$1\" \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeVerified,
		Provider:            "codex-cli",
		WorkspaceReadPaths:  []string{workspace},
		WorkspaceWritePaths: []string{workspace},
	}
	command, cleanup, err := PrepareCodexCommand(
		policy,
		[]string{fakeCodex, bridge, resultPath},
		workspace,
		[]string{bridge},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("trusted bridge failed inside sandbox: %v\n%s", err, out)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "bridge-ok" {
		t.Fatalf("bridge result = %q", data)
	}
}
