//go:build linux

package clisandbox

import (
	"bytes"
	"context"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestClaudeCredentialRefreshRejectsInvalidOrReconnectedAccount(t *testing.T) {
	for _, mode := range []string{"empty_refresh", "account_reconnected", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			account := filepath.Join(root, "account")
			home := filepath.Join(root, "private")
			src := filepath.Join(account, ".claude", ".credentials.json")
			dst := filepath.Join(home, ".claude", ".credentials.json")
			os.MkdirAll(filepath.Dir(src), 0700)
			before := []byte(`{"claudeAiOauth":{"accessToken":"fixture-old","refreshToken":"fixture-old","expiresAt":100}}`)
			after := []byte(`{"claudeAiOauth":{"accessToken":"fixture-new","refreshToken":"fixture-new","expiresAt":200}}`)
			if err := os.WriteFile(src, before, 0600); err != nil {
				t.Fatal(err)
			}
			policy := &llmtypes.CLISecurityPolicy{Provider: "claude-code", CredentialHome: account}
			if _, err := linkCredentialFiles(policy, home); err != nil {
				t.Fatal(err)
			}
			stop, err := beginClaudeCredentialSync(policy, home)
			if err != nil {
				t.Fatal(err)
			}
			want := before
			if mode == "account_reconnected" {
				want = after
				if err := os.WriteFile(src, after, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "symlink" {
				staged := filepath.Join(home, ".claude", "candidate")
				candidate := after
				if mode == "empty_refresh" {
					candidate = []byte(`{"claudeAiOauth":{}}`)
				}
				if err := os.WriteFile(staged, candidate, 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "account_reconnected" {
					candidate = before
					if err := os.WriteFile(staged, candidate, 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Rename(staged, dst); err != nil {
					t.Fatal(err)
				}
			}
			stop()
			got, err := os.ReadFile(src)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatal("runtime overwrote an invalid or independently reconnected account")
			}
		})
	}
}

// The helper does not call an OAuth service. It is an actual confined process
// doing Claude's write-and-rename, then trying the same path from a native shell.
func TestClaudeRefreshFixtureProcess(t *testing.T) {
	dst := os.Getenv("CLAUDE_REFRESH_FIXTURE")
	if dst == "" {
		return
	}
	data := []byte(`{"claudeAiOauth":{"accessToken":"fixture-new","refreshToken":"fixture-new","expiresAt":200}}`)
	if err := os.WriteFile(dst+".tmp", data, 0600); err != nil {
		t.Fatal("outer CLI cannot stage its refresh")
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		t.Fatal("outer CLI cannot atomically refresh")
	}
	// Require the allowed side first; a missing fixture is not privacy proof.
	deadline := time.Now().Add(time.Second)
	for {
		got, err := os.ReadFile(dst)
		if err == nil && bytes.Equal(got, data) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("outer CLI cannot read refreshed login")
		}
		time.Sleep(time.Millisecond)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(cwd, "allowed-probe")
	if err := os.WriteFile(allowed, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `command -v cat >/dev/null || exit 66; cat "$2" >/dev/null || exit 67; if cat "$1" >/dev/null 2>&1; then exit 55; fi`, "probe", dst, allowed)
	if err := cmd.Run(); err != nil {
		t.Fatal("inner shell could read the login, or the shell failed to start")
	}
}

func TestClaudeRefreshUnderRealLandlockKeepsLoginOutOfShell(t *testing.T) {
	runner := os.Getenv("AGENTWORKS_LANDLOCK_RUNNER")
	if runner == "" {
		runner = os.Getenv("CODING_TEST_LANDLOCK_RUNNER")
	}
	if runner == "" {
		t.Skip("real Landlock launcher not configured")
	}
	base := os.Getenv("AGENTWORKS_LINUX_TEST_ROOT")
	if base == "" {
		base = "/var/tmp"
	}
	root, err := os.MkdirTemp(base, "claude-refresh-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	account, home, work := filepath.Join(root, "account"), filepath.Join(root, "private"), filepath.Join(root, "work")
	src, dst := filepath.Join(account, ".claude", ".credentials.json"), filepath.Join(home, ".claude", ".credentials.json")
	for _, dir := range []string{filepath.Dir(src), work} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	before := []byte(`{"claudeAiOauth":{"accessToken":"fixture-old","refreshToken":"fixture-old","expiresAt":100}}`)
	if err := os.WriteFile(src, before, 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{Mode: llmtypes.CLISecurityModeIsolated, Provider: "claude-code", PrivateHome: home, CredentialHome: account, LandlockRunner: runner}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestClaudeRefreshFixtureProcess$")
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_REFRESH_FIXTURE="+dst)
	stop, err := LandlockCmd(policy, cmd, work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("confined refresh contract failed: %v %s", err, out)
	}
	stop()
	got, err := os.ReadFile(src)
	if err != nil || bytes.Equal(got, before) {
		t.Fatal("confined atomic refresh did not reach the account")
	}
	t.Log("[CLAUDE_CREDENTIAL_REFRESH] fixture=true action=contract canonical_updated=true inner_shell_refused=true")
}
