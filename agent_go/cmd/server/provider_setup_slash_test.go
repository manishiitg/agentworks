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
