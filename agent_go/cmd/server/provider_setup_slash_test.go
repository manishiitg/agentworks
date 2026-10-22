package server

import (
	"os"
	"strings"
	"testing"
)

// Account-changing commands are cancelled before Enter; usage/status and normal keys work.
func TestProviderUsageTerminalBlocksSlashCommandsButUsage(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	session := &providerSetupSession{id: "slash-test", action: "usage", status: "running", terminal: write}
	defer terminalSlashGuard.forget("provider-setup:slash-test")
	for _, key := range []string{"/", "l", "o", "g", "o", "u", "t", "\r", "/", "u", "s", "a", "g", "e", "\r"} {
		err := session.userInput(key)
		if err != nil && key != "\r" {
			t.Fatal(err)
		}
	}
	write.Close()
	raw := make([]byte, 4096)
	n, _ := read.Read(raw)
	got := string(raw[:n])
	if strings.Count(got, "\r") != 1 {
		t.Fatalf("only the /usage line may be submitted, got %q", got)
	}
	if !strings.Contains(got, "\x15") {
		t.Fatalf("the refused /logout line must be erased, got %q", got)
	}
	if !strings.HasSuffix(got, "/usage\r") {
		t.Fatalf("/usage must go through, got %q", got)
	}
}

func TestProviderSetupConfinesOnlyPersonalAccounts(t *testing.T) {
	for _, tc := range []struct {
		binding  string
		personal bool
	}{
		{"cursor-cli", false},            // the server account, started without a connection
		{"global:cursor-cli", false},     // the admin-managed server account (Providers screen)
		{"conn-4f2a9c", true},            // a personal account
		{"personal:cursor-cli:u1", true}, // any other connection id
	} {
		if got := providerSetupIsPersonalBinding("cursor-cli", tc.binding); got != tc.personal {
			t.Errorf("binding %q: personal = %v, want %v", tc.binding, got, tc.personal)
		}
	}
}

// The owner chose short input with native navigation, rather than a slash allowlist.
func TestProviderUsageTerminalAcceptsShortInputAndNavigation(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	session := &providerSetupSession{id: "short-input", action: "usage", status: "running", terminal: write}
	input := []string{"hi", "\r", "\x1b[A", "\x1b[B", "\x1b[D", "\x1b[C", "\t", "\x1b", "\x1b[200~héllo\x1b[201~", "\x15", "/status", "\r", "/usage", "\r"}
	for _, key := range input {
		if err := session.userInput(key); err != nil {
			t.Fatal(err)
		}
	}
	write.Close()
	raw := make([]byte, 4096)
	n, _ := read.Read(raw)
	if got := string(raw[:n]); got != strings.Join(input, "") {
		t.Fatal("short typing or navigation did not reach the terminal")
	}
}

func TestProviderUsageTerminalCapsInputAndCancelsAccountChanges(t *testing.T) {
	for _, command := range []string{"/login", "/logout", "/login extra", "/lo", "/config", "/model", "/resume", "/mcp"} {
		guard := &providerUsageInput{}
		if _, err := guard.filter(command); err != nil {
			t.Fatal(err)
		}
		data, err := guard.filter("\r")
		if err == nil || strings.ContainsAny(data, "\r\n") {
			t.Fatalf("account command was submitted: %s", command)
		}
	}
	guard := &providerUsageInput{}
	for _, input := range []string{"!echo hi", "  !echo hi", "@/proc/self/environ", "\x1b[200~@~/.claude/.credentials.json\x1b[201~"} {
		if data, err := guard.filter(input); err == nil || data != "" {
			t.Fatal("shell mode or a file mention reached the terminal")
		}
	}
	if data, err := guard.filter("\x1b[200~" + strings.Repeat("x", providerUsageInputLimit+1) + "\x1b[201~"); err == nil || data != "" {
		t.Fatal("long paste reached the terminal")
	}
	if _, err := guard.filter(strings.Repeat("x", providerUsageInputLimit)); err != nil {
		t.Fatal(err)
	}
	if data, err := guard.filter("y"); err == nil || data != "" {
		t.Fatal("typing exceeded the cap")
	}
	if _, err := guard.filter("\x7f"); err != nil {
		t.Fatal("backspace was blocked at the cap")
	}
	if _, err := guard.filter("y"); err != nil {
		t.Fatal(err)
	}
	if data, err := guard.filter("\r"); err != nil || data != "\r" {
		t.Fatal("Enter was blocked at the cap")
	}
}
