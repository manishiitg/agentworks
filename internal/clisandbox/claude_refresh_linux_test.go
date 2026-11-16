//go:build linux

package clisandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"golang.org/x/sys/unix"
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
					candidate = []byte(`{"claudeAiOauth":{"accessToken":"fixture-old-session","refreshToken":"fixture-old-session","expiresAt":300}}`)
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
	t.Log("[PROVIDER_CRED] event=contract provider=claude fixture=true result=passed canonical_updated=true inner_shell_refused=true")
}

// #2911: two completed runtimes race; timestamp order, not callback order,
// decides the saved login. Copy-back preserves that timestamp for later runs.
func TestClaudeCredentialNewestRuntimeWins(t *testing.T) {
	root := t.TempDir()
	account := filepath.Join(root, "account")
	src := filepath.Join(account, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(src), 0700); err != nil {
		t.Fatal(err)
	}
	old := []byte(`{"claudeAiOauth":{"accessToken":"fixture-old","refreshToken":"fixture-old","expiresAt":100}}`)
	newer := []byte(`{"claudeAiOauth":{"accessToken":"fixture-new","refreshToken":"fixture-new","expiresAt":300}}`)
	middle := []byte(`{"claudeAiOauth":{"accessToken":"fixture-middle","refreshToken":"fixture-middle","expiresAt":200}}`)
	if err := os.WriteFile(src, old, 0600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(src, time.Unix(100, 0), time.Unix(100, 0))
	policies := []*llmtypes.CLISecurityPolicy{}
	for _, name := range []string{"a", "b"} {
		home := filepath.Join(root, name)
		policy := &llmtypes.CLISecurityPolicy{Provider: "claude-code", CredentialHome: account, PrivateHome: home}
		if _, err := linkCredentialFiles(policy, home); err != nil {
			t.Fatal(err)
		}
		stop, err := beginClaudeCredentialSync(policy, home)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stop)
		policies = append(policies, policy)
	}
	for i, data := range [][]byte{newer, middle} {
		staged := filepath.Join(policies[i].PrivateHome, ".claude", "staged")
		if err := os.WriteFile(staged, data, 0600); err != nil {
			t.Fatal(err)
		}
		at := time.Unix([]int64{300, 200}[i], 0)
		if err := os.Chtimes(staged, at, at); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, filepath.Join(policies[i].PrivateHome, ".claude", ".credentials.json")); err != nil {
			t.Fatal(err)
		}
	}
	errors := make(chan error, 2)
	for _, policy := range policies {
		go func(p *llmtypes.CLISecurityPolicy) { errors <- flushClaudeCredentialSync(p) }(policy)
	}
	for range policies {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(src)
	if err != nil || !bytes.Equal(got, newer) {
		t.Fatal("older runtime replaced the newer login")
	}
	info, err := os.Stat(src)
	if err != nil || !info.ModTime().Equal(time.Unix(300, 0)) {
		t.Fatal("copy-back lost the credential timestamp")
	}
}

// Exercise the exact slot-side read program, including parent symlink and
// hardlink refusal, without copying any real credential or starting a login.
func TestClaudeSlotCredentialReaderRefusesAliases(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	os.Mkdir(real, 0700)
	file := filepath.Join(real, "refresh")
	os.WriteFile(file, []byte("fixture"), 0600)
	read := func(path string) ([]byte, error) {
		return exec.Command("/usr/bin/python3", "-I", "-c", claudeRefreshReadScript, path).Output()
	}
	got, err := read(file)
	if err != nil || len(got) < 8 || string(got[8:]) != "fixture" {
		t.Fatal("allowed fixture could not be read")
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := read(filepath.Join(link, "refresh")); err == nil {
		t.Fatal("reader followed a parent link")
	}
	if err := os.Link(file, filepath.Join(real, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := read(file); err == nil {
		t.Fatal("reader followed a hardlinked file")
	}
}

// A shared login's readers must survive replacement, without granting its
// parent directory or replacing the source's existing POSIX permissions.
func TestClaudeCredentialAtomicCopyPreservesACL(t *testing.T) {
	root := t.TempDir()
	account := filepath.Join(root, "account")
	home := filepath.Join(root, "private")
	src := filepath.Join(account, ".claude", ".credentials.json")
	dst := filepath.Join(home, ".claude", ".credentials.json")
	for _, p := range []string{filepath.Dir(src), filepath.Dir(dst)} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	before := []byte(`{"claudeAiOauth":{"accessToken":"fixture-old","refreshToken":"fixture-old","expiresAt":100}}`)
	after := []byte(`{"claudeAiOauth":{"accessToken":"fixture-new","refreshToken":"fixture-new","expiresAt":200}}`)
	if err := os.WriteFile(src, before, 0600); err != nil {
		t.Fatal(err)
	}
	acl := make([]byte, 44)
	binary.LittleEndian.PutUint32(acl, 2)
	entries := []struct {
		tag, perm uint16
		id        uint32
	}{{1, 6, 0xffffffff}, {2, 4, 12345}, {4, 0, 0xffffffff}, {16, 4, 0xffffffff}, {32, 0, 0xffffffff}}
	for i, e := range entries {
		at := 4 + i*8
		binary.LittleEndian.PutUint16(acl[at:], e.tag)
		binary.LittleEndian.PutUint16(acl[at+2:], e.perm)
		binary.LittleEndian.PutUint32(acl[at+4:], e.id)
	}
	if err := unix.Setxattr(src, "system.posix_acl_access", acl, 0); errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip("filesystem has no POSIX ACL support")
	} else if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	policy := &llmtypes.CLISecurityPolicy{Provider: "claude-code", CredentialHome: account, PrivateHome: home}
	if _, err := linkCredentialFiles(policy, home); err != nil {
		t.Fatal(err)
	}
	stop, err := beginClaudeCredentialSync(policy, home)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	staged := dst + ".tmp"
	if err := os.WriteFile(staged, after, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(staged, time.Unix(200, 0), time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staged, dst); err != nil {
		t.Fatal(err)
	}
	if err := flushClaudeCredentialSync(policy); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 65536)
	n, err := unix.Getxattr(src, "system.posix_acl_access", got)
	if err != nil || !bytes.Equal(got[:n], acl) {
		t.Fatal("atomic refresh changed the admitted readers")
	}
}

// An untracked old runtime could predate a human signing in to another account;
// do not silently import it solely because its file has a later timestamp.
func TestClaudeUntrackedRefreshCannotReplaceAccount(t *testing.T) {
	root := t.TempDir()
	account := filepath.Join(root, "account")
	home := filepath.Join(root, "private")
	src := filepath.Join(account, ".claude", ".credentials.json")
	dst := filepath.Join(home, ".claude", ".credentials.json")
	for _, dir := range []string{filepath.Dir(src), filepath.Dir(dst)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	before := []byte(`{"claudeAiOauth":{"accessToken":"fixture-current","refreshToken":"fixture-current","expiresAt":100}}`)
	after := []byte(`{"claudeAiOauth":{"accessToken":"fixture-unknown","refreshToken":"fixture-unknown","expiresAt":200}}`)
	if err := os.WriteFile(src, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, after, 0600); err != nil {
		t.Fatal(err)
	}
	if err := flushClaudeCredentialSync(&llmtypes.CLISecurityPolicy{Provider: "claude-code", CredentialHome: account, PrivateHome: home}); err == nil {
		t.Fatal("untracked runtime was admitted")
	}
	got, err := os.ReadFile(src)
	if err != nil || !bytes.Equal(got, before) {
		t.Fatal("untracked file replaced account")
	}
}
