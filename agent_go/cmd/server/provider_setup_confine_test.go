package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

func TestConfineProviderSetupLeavesTheServerAccountAlone(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/echo", "hi")
	release, err := confineProviderSetup(cmd, "codex-cli", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(cmd.Args) != 2 || cmd.Args[0] != "/bin/echo" {
		t.Fatalf("the server account's terminal was rewritten: %v", cmd.Args)
	}
}

// Where the host cannot confine, a multi-user server refuses a personal account's terminal instead of
// running it open; a single-user install keeps working.
func TestConfineProviderSetupRefusesAnOpenTerminalOnAMultiUserHost(t *testing.T) {
	if _, available := security.CLILandlockRunner(); available {
		t.Skip("this host can confine; the refusal path needs a host without Landlock")
	}
	env := []string{"HOME=" + t.TempDir()}
	t.Setenv("MULTI_USER_MODE", "true")
	if _, err := confineProviderSetup(exec.CommandContext(context.Background(), "/bin/echo", "hi"), "pi-cli", env, true); err == nil {
		t.Fatal("a multi-user host started a personal account's terminal unconfined")
	}
	t.Setenv("MULTI_USER_MODE", "")
	if _, err := confineProviderSetup(exec.CommandContext(context.Background(), "/bin/echo", "hi"), "pi-cli", env, true); err != nil {
		t.Fatalf("single-user install broke: %v", err)
	}
}

// Exercise the actual setup launch under Linux Landlock: signing into a
// private home must never inherit or overwrite the service login (PLAT-615).
func TestPrivateProviderSetupKeepsItsOwnLogin(t *testing.T) {
	if _, available := security.CLILandlockRunner(); !available {
		t.Skip("requires the Linux Landlock launcher")
	}
	serverHome, privateHome := t.TempDir(), t.TempDir()
	for home, value := range map[string]string{serverHome: "server-login", privateHome: "private-login"} {
		if err := os.MkdirAll(filepath.Join(home, ".claude"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", serverHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(serverHome, ".claude"))
	env := []string{"HOME=" + privateHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(privateHome, ".claude"), "PATH=/usr/bin:/bin"}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", `cat "$CLAUDE_CONFIG_DIR/.credentials.json"; printf private-refreshed > "$CLAUDE_CONFIG_DIR/.credentials.json"`)
	cmd.Dir, cmd.Env = privateHome, env
	release, err := confineProviderSetup(cmd, "claude-code", env, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("confined setup failed: %v %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "private-login" {
		t.Fatalf("setup read another account: %s", output)
	}
	saved, err := os.ReadFile(filepath.Join(serverHome, ".claude", ".credentials.json"))
	if err != nil || string(saved) != "server-login" {
		t.Fatalf("private setup changed server login: %s %v", saved, err)
	}
	saved, err = os.ReadFile(filepath.Join(privateHome, ".claude", ".credentials.json"))
	if err != nil || string(saved) != "private-refreshed" {
		t.Fatalf("private setup failed to retain its login: %s %v", saved, err)
	}
}

func TestPrivateProviderAdmissionRejectsLegacyServerLink(t *testing.T) {
	home, server := t.TempDir(), t.TempDir()
	path := filepath.Join(home, ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(server, "login.json")
	if err := os.WriteFile(target, []byte("another-account"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if checkPrivateProviderCredentialLinks(home) == nil {
		t.Fatal("legacy server link admitted as a private login")
	}
	if err := detachPrivateProviderCredentialLinks(home); err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateProviderCredentialLinks(home); err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(target)
	if err != nil || string(value) != "another-account" {
		t.Fatalf("reconnect changed another account: %s %v", value, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("reconnect retained another account's login")
	}
}
