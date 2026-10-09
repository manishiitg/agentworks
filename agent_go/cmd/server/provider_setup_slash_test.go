package server

import (
	"os"
	"strings"
	"testing"
)

// A manager's usage terminal of a shared provider account: typed slash commands follow the same allowlist as the
// coding agents' live terminals, so /logout (or /config, /model) cannot be run there; /usage still can.
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
		if err := session.userInput(key); err != nil {
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
	if !strings.Contains(got, "\x7f") {
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

// A manager's usage terminal reads limits only: free text, typed or pasted, never reaches the model on the shared account.
func TestProviderUsageTerminalDropsFreeText(t *testing.T) {
	t.Setenv(terminalSlashCommandsEnv, "")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	session := &providerSetupSession{id: "strict-test", action: "usage", status: "running", terminal: write}
	defer terminalSlashGuard.forget("provider-setup:strict-test")
	for _, input := range []string{
		"h", "i", "\r", // typed text, then Enter
		"\x1b[200~write me a poem\x1b[201~", "\r", // pasted text
		"\x1b[200~line one\nline two\x1b[201~", // pasted multi-line text
		"héllo wörld",                          // multi-byte text
		"\x1b[A",                               // an arrow key is navigation and goes through
		"/", "u", "s", "a", "g", "e", "\r",     // an allowed slash command goes through
	} {
		if err := session.userInput(input); err != nil {
			t.Fatal(err)
		}
	}
	write.Close()
	raw := make([]byte, 4096)
	n, _ := read.Read(raw)
	got := string(raw[:n])
	for _, leaked := range []string{"h", "poem", "line one", "héllo"} {
		if strings.Contains(strings.ReplaceAll(got, "/usage", ""), leaked) {
			t.Fatalf("free text %q reached the terminal: %q", leaked, got)
		}
	}
	if !strings.Contains(got, "\x1b[A") || !strings.HasSuffix(got, "/usage\r") {
		t.Fatalf("navigation keys and an allowed slash command must go through, got %q", got)
	}
}
