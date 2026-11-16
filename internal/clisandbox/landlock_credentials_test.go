package clisandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A login kept under an XDG_CONFIG_HOME other than ~/.config (the RTS server
// sets one) is linked into the private home; it used to be looked for under
// ~/.config only, so a confined Cursor on the server account had no login.
func TestLinkCredentialFilesFollowsTheAccountConfigHome(t *testing.T) {
	root := t.TempDir()
	accountHome := filepath.Join(root, "account")
	configHome := filepath.Join(root, "xdg-config")
	login := filepath.Join(configHome, "cursor", "auth.json")
	if err := os.MkdirAll(filepath.Dir(login), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(login, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(root, "private")
	policy := &llmtypes.CLISecurityPolicy{
		Provider:       "cursor-cli",
		CredentialHome: accountHome,
		CredentialEnv:  map[string]string{"HOME": accountHome, "XDG_CONFIG_HOME": configHome},
	}
	granted, err := linkCredentialFiles(policy, private)
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 1 || granted[0] != login {
		t.Fatalf("granted %v, want only %s", granted, login)
	}
	if target, err := os.Readlink(filepath.Join(private, ".config", "cursor", "auth.json")); err != nil || target != login {
		t.Fatalf("private login link = %q, %v", target, err)
	}
}

// The server account's logins are found through this process's environment.
func TestLinkCredentialFilesServerAccountUsesProcessEnvironment(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "xdg-config")
	login := filepath.Join(configHome, "muse", "auth.json")
	if err := os.MkdirAll(filepath.Dir(login), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(login, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "server-home"))
	t.Setenv("XDG_CONFIG_HOME", configHome)
	granted, err := linkCredentialFiles(&llmtypes.CLISecurityPolicy{Provider: "muse-cli"}, filepath.Join(root, "private"))
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 1 || granted[0] != login {
		t.Fatalf("granted %v, want %s", granted, login)
	}
}

// PLAT-872-64: SDK login belongs to the selected account, not the service or Crew.
func TestCursorSDKCredentialProjectionUsesSelectedAccount(t *testing.T) {
	root := t.TempDir()
	account, other, private := filepath.Join(root, "selected"), filepath.Join(root, "other"), filepath.Join(root, "runtime")
	rel := ".cursor/sdk/auth.json"
	for _, home := range []string{account, other} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(home, rel)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, rel), []byte(`{"apiKey":"fixture"}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", other)
	granted, err := linkCredentialFiles(&llmtypes.CLISecurityPolicy{Provider: "cursor-cli", CredentialHome: account}, private)
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 1 || granted[0] != filepath.Join(account, rel) {
		t.Fatalf("wrong account grant: %v", granted)
	}
	target, err := os.Readlink(filepath.Join(private, rel))
	if err != nil || target != filepath.Join(account, rel) {
		t.Fatalf("wrong SDK login link: %q %v", target, err)
	}
}

// #2911: refreshing in runtime A must not leave runtime B using the invalidated
// account login. Values are fixtures; this exercises the actual launch layout,
// atomic CLI-style rename and cleanup, not a real OAuth service.
func TestClaudeAtomicCredentialRefreshReachesOtherRuntime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux credential projection")
	}
	root := t.TempDir()
	account := filepath.Join(root, "account")
	login := filepath.Join(account, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(login), 0700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"claudeAiOauth":{"accessToken":"fixture-before","refreshToken":"fixture-before","expiresAt":100}}`)
	after := []byte(`{"claudeAiOauth":{"accessToken":"fixture-after","refreshToken":"fixture-after","expiresAt":200}}`)
	if err := os.WriteFile(login, before, 0600); err != nil {
		t.Fatal(err)
	}
	homeA, homeB := filepath.Join(root, "runtime-a"), filepath.Join(root, "runtime-b")
	policy := &llmtypes.CLISecurityPolicy{Provider: "claude-code", CredentialHome: account}
	for _, home := range []string{homeA, homeB} {
		if _, err := linkCredentialFiles(policy, home); err != nil {
			t.Fatal(err)
		}
	}
	runner := filepath.Join(root, "runner")
	if err := os.WriteFile(runner, nil, 0700); err != nil {
		t.Fatal(err)
	}
	policy.Mode = llmtypes.CLISecurityModeIsolated
	policy.PrivateHome = homeA
	policy.LandlockRunner = runner
	host := exec.Command("/usr/bin/node", "bridge.cjs", "sdk.mjs", "/bin/sh", "-p")
	stop, err := LandlockChild(policy, host, host.Args[3:], root, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	original, err := os.Stat(login)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(homeA, ".claude", "refresh.tmp")
	if err := os.WriteFile(staged, after, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, filepath.Join(homeA, ".claude", ".credentials.json")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := os.ReadFile(filepath.Join(homeB, ".claude", ".credentials.json"))
		if err == nil && bytes.Equal(got, after) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("atomic refresh left canonical account and other runtime stale (fixture values not logged)")
		}
		time.Sleep(10 * time.Millisecond)
	}
	current, err := os.Stat(login)
	if err != nil || !os.SameFile(original, current) {
		t.Fatal("refresh replaced the inode admitted to other running sandboxes")
	}
	stop()
}
