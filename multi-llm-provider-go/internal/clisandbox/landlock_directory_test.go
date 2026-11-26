//go:build linux

package clisandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestConfinedCLIListingDoesNotGrantRootFileAccess(t *testing.T) {
	for _, provider := range []string{"muse-cli", "claude-code", "codex-cli", "cursor-cli"} {
		t.Run(provider, func(t *testing.T) {
			dir := t.TempDir()
			runner := filepath.Join(dir, "runner")
			if err := os.WriteFile(runner, nil, 0o700); err != nil {
				t.Fatal(err)
			}
			policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: provider, LandlockRunner: runner, PrivateHome: filepath.Join(dir, "home"), CredentialHome: dir}
			args, cleanup, err := LandlockArgs(policy, []string{"sh"}, dir, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			raw, err := os.ReadFile(args[2])
			if err != nil {
				t.Fatal(err)
			}
			var got landlockPolicy
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.ListPaths) != 1 || got.ListPaths[0] != "/" {
				t.Fatalf("missing list-only startup grant: %s", raw)
			}
			for _, paths := range [][]string{got.ReadPaths, got.WritePaths} {
				for _, path := range paths {
					if path == "/" {
						t.Fatalf("discovery must never grant file access at root: %s", raw)
					}
				}
			}
		})
	}
}

func TestMuseLandlockLiveEchoStartup(t *testing.T) {
	runner := os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	muse := os.Getenv("CODING_TEST_MUSE_BINARY")
	if runner == "" || muse == "" {
		t.Skip("set CODING_TEST_LANDLOCK_RUNNER and CODING_TEST_MUSE_BINARY")
	}
	root := t.TempDir()
	work, home := filepath.Join(root, "work"), filepath.Join(root, "home")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("TMPDIR", filepath.Join(home, "tmp"))
	t.Setenv("META_API_KEY", "")
	t.Setenv("MUSE_AUTH_PATH", "")
	t.Setenv("MUSE_NO_AUTO_UPDATE", "1")
	t.Setenv("MUSE_LOGIN", "0")
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "muse-cli", LandlockRunner: runner, PrivateHome: home, CredentialHome: home}
	// LandlockArgs creates the private-home layout, including TMPDIR, before
	// it creates the policy file there.
	args, cleanup, err := LandlockArgs(policy, []string{muse, "exec", "--provider", "echo", "--trust-workspace", "--json", "Reply SDK_STARTUP_OK"}, work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "echo: Reply SDK_STARTUP_OK") {
		t.Fatalf("Muse failed under the actual SDK policy: %v, %s", err, output)
	}
}

// The directory-open workaround does not require opening shared /tmp to
// Muse. Exercise the actual serialized SDK launch, not a hand-written policy.
func TestMuseLandlockKeepsSharedTmpFilesPrivate(t *testing.T) {
	runner := os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	if runner == "" {
		t.Skip("set CODING_TEST_LANDLOCK_RUNNER to a real Linux launcher")
	}
	root := t.TempDir()
	work := filepath.Join(root, "work")
	private := filepath.Join(root, "home")
	serverTmp := filepath.Join(root, "server-tmp")
	for _, dir := range []string{work, private, serverTmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", serverTmp)
	t.Setenv("HOME", private)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(private, ".config"))
	canary, err := os.CreateTemp("/tmp", "muse-outside-canary-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(canary.Name())
	if _, err := canary.WriteString("outside-canary"); err != nil {
		t.Fatal(err)
	}
	if err := canary.Close(); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "muse-cli", LandlockRunner: runner, PrivateHome: private, CredentialHome: private}
	script := `ls / >/dev/null || exit 10
if cat "$1" >/dev/null 2>&1; then exit 11; fi
if (printf changed > "$1") 2>/dev/null; then exit 12; fi
printf allowed > local-file
cat local-file`
	args, cleanup, err := LandlockArgs(policy, []string{"sh", "-c", script, "probe", canary.Name()}, work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	output, err := exec.CommandContext(t.Context(), args[0], args[1:]...).CombinedOutput()
	if err != nil || string(output) != "allowed" {
		t.Fatalf("Muse's directory workaround exposed another CLI's /tmp file: %v, %s", err, output)
	}
}
